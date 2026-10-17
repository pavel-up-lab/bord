package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersionSmoke(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != version || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRejectsUnknownInlineModeBeforeStartingTUI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--config", t.TempDir() + "/state.json", "--inline", "sixel"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--inline") {
		t.Fatalf("err=%v", err)
	}
}
