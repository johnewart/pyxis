/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

// Lifecycle service for the managed llama-server: spawn, health-wait,
// model-load wait, stop, and the status snapshots a client renders verbatim.
//
// llmd is authoritative here. Every phase, readiness verdict and "which model
// is loaded" answer is measured from the live process and the server's own
// /health and /models endpoints — a client never recomputes them.
//
// Status is a newest-state fan-out, not an event log: subscribers receive the
// current snapshot on every transition, and a slow subscriber drops
// intermediate snapshots rather than blocking the lifecycle.

package llamacpp

import (
	"github.com/johnewart/pyxis/internal/serverutil"

	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
)

// Phase is the lifecycle phase of the managed llama-server. Values are ordered
// so a caller can assert transitions never move backwards.
type Phase int

// Lifecycle phases, in the order a successful start passes through them.
const (
	PhaseIdle Phase = iota
	PhaseStarting
	PhaseWaitingHealth
	PhaseLoadingModel
	PhaseReady
	PhaseStopping
	PhaseError
)

// String renders the phase for logs and tests.
func (p Phase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseStarting:
		return "spawning"
	case PhaseWaitingHealth:
		return "server-ready"
	case PhaseLoadingModel:
		return "model-loading"
	case PhaseReady:
		return "ready"
	case PhaseStopping:
		return "stopping"
	case PhaseError:
		return "error"
	default:
		return "unknown"
	}
}

// Status is llmd's authoritative snapshot of the managed server.
type Status struct {
	Phase         Phase
	Message       string
	ModelID       string
	ModelDir      string
	BinaryPath    string
	Port          int
	BaseURL       string
	PID           int
	Scope         serverutil.Scope
	Enabled       bool
	ActiveModelID string
	Error         string
}

// StartSpec is one Start request.
type StartSpec struct {
	ModelID    string
	ModelDir   string
	BinaryPath string
	Port       int
	ExtraArgs  []string
	Scope      serverutil.Scope
}

// ServiceOptions configures a Service. Registry and State are required; the
// timing and URL hooks exist so tests run in milliseconds against an
// httptest server instead of a real llama-server.
type ServiceOptions struct {
	Registry *Registry
	State    *ManagedStateStore
	// HTTPClient performs health/model probes.
	HTTPClient *http.Client
	// ReadyWait and ModelWait override the ported polling defaults.
	ReadyWait WaitOptions
	ModelWait ModelWaitOptions
	// BaseURLFor maps the chosen port to the server's OpenAI base URL.
	// Defaults to http://127.0.0.1:<port>/v1.
	BaseURLFor func(port int) string
	// SkipModelWait omits the /models wait (single-model servers that do not
	// expose the router endpoint).
	SkipModelWait bool
}

// serviceCacheKey is the single managed llama-server slot. One process at a
// time, app-wide — switching models replaces it rather than adding another.
const serviceCacheKey = "managed-llama-server"

// Service owns the managed llama-server lifecycle.
type Service struct {
	opts ServiceOptions

	mu     sync.Mutex
	status Status
	subs   map[int]chan Status
	nextID int
}

// NewService builds a Service. It does not touch the process or filesystem.
func NewService(opts ServiceOptions) *Service {
	if opts.BaseURLFor == nil {
		opts.BaseURLFor = func(port int) string {
			return fmt.Sprintf("http://127.0.0.1:%d/v1", port)
		}
	}
	return &Service{
		opts:   opts,
		status: Status{Phase: PhaseIdle},
		subs:   map[int]chan Status{},
	}
}

// managedState reads the app-global enabled/active-model pair, tolerating an
// unconfigured store.
func (s *Service) managedState() ManagedState {
	if s.opts.State == nil {
		return ManagedState{Enabled: true}
	}
	return s.opts.State.Get()
}

// Status returns the current snapshot, always carrying the app-global managed
// state so callers never have to merge two sources.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Service) snapshotLocked() Status {
	out := s.status
	managed := s.managedState()
	out.Enabled = managed.Enabled
	out.ActiveModelID = managed.ActiveModelID
	return out
}

// publish mutates the status and fans the new snapshot out to subscribers.
func (s *Service) publish(mutate func(*Status)) Status {
	s.mu.Lock()
	mutate(&s.status)
	snapshot := s.snapshotLocked()
	for _, ch := range s.subs {
		select {
		case ch <- snapshot:
		default:
			// Newest-state fan-out: a slow subscriber drops intermediate
			// snapshots rather than stalling the lifecycle.
		}
	}
	s.mu.Unlock()
	return snapshot
}

// Subscribe returns a channel of status snapshots plus an unsubscribe func.
// The current snapshot is delivered immediately so a late subscriber is never
// left waiting on a transition that already happened.
func (s *Service) Subscribe() (<-chan Status, func()) {
	ch := make(chan Status, 16)
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.subs[id] = ch
	ch <- s.snapshotLocked()
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		if existing, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(existing)
		}
		s.mu.Unlock()
	}
}

