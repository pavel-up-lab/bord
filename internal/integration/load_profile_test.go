package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bord/internal/api"
	"bord/internal/events"
	storage "bord/internal/storage/sqlite"
)

// TestLoadProfile is opt-in because it performs a bounded load run against the
// production SQLite, filesystem, HTTP, outbox and SSE adapters.
func TestLoadProfile(t *testing.T) {
	if os.Getenv("BORD_LOAD_PROFILE") != "1" {
		t.Skip("set BORD_LOAD_PROFILE=1 or run scripts/load-profile.sh")
	}
	cfg := loadConfig{
		Duration: loadDuration("BORD_LOAD_DURATION", 10*time.Second), ReadRPS: loadInt("BORD_LOAD_READ_RPS", 100),
		WriteRPS: loadInt("BORD_LOAD_WRITE_RPS", 10), SSEClients: loadInt("BORD_LOAD_SSE_CLIENTS", 50), SSERPS: loadInt("BORD_LOAD_SSE_RPS", 2),
	}
	if cfg.Duration < time.Second {
		t.Fatal("profile duration must be at least 1s")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	db, err := storage.Open(root + "/bord.db")
	if err != nil {
		t.Fatal(err)
	}
	if err = storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	rt, err := New(Config{DB: db, Blobs: root + "/blobs", LookupKey: bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if err = rt.SeedBoard(ctx, "general", "General"); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(4096, 256)
	defer hub.Close()
	dispatchCtx, stopDispatch := context.WithCancel(ctx)
	defer stopDispatch()
	go func() {
		_ = (&events.Dispatcher{Outbox: rt.Outbox, Hub: hub, Interval: 2 * time.Millisecond, Batch: 100}).Run(dispatchCtx)
	}()
	cursors, err := api.NewCursorCodec(bytes.Repeat([]byte{3}, 32), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, trusted, _ := net.ParseCIDR("127.0.0.0/8")
	handler := api.NewPublicRouter(api.Config{Public: Public{rt}, Abuse: rt.Abuse, Events: hub, CursorCodec: cursors, TrustedProxies: []*net.IPNet{trusted}, HeartbeatPeriod: time.Hour})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := server.Client()
	seed := createPostRequest(client, server.URL+"/api/v1/boards/general/threads", "seed", 0)
	if seed.Err || seed.ThreadID == "" {
		t.Fatal("failed to create load profile thread")
	}

	resources := startResourceSample()
	read := runPaced(cfg.Duration, cfg.ReadRPS, 16, func(int) requestResult {
		return loadRequest(client, http.MethodGet, server.URL+"/api/v1/threads/"+seed.ThreadID+"/posts?limit=100", nil, "", "")
	})
	write := runPaced(cfg.Duration, cfg.WriteRPS, 8, func(n int) requestResult {
		return createPostRequest(client, server.URL+"/api/v1/threads/"+seed.ThreadID+"/posts", "write", n)
	})
	sse := runSSEProfile(t, ctx, client, server.URL, seed.ThreadID, cfg)
	resourceResult := resources.stop()

	report := loadReport{Schema: 1, Commit: os.Getenv("BORD_LOAD_COMMIT"), RecordedAt: time.Now().UTC(), Toolchain: toolchainInfo(), Hardware: hardwareInfo(), Config: cfg, Read: read, Write: write, SSE: sse, Resources: resourceResult}
	report.SQLiteBusy = read.SQLiteBusy + write.SQLiteBusy + sse.Publish.SQLiteBusy
	report.Passed, report.Failures = evaluateLoad(report)
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("BORD_LOAD_OUT")
	if out == "" {
		out = root + "/load-profile.json"
	}
	if err = os.WriteFile(out, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("load profile: %s\n%s", out, encoded)
	if !report.Passed {
		t.Fatalf("minimum MVP profile failed: %s", strings.Join(report.Failures, "; "))
	}
}

type loadConfig struct {
	Duration   time.Duration `json:"-"`
	ReadRPS    int           `json:"read_rps"`
	WriteRPS   int           `json:"write_rps"`
	SSEClients int           `json:"sse_clients"`
	SSERPS     int           `json:"sse_events_per_second"`
}

func (c loadConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		DurationS  float64 `json:"duration_seconds"`
		ReadRPS    int     `json:"read_rps"`
		WriteRPS   int     `json:"write_rps"`
		SSEClients int     `json:"sse_clients"`
		SSERPS     int     `json:"sse_events_per_second"`
	}{c.Duration.Seconds(), c.ReadRPS, c.WriteRPS, c.SSEClients, c.SSERPS})
}

type latencyResult struct {
	Requests   int     `json:"requests"`
	Throughput float64 `json:"throughput_per_second"`
	P50MS      float64 `json:"p50_ms"`
	P95MS      float64 `json:"p95_ms"`
	P99MS      float64 `json:"p99_ms"`
	Errors     int     `json:"errors"`
	SQLiteBusy int     `json:"sqlite_busy"`
}

type sseResult struct {
	Clients              int           `json:"clients"`
	Publish              latencyResult `json:"publish"`
	ExpectedDeliveries   int           `json:"expected_deliveries"`
	ReceivedDeliveries   int           `json:"received_deliveries"`
	DeliveryThroughput   float64       `json:"delivery_throughput_per_second"`
	DeliveryP50MS        float64       `json:"delivery_p50_ms"`
	DeliveryP95MS        float64       `json:"delivery_p95_ms"`
	DeliveryP99MS        float64       `json:"delivery_p99_ms"`
	DisconnectsOrMissing int           `json:"disconnects_or_missing"`
}

type resourceResult struct {
	HeapBaselineBytes uint64 `json:"heap_baseline_bytes"`
	HeapPeakBytes     uint64 `json:"heap_peak_bytes"`
	HeapEndBytes      uint64 `json:"heap_end_bytes"`
	SysBaselineBytes  uint64 `json:"sys_baseline_bytes"`
	SysPeakBytes      uint64 `json:"sys_peak_bytes"`
	SysEndBytes       uint64 `json:"sys_end_bytes"`
	GoroutinesStart   int    `json:"goroutines_start"`
	GoroutinesPeak    int    `json:"goroutines_peak"`
	GoroutinesEnd     int    `json:"goroutines_end"`
}

type loadReport struct {
	Schema     int            `json:"schema"`
	Commit     string         `json:"commit"`
	RecordedAt time.Time      `json:"recorded_at"`
	Toolchain  map[string]any `json:"toolchain"`
	Hardware   map[string]any `json:"hardware"`
	Config     loadConfig     `json:"config"`
	Read       latencyResult  `json:"public_read"`
	Write      latencyResult  `json:"create_write"`
	SSE        sseResult      `json:"sse_fanout"`
	Resources  resourceResult `json:"resources"`
	SQLiteBusy int            `json:"sqlite_busy_total"`
	Passed     bool           `json:"minimum_mvp_profile_passed"`
	Failures   []string       `json:"failures,omitempty"`
}

type requestResult struct {
	Duration time.Duration
	Err      bool
	Busy     bool
	ThreadID string
	PostID   string
}

func runPaced(duration time.Duration, rate, concurrency int, fn func(int) requestResult) latencyResult {
	total := int(duration.Seconds() * float64(rate))
	jobs, results := make(chan int), make(chan requestResult, total)
	var workers sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := range jobs {
				results <- fn(n)
			}
		}()
	}
	started := time.Now()
	interval := time.Second / time.Duration(rate)
	for n := 0; n < total; n++ {
		if delay := time.Until(started.Add(time.Duration(n) * interval)); delay > 0 {
			time.Sleep(delay)
		}
		jobs <- n
	}
	close(jobs)
	workers.Wait()
	close(results)
	return summarizeRequests(results, time.Since(started))
}

func summarizeRequests(results <-chan requestResult, elapsed time.Duration) latencyResult {
	var samples []time.Duration
	var errors, busy int
	for result := range results {
		samples = append(samples, result.Duration)
		if result.Err {
			errors++
		}
		if result.Busy {
			busy++
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return latencyResult{Requests: len(samples), Throughput: float64(len(samples)) / elapsed.Seconds(), P50MS: percentile(samples, .50), P95MS: percentile(samples, .95), P99MS: percentile(samples, .99), Errors: errors, SQLiteBusy: busy}
}

func percentile(samples []time.Duration, q float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	return float64(samples[int(float64(len(samples)-1)*q)].Microseconds()) / 1000
}

func loadRequest(client *http.Client, method, url string, body []byte, key, forwarded string) requestResult {
	started := time.Now()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return requestResult{Duration: time.Since(started), Err: true}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	response, err := client.Do(req)
	if err != nil {
		return requestResult{Duration: time.Since(started), Err: true, Busy: isBusy(err.Error())}
	}
	contents, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	result := requestResult{Duration: time.Since(started), Err: readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300, Busy: isBusy(string(contents))}
	if !result.Err && method == http.MethodPost {
		var created api.CreateResult
		if json.Unmarshal(contents, &created) == nil {
			result.ThreadID, result.PostID = created.ThreadID, created.PostID
		}
	}
	return result
}

func createPostRequest(client *http.Client, url, phase string, n int) requestResult {
	body, _ := json.Marshal(map[string]string{"body": "bounded load profile", "session_proof": fmt.Sprintf("%032d", n), "delete_verifier": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))})
	key := fmt.Sprintf("%s-%020d", phase, n)
	// A distinct /24 exercises anti-abuse storage without hitting the intentionally
	// tiny per-subject allowance, which is not a SQLite capacity limit.
	forwarded := fmt.Sprintf("10.%d.%d.1", (n/255)%250+1, n%255)
	return loadRequest(client, http.MethodPost, url, body, key, forwarded)
}

type sseDelivery struct {
	event api.Event
	at    time.Time
}

func runSSEProfile(t *testing.T, parent context.Context, client *http.Client, baseURL, thread string, cfg loadConfig) sseResult {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	deliveries := make(chan sseDelivery, cfg.SSEClients*int(cfg.Duration.Seconds())*cfg.SSERPS+cfg.SSEClients)
	var connected sync.WaitGroup
	connected.Add(cfg.SSEClients)
	var disconnected atomic.Int64
	for i := 0; i < cfg.SSEClients; i++ {
		go func() {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/events?thread="+thread, nil)
			response, err := client.Do(req)
			if err != nil {
				disconnected.Add(1)
				connected.Done()
				return
			}
			connected.Done()
			defer response.Body.Close()
			scanner := bufio.NewScanner(response.Body)
			for scanner.Scan() {
				if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
					var event api.Event
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event.Type == "post.created" {
						deliveries <- sseDelivery{event, time.Now()}
					}
				}
			}
			if ctx.Err() == nil {
				disconnected.Add(1)
			}
		}()
	}
	connected.Wait()
	total := int(cfg.Duration.Seconds() * float64(cfg.SSERPS))
	requestResults := make(chan requestResult, total)
	sent := make(map[string]time.Time, total)
	started := time.Now()
	interval := time.Second / time.Duration(cfg.SSERPS)
	for n := 0; n < total; n++ {
		if delay := time.Until(started.Add(time.Duration(n) * interval)); delay > 0 {
			time.Sleep(delay)
		}
		result := createPostRequest(client, baseURL+"/api/v1/threads/"+thread+"/posts", "sse", n+1_000_000)
		requestResults <- result
		if !result.Err && result.PostID != "" {
			sent[result.PostID] = time.Now()
		}
	}
	close(requestResults)
	publish := summarizeRequests(requestResults, time.Since(started))
	expected := (publish.Requests - publish.Errors) * cfg.SSEClients
	latencies := make([]time.Duration, 0, expected)
	deadline := time.NewTimer(5 * time.Second)
collect:
	for len(latencies) < expected {
		select {
		case delivery := <-deliveries:
			if at, ok := sent[delivery.event.PostID]; ok {
				latencies = append(latencies, delivery.at.Sub(at))
			}
		case <-deadline.C:
			break collect
		}
	}
	if !deadline.Stop() {
		select {
		case <-deadline.C:
		default:
		}
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	missing := expected - len(latencies) + int(disconnected.Load())
	if missing < 0 {
		missing = 0
	}
	elapsed := time.Since(started)
	return sseResult{Clients: cfg.SSEClients, Publish: publish, ExpectedDeliveries: expected, ReceivedDeliveries: len(latencies), DeliveryThroughput: float64(len(latencies)) / elapsed.Seconds(), DeliveryP50MS: percentile(latencies, .50), DeliveryP95MS: percentile(latencies, .95), DeliveryP99MS: percentile(latencies, .99), DisconnectsOrMissing: missing}
}

type resourceSampler struct {
	stopChannel chan struct{}
	done        chan struct{}
	result      resourceResult
}

func startResourceSample() *resourceSampler {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	g := runtime.NumGoroutine()
	r := &resourceSampler{stopChannel: make(chan struct{}), done: make(chan struct{}), result: resourceResult{HeapBaselineBytes: m.HeapAlloc, HeapPeakBytes: m.HeapAlloc, SysBaselineBytes: m.Sys, SysPeakBytes: m.Sys, GoroutinesStart: g, GoroutinesPeak: g}}
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.sample()
			case <-r.stopChannel:
				r.sample()
				return
			}
		}
	}()
	return r
}

