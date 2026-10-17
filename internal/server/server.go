// Package server composes public and management HTTP listeners and owns shutdown.
package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const maxHeaderBytes = 64 << 10

type Lifecycle struct{ ready atomic.Bool }

func (l *Lifecycle) SetReady() { l.ready.Store(true) }
func (l *Lifecycle) Stop()     { l.ready.Store(false) }
func (l *Lifecycle) Ready(context.Context) error {
	if !l.ready.Load() {
		return errors.New("server is not accepting work")
	}
	return nil
}

type Config struct {
	PublicAddress     string
	ManagementAddress string
	PublicHandler     http.Handler
	ManagementHandler http.Handler
	ShutdownTimeout   time.Duration
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	IdleTimeout       time.Duration
	StreamClosers     []io.Closer
	Closers           []io.Closer
}

type Server struct {
	config     Config
	lifecycle  *Lifecycle
	public     *http.Server
	management *http.Server
	listeners  []net.Listener
	stopOnce   sync.Once
}

func New(config Config, lifecycle *Lifecycle) (*Server, error) {
	if config.PublicAddress == "" || config.PublicHandler == nil {
		return nil, errors.New("public address and handler are required")
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 10 * time.Second
	}
	if config.ReadHeaderTimeout <= 0 {
		config.ReadHeaderTimeout = 5 * time.Second
	}
	if config.ReadTimeout <= 0 {
		config.ReadTimeout = 15 * time.Second
	}
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = 60 * time.Second
	}
	if lifecycle == nil {
		lifecycle = &Lifecycle{}
	}
	s := &Server{config: config, lifecycle: lifecycle}
	s.public = &http.Server{Addr: config.PublicAddress, Handler: config.PublicHandler, ReadHeaderTimeout: config.ReadHeaderTimeout, ReadTimeout: config.ReadTimeout, IdleTimeout: config.IdleTimeout, MaxHeaderBytes: maxHeaderBytes}
	if config.ManagementAddress != "" && config.ManagementHandler != nil {
		s.management = &http.Server{Addr: config.ManagementAddress, Handler: config.ManagementHandler, ReadHeaderTimeout: config.ReadHeaderTimeout, ReadTimeout: config.ReadTimeout, IdleTimeout: config.IdleTimeout, MaxHeaderBytes: maxHeaderBytes}
	}
	return s, nil
}

func (s *Server) Lifecycle() *Lifecycle { return s.lifecycle }

// Run binds all configured listeners before publishing readiness.
func (s *Server) Run(ctx context.Context) error {
	publicListener, err := net.Listen("tcp", s.public.Addr)
	if err != nil {
		return err
	}
	s.listeners = append(s.listeners, publicListener)
	if s.management != nil {
		managementListener, listenErr := net.Listen("tcp", s.management.Addr)
		if listenErr != nil {
			_ = publicListener.Close()
			return listenErr
		}
		s.listeners = append(s.listeners, managementListener)
	}
	errorsChannel := make(chan error, len(s.listeners))
	go func() { errorsChannel <- normalizeServeError(s.public.Serve(publicListener)) }()
	if s.management != nil {
		go func() { errorsChannel <- normalizeServeError(s.management.Serve(s.listeners[1])) }()
	}
	s.lifecycle.SetReady()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
		defer cancel()
		return s.Shutdown(shutdownCtx)
	case serveErr := <-errorsChannel:
		s.lifecycle.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
		defer cancel()
		shutdownErr := s.Shutdown(shutdownCtx)
		if serveErr != nil {
			return serveErr
		}
		return shutdownErr
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	var result error
	s.stopOnce.Do(func() {
		s.lifecycle.Stop()
		// Stream closers terminate long-lived SSE handlers before HTTP drain.
		for _, closer := range s.config.StreamClosers {
			if err := closer.Close(); err != nil {
				result = errors.Join(result, err)
			}
		}
		if s.management != nil {
			if err := s.management.Shutdown(ctx); err != nil {
				result = errors.Join(result, err)
			}
		}
		if err := s.public.Shutdown(ctx); err != nil {
			result = errors.Join(result, err)
		}
		// Storage and other handler dependencies remain available until active
		// requests have drained.
		for _, closer := range s.config.Closers {
			if err := closer.Close(); err != nil {
				result = errors.Join(result, err)
			}
		}
	})
	return result
}

func normalizeServeError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
