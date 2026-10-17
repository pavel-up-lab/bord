package server

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func TestGracefulShutdownDropsReadinessAndClosesStreams(t *testing.T) {
	lifecycle := &Lifecycle{}
	var closed atomic.Bool
	service, err := New(Config{
		PublicAddress:   "127.0.0.1:0",
		PublicHandler:   http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		ShutdownTimeout: time.Second,
		Closers:         []io.Closer{closeFunc(func() error { closed.Store(true); return nil })},
	}, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for lifecycle.Ready(context.Background()) != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if lifecycle.Ready(context.Background()) != nil {
		t.Fatal("server never became ready")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown timed out")
	}
	if lifecycle.Ready(context.Background()) == nil {
		t.Fatal("readiness remained true")
	}
	if !closed.Load() {
		t.Fatal("stream closer was not called")
	}
}