// Start spawns (or replaces) the single managed llama-server and waits until it
// is ready, emitting a status snapshot on every phase transition.
func (s *Service) Start(ctx context.Context, spec StartSpec) (Status, error) {
	modelID := strings.TrimSpace(spec.ModelID)
	if modelID == "" {
		return s.fail(errors.New("llamacpp: Start requires a model id"))
	}
	if s.opts.Registry == nil {
		return s.fail(errors.New("llamacpp: Start requires a process registry"))
	}

	port := spec.Port
	if port <= 0 {
		free, err := freePort()
		if err != nil {
			return s.fail(fmt.Errorf("allocate port: %w", err))
		}
		port = free
	}
	baseURL := s.opts.BaseURLFor(port)
	modelPath := spec.ModelID
	if dir := strings.TrimSpace(spec.ModelDir); dir != "" {
		modelPath = filepath.Join(dir, spec.ModelID)
	}

	s.publish(func(st *Status) {
		*st = Status{
			Phase:      PhaseStarting,
			Message:    "Starting llama-server",
			ModelID:    modelID,
			ModelDir:   spec.ModelDir,
			BinaryPath: spec.BinaryPath,
			Port:       port,
			BaseURL:    baseURL,
			Scope:      spec.Scope,
		}
	})

	// Replace whatever is running: one managed process, app-wide.
	_ = s.opts.Registry.Stop(ctx, serviceCacheKey)

	args := append([]string{
		"--model", modelPath,
		"--port", fmt.Sprintf("%d", port),
	}, spec.ExtraArgs...)
	record, err := s.opts.Registry.EnsureStarted(ctx, SpawnSpec{
		CacheKey:   serviceCacheKey,
		BinaryPath: spec.BinaryPath,
		Args:       args,
		ModelsDir:  spec.ModelDir,
		Port:       port,
	})
	if err != nil {
		return s.fail(err)
	}
	child := s.opts.Registry.child(serviceCacheKey)

	readyOpts := s.opts.ReadyWait
	readyOpts.HTTPClient = s.opts.HTTPClient
	if child != nil {
		readyOpts.Process = child
	}
	s.publish(func(st *Status) {
		st.Phase = PhaseWaitingHealth
		st.Message = "Waiting for llama-server to answer /health"
		st.PID = record.Pid
	})
	if err := WaitForServerReady(ctx, baseURL, readyOpts); err != nil {
		return s.fail(err)
	}

	if !s.opts.SkipModelWait {
		modelOpts := s.opts.ModelWait
		modelOpts.HTTPClient = s.opts.HTTPClient
		if child != nil {
			modelOpts.Process = child
		}
		s.publish(func(st *Status) {
			st.Phase = PhaseLoadingModel
			st.Message = "Loading " + modelID
		})
		candidates := []string{modelID, modelPath}
		if err := WaitForModelLoaded(ctx, baseURL, candidates, modelOpts); err != nil {
			return s.fail(err)
		}
	}

	return s.publish(func(st *Status) {
		st.Phase = PhaseReady
		st.Message = "llama-server ready"
		st.Error = ""
	}), nil
}

// Stop terminates the managed server and returns a non-running status.
func (s *Service) Stop(ctx context.Context) (Status, error) {
	s.publish(func(st *Status) {
		st.Phase = PhaseStopping
		st.Message = "Stopping llama-server"
	})
	var err error
	if s.opts.Registry != nil {
		err = s.opts.Registry.Stop(ctx, serviceCacheKey)
	}
	status := s.publish(func(st *Status) {
		*st = Status{Phase: PhaseIdle, Message: "llama-server stopped"}
	})
	return status, err
}

// fail records an error status and returns it alongside the error.
func (s *Service) fail(err error) (Status, error) {
	status := s.publish(func(st *Status) {
		st.Phase = PhaseError
		st.Message = err.Error()
		st.Error = err.Error()
		st.PID = 0
	})
	return status, err
}

// SetEnabled and SetActiveModel expose the app-global managed state so callers
// have a single authority for it.
func (s *Service) SetEnabled(enabled bool) (ManagedState, error) {
	if s.opts.State == nil {
		return ManagedState{Enabled: enabled}, errors.New("llamacpp: no managed state store")
	}
	state, err := s.opts.State.SetEnabled(enabled)
	s.publish(func(*Status) {})
	return state, err
}

// SetActiveModel pins the single model the managed server should serve.
func (s *Service) SetActiveModel(modelID string) (ManagedState, error) {
	if s.opts.State == nil {
		return ManagedState{}, errors.New("llamacpp: no managed state store")
	}
	state, err := s.opts.State.SetActiveModel(modelID)
	s.publish(func(*Status) {})
	return state, err
}

// freePort asks the OS for an unused localhost port.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