func (r *resourceSampler) sample() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.HeapAlloc > r.result.HeapPeakBytes {
		r.result.HeapPeakBytes = m.HeapAlloc
	}
	if m.Sys > r.result.SysPeakBytes {
		r.result.SysPeakBytes = m.Sys
	}
	g := runtime.NumGoroutine()
	if g > r.result.GoroutinesPeak {
		r.result.GoroutinesPeak = g
	}
	r.result.HeapEndBytes, r.result.SysEndBytes, r.result.GoroutinesEnd = m.HeapAlloc, m.Sys, g
}

func (r *resourceSampler) stop() resourceResult { close(r.stopChannel); <-r.done; return r.result }

func evaluateLoad(report loadReport) (bool, []string) {
	var failures []string
	check := func(ok bool, message string) {
		if !ok {
			failures = append(failures, message)
		}
	}
	check(report.Read.Errors == 0 && report.Read.P95MS <= 100 && report.Read.Throughput >= float64(report.Config.ReadRPS)*.95, "public read target")
	check(report.Write.Errors == 0 && report.Write.P95MS <= 500 && report.Write.Throughput >= float64(report.Config.WriteRPS)*.90, "create/write target")
	check(report.SSE.Publish.Errors == 0 && report.SSE.DeliveryP95MS <= 1000 && report.SSE.DisconnectsOrMissing == 0, "SSE fanout target")
	check(report.SQLiteBusy == 0, "SQLite busy must be zero")
	check(report.Resources.SysPeakBytes-report.Resources.SysBaselineBytes <= 64<<20, "additional runtime memory exceeds 64 MiB")
	check(report.Resources.GoroutinesEnd <= report.Resources.GoroutinesStart+10, "goroutines did not return within +10 of baseline")
	return len(failures) == 0, failures
}

func toolchainInfo() map[string]any {
	return map[string]any{"go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH}
}

func hardwareInfo() map[string]any {
	info := map[string]any{"logical_cpus": runtime.NumCPU()}
	if contents, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(contents), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
				info["cpu_model"] = strings.TrimSpace(v)
				break
			}
		}
	}
	if contents, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(contents), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kib, e := strconv.ParseUint(fields[1], 10, 64); e == nil {
						info["memory_bytes"] = kib * 1024
					}
				}
			}
		}
	}
	return info
}

func loadInt(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}
func loadDuration(name string, fallback time.Duration) time.Duration {
	if value, err := time.ParseDuration(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}
func isBusy(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "sqlite_busy") || strings.Contains(value, "database is locked") || strings.Contains(value, "database is busy")
}
