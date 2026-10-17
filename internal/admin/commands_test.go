package admin

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type mockClient struct{ calls []string }

func (m *mockClient) add(value string) { m.calls = append(m.calls, value) }
func (m *mockClient) Bootstrap(_ context.Context, id, role string, ttl time.Duration) (string, error) {
	m.add("bootstrap:" + id + ":" + role + ":" + ttl.String())
	return "one-time-credential", nil
}
func (m *mockClient) RevokeIdentity(_ context.Context, id, reason string) error {
	m.add("revoke:" + id + ":" + reason)
	return nil
}
func (m *mockClient) ListReports(context.Context) ([]Report, error) {
	m.add("list-reports")
	return []Report{{ID: "r1", PostID: "p1", Reason: "spam", CreatedAt: time.Unix(0, 0)}}, nil
}
func (m *mockClient) Hide(_ context.Context, id, reason string) error {
	m.add("hide:" + id + ":" + reason)
	return nil
}
func (m *mockClient) Restore(_ context.Context, id, reason string) error {
	m.add("restore:" + id + ":" + reason)
	return nil
}
func (m *mockClient) Close(_ context.Context, id, reason string) error {
	m.add("close:" + id + ":" + reason)
	return nil
}
func (m *mockClient) CreateBan(_ context.Context, subject string, ttl time.Duration, reason string) error {
	m.add("ban:" + subject + ":" + ttl.String() + ":" + reason)
	return nil
}
func (m *mockClient) RevokeBan(_ context.Context, id, reason string) error {
	m.add("unban:" + id + ":" + reason)
	return nil
}
func (m *mockClient) RevealIP(_ context.Context, id, reason string) (string, error) {
	m.add("reveal:" + id + ":" + reason)
	return "192.0.2.1", nil
}

func TestRunnerDelegatesEveryCommandThroughClient(t *testing.T) {
	mock := &mockClient{}
	var output bytes.Buffer
	runner := NewRunner(mock, &output)
	commands := [][]string{
		{"bootstrap", "alice", "administrator", "24h"},
		{"revoke", "alice", "--reason", "rotation"},
		{"list-reports"},
		{"hide", "p1", "--reason", "policy"},
		{"restore", "p1", "--reason", "appeal"},
		{"close", "t1", "--reason", "flood"},
		{"ban", "lookup:abc", "2h", "--reason", "spam"},
		{"unban", "b1", "--reason", "mistake"},
		{"reveal", "s1", "--reason", "investigation"},
	}
	for _, command := range commands {
		if err := runner.Execute(context.Background(), command); err != nil {
			t.Fatalf("%v: %v", command, err)
		}
	}
	want := []string{"bootstrap:alice:administrator:24h0m0s", "revoke:alice:rotation", "list-reports", "hide:p1:policy", "restore:p1:appeal", "close:t1:flood", "ban:lookup:abc:2h0m0s:spam", "unban:b1:mistake", "reveal:s1:investigation"}
	if strings.Join(mock.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %#v", mock.calls)
	}
	text := output.String()
	for _, fragment := range []string{"one-time-credential", "r1\tp1\tspam", "192.0.2.1"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("output %q missing %q", text, fragment)
		}
	}
}

func TestRunnerRejectsUnsafeOrMalformedCommandsBeforeClient(t *testing.T) {
	mock := &mockClient{}
	runner := NewRunner(mock, &bytes.Buffer{})
	invalid := [][]string{{}, {"unknown"}, {"hide", "p1"}, {"hide", "p1", "--reason", ""}, {"ban", "source", "forever", "--reason", "spam"}, {"reveal", "s1", "--reason", ""}}
	for _, command := range invalid {
		if err := runner.Execute(context.Background(), command); !errors.Is(err, ErrUsage) {
			t.Fatalf("%v: %v", command, err)
		}
	}
	if len(mock.calls) != 0 {
		t.Fatalf("unexpected calls: %#v", mock.calls)
	}
}
