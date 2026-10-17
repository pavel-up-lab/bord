// Package telemetry defines the bounded operational telemetry vocabulary.
package telemetry

import (
	"errors"
	"fmt"
	"strings"
)

type Metric struct {
	Name   string
	Labels []string
}

type Event struct {
	Name   string
	Fields []string
}

var Metrics = []Metric{
	{Name: "bord_http_request_duration_seconds", Labels: []string{"method", "route", "status_class"}},
	{Name: "bord_sqlite_busy_total", Labels: []string{"operation"}},
	{Name: "bord_sse_disconnect_total", Labels: []string{"reason"}},
	{Name: "bord_sse_reset_total", Labels: []string{"scope"}},
	{Name: "bord_media_reject_total", Labels: []string{"reason"}},
	{Name: "bord_rate_limit_total", Labels: []string{"operation", "subject_kind"}},
	{Name: "bord_retention_records_total", Labels: []string{"record_kind", "result"}},
}

var Events = []Event{
	{Name: "http.request.completed", Fields: []string{"request_id", "method", "route", "status_class", "duration_bucket"}},
	{Name: "sqlite.busy", Fields: []string{"request_id", "operation"}},
	{Name: "sse.disconnected", Fields: []string{"request_id", "reason"}},
	{Name: "sse.reset", Fields: []string{"request_id", "scope"}},
	{Name: "media.rejected", Fields: []string{"request_id", "reason"}},
	{Name: "rate_limit.rejected", Fields: []string{"request_id", "operation", "subject_kind"}},
	{Name: "retention.completed", Fields: []string{"record_kind", "result", "count"}},
}

var forbidden = []string{"ip", "address", "token", "secret", "credential", "body", "content", "session", "board_id", "thread_id", "post_id", "user"}

var metricLabels = map[string]struct{}{
	"method": {}, "route": {}, "status_class": {}, "operation": {}, "reason": {},
	"scope": {}, "subject_kind": {}, "record_kind": {}, "result": {},
}

func Validate() error {
	seen := map[string]struct{}{}
	for _, metric := range Metrics {
		if err := uniqueName(seen, metric.Name); err != nil {
			return err
		}
		for _, label := range metric.Labels {
			if _, ok := metricLabels[label]; !ok {
				return fmt.Errorf("metric %s has unbounded label %s", metric.Name, label)
			}
			if sensitive(label) {
				return fmt.Errorf("metric %s has sensitive label %s", metric.Name, label)
			}
		}
	}
	for _, event := range Events {
		if err := uniqueName(seen, event.Name); err != nil {
			return err
		}
		for _, field := range event.Fields {
			if sensitive(field) {
				return fmt.Errorf("event %s has sensitive field %s", event.Name, field)
			}
		}
	}
	return nil
}

func uniqueName(seen map[string]struct{}, name string) error {
	if name == "" {
		return errors.New("telemetry name is empty")
	}
	if _, ok := seen[name]; ok {
		return fmt.Errorf("duplicate telemetry name %s", name)
	}
	seen[name] = struct{}{}
	return nil
}

func sensitive(name string) bool {
	name = strings.ToLower(name)
	for _, part := range forbidden {
		if name == part || strings.HasSuffix(name, "_"+part) || strings.HasPrefix(name, part+"_") {
			return true
		}
	}
	return false
}
