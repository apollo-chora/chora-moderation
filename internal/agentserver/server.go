// Package agentserver serves the Content Moderation crew's HTTP API.
//
// The crew's callers (chora-sharing) drive it through the agent-engine-style
// HTTP surface: POST {prefix}/reasoning_engine with a class_method dispatch
// (async_create_session / async_get_session / async_list_sessions /
// async_delete_session) and POST {prefix}/stream_reasoning_engine
// (async_stream_query) for the SSE agent run.
//
// This package re-implements that surface on top of
// google.golang.org/adk/runner + adk/session so the crew stays
// cloud-neutral: the ADK's own launcher stack
// (google.golang.org/adk/cmd/launcher) transitively requires a Google Cloud
// resource detector for its optional otel-to-cloud flag, which does not
// belong in a cloud-neutral service. The wire protocol (paths, payloads,
// JSON shapes, snake_case SSE framing) is preserved exactly.
package agentserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
)

const (
	defaultPort            = 8080
	defaultPathPrefix      = "/api"
	defaultMaxPayloadSize  = 10 * 1024 * 1024
	defaultSSEWriteTimeout = 120 * time.Second
)

// Config wires the crew's HTTP server.
type Config struct {
	// Port is the listen port (default 8080).
	Port int
	// AppName is the ADK session AppName, decoupled from any hosted
	// reasoning-engine ID: every session operation is scoped to it.
	AppName string
	// PathPrefix is the API path prefix (default "/api").
	PathPrefix string
	// MaxPayloadSize caps the request body (default 10 MiB).
	MaxPayloadSize int64
	// SSEWriteTimeout bounds a single SSE stream (default 120s).
	SSEWriteTimeout time.Duration
	// RootAgent is the crew's root agent (the sequential pipeline).
	RootAgent agent.Agent
	// SessionService is the session store (in-memory for this crew).
	SessionService session.Service
	// Plugins are the runner plugins (tenant propagation, termination).
	Plugins runner.PluginConfig
}

// Serve runs the HTTP server until ctx is cancelled.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.RootAgent == nil {
		return fmt.Errorf("agentserver: RootAgent is required")
	}
	if cfg.SessionService == nil {
		return fmt.Errorf("agentserver: SessionService is required")
	}
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	prefix := cfg.PathPrefix
	if prefix == "" {
		prefix = defaultPathPrefix
	}
	maxPayload := cfg.MaxPayloadSize
	if maxPayload == 0 {
		maxPayload = defaultMaxPayloadSize
	}
	sseTimeout := cfg.SSEWriteTimeout
	if sseTimeout == 0 {
		sseTimeout = defaultSSEWriteTimeout
	}

	ctrl := &queryController{
		appName:        cfg.AppName,
		sessionService: cfg.SessionService,
		rootAgent:      cfg.RootAgent,
		plugins:        cfg.Plugins,
		maxPayloadSize: maxPayload,
		sseTimeout:     sseTimeout,
	}

	router := mux.NewRouter().StrictSlash(true)
	router.Methods(http.MethodPost).
		Path(prefix + "/reasoning_engine").
		HandlerFunc(ctrl.query)
	router.Methods(http.MethodPost).
		Path(prefix + "/stream_reasoning_engine").
		HandlerFunc(ctrl.streamQuery)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("agentserver: serving on :%d (prefix %s, app_name %s)", port, prefix, cfg.AppName)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("agentserver: %w", err)
	}
	return nil
}

// queryController dispatches class_method requests to the session and
// streaming handlers, mirroring the ADK agent-engine API controller.
type queryController struct {
	appName        string
	sessionService session.Service
	rootAgent      agent.Agent
	plugins        runner.PluginConfig
	maxPayloadSize int64
	sseTimeout     time.Duration
}

// query serves POST {prefix}/reasoning_engine.
func (c *queryController) query(rw http.ResponseWriter, req *http.Request) {
	deadline := time.Now().Add(c.sseTimeout)
	rc := http.NewResponseController(rw)
	if err := rc.SetWriteDeadline(deadline); err != nil {
		log.Printf("agentserver: SetWriteDeadline failed: %v", err)
	}

	var payload []byte
	if req.Body != nil && req.Body != http.NoBody {
		var err error
		payload, err = io.ReadAll(io.LimitReader(req.Body, c.maxPayloadSize))
		if err != nil {
			err = fmt.Errorf("io.ReadAll with LimitReader failed: %w", err)
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		var query struct {
			ClassMethod string `json:"class_method"`
		}
		if err := json.Unmarshal(payload, &query); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		if err := c.handleQuery(req.Context(), rw, payload, query.ClassMethod); err != nil {
			log.Printf("agentserver: handleQuery failed: %v", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		return
	}

	if err := c.handleQuery(req.Context(), rw, payload, ""); err != nil {
		log.Printf("agentserver: handleQuery failed: %v", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}

func (c *queryController) handleQuery(ctx context.Context, rw http.ResponseWriter, payload []byte, classMethod string) error {
	switch classMethod {
	case "async_create_session":
		return c.createSession(ctx, rw, payload)
	case "async_get_session":
		return c.getSession(ctx, rw, payload)
	case "async_list_sessions":
		return c.listSessions(ctx, rw, payload)
	case "async_delete_session":
		return c.deleteSession(ctx, rw, payload)
	default:
		return fmt.Errorf("unrecognized class method: %v", classMethod)
	}
}

// streamQuery serves POST {prefix}/stream_reasoning_engine. It reuses the
// query framing (write deadline, payload limit) and dispatches through the
// same class_method lookup as the ADK's stream controller — only
// async_stream_query is registered there.
func (c *queryController) streamQuery(rw http.ResponseWriter, req *http.Request) {
	deadline := time.Now().Add(c.sseTimeout)
	rc := http.NewResponseController(rw)
	if err := rc.SetWriteDeadline(deadline); err != nil {
		log.Printf("agentserver: SetWriteDeadline failed: %v", err)
	}

	var payload []byte
	if req.Body != nil && req.Body != http.NoBody {
		var err error
		payload, err = io.ReadAll(io.LimitReader(req.Body, c.maxPayloadSize))
		if err != nil {
			err = fmt.Errorf("io.ReadAll with LimitReader failed: %w", err)
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		var query struct {
			ClassMethod string `json:"class_method"`
		}
		if err := json.Unmarshal(payload, &query); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		if query.ClassMethod != "async_stream_query" {
			err := fmt.Errorf("unrecognized class method: %v", query.ClassMethod)
			log.Printf("agentserver: handleQuery failed: %v", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := c.streamQueryEvents(req.Context(), rw, payload); err != nil {
		log.Printf("agentserver: streamQuery failed: %v", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}
