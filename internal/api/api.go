// Package api exposes the role HTTP endpoints the L7 Caddy in
// weft-agent active-probes :
//
//	GET /ready   — 200 when the local Forgejo is healthy ; 503 otherwise.
//	GET /info    — JSON {install, node, dc, version, up} for ops dashboards.
//	GET /health  — same as /ready (conventional probe path).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

// State is what the API surfaces. Filled by the reconcile loop each
// tick ; the API holders read it via atomic snapshot so the role
// endpoint never blocks the reconcile.
type State struct {
	Up          bool
	InstallName string
	NodeName    string
	DC          string
	Version     string
}

// Server is a thin HTTP wrapper around an atomically-swapped State.
type Server struct {
	addr  string
	srv   *http.Server
	state atomic.Pointer[State]
}

// New returns a configured but not-yet-started Server.
func New(addr, installName, nodeName, dc string) *Server {
	s := &Server{addr: addr}
	s.state.Store(&State{InstallName: installName, NodeName: nodeName, DC: dc})

	mux := http.NewServeMux()
	mux.HandleFunc("/ready", s.ready)
	mux.HandleFunc("/health", s.ready)
	mux.HandleFunc("/info", s.info)

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start binds the listener + serves in a goroutine.
func (s *Server) Start() error {
	if s.srv == nil {
		return errors.New("api: server not constructed")
	}
	go func() { _ = s.srv.ListenAndServe() }()
	return nil
}

// Shutdown stops the listener. Idempotent.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// Update is what the reconcile loop calls after each tick.
func (s *Server) Update(st State) { s.state.Store(&st) }

func (s *Server) snapshot() State {
	p := s.state.Load()
	if p == nil {
		return State{}
	}
	return *p
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	st := s.snapshot()
	if !st.Up {
		http.Error(w, "forgejo not up", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	st := s.snapshot()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Install string `json:"install"`
		Node    string `json:"node"`
		DC      string `json:"dc"`
		Version string `json:"version"`
		Up      bool   `json:"up"`
	}{
		Install: st.InstallName,
		Node:    st.NodeName,
		DC:      st.DC,
		Version: st.Version,
		Up:      st.Up,
	})
}
