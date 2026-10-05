package agentserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// createSession handles async_create_session.
func (c *queryController) createSession(ctx context.Context, rw http.ResponseWriter, payload []byte) error {
	var req CreateSessionRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("json.Unmarshal() failed: %v", err)
	}

	resp, err := c.sessionService.Create(ctx, &session.CreateRequest{
		AppName: c.appName,
		UserID:  req.Input.UserID,
		State:   req.Input.State,
	})
	if err != nil {
		return fmt.Errorf("sessionService.Create() failed: %v", err)
	}

	if err := json.NewEncoder(rw).Encode(CreateSessionResponse{Output: fromSession(resp.Session)}); err != nil {
		return fmt.Errorf("json.NewEncoder failed: %v", err)
	}
	return nil
}

// getSession handles async_get_session.
func (c *queryController) getSession(ctx context.Context, rw http.ResponseWriter, payload []byte) error {
	var req GetSessionRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("json.Unmarshal() failed: %v", err)
	}

	resp, err := c.sessionService.Get(ctx, &session.GetRequest{
		AppName:   c.appName,
		UserID:    req.Input.UserID,
		SessionID: req.Input.SessionID,
	})
	if err != nil {
		return fmt.Errorf("sessionService.Get() failed: %v", err)
	}

	if err := json.NewEncoder(rw).Encode(GetSessionResponse{Output: fromSession(resp.Session)}); err != nil {
		return fmt.Errorf("json.NewEncoder failed: %v", err)
	}
	return nil
}

// listSessions handles async_list_sessions.
func (c *queryController) listSessions(ctx context.Context, rw http.ResponseWriter, payload []byte) error {
	var req ListSessionRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("json.Unmarshal() failed: %v", err)
	}

	resp, err := c.sessionService.List(ctx, &session.ListRequest{
		AppName: c.appName,
		UserID:  req.Input.UserID,
	})
	if err != nil {
		return fmt.Errorf("sessionService.List() failed: %v", err)
	}

	sessions := []SessionData{}
	for _, sess := range resp.Sessions {
		sessions = append(sessions, fromSession(sess))
	}

	if err := json.NewEncoder(rw).Encode(ListSessionResponse{Output: Sessions{Sessions: sessions}}); err != nil {
		return fmt.Errorf("json.NewEncoder failed: %v", err)
	}
	return nil
}

// deleteSession handles async_delete_session.
func (c *queryController) deleteSession(ctx context.Context, rw http.ResponseWriter, payload []byte) error {
	var req DeleteSessionRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("json.Unmarshal() failed: %v", err)
	}

	if err := c.sessionService.Delete(ctx, &session.DeleteRequest{
		AppName:   c.appName,
		UserID:    req.Input.UserID,
		SessionID: req.Input.SessionID,
	}); err != nil {
		return fmt.Errorf("sessionService.Delete() failed: %v", err)
	}

	if err := json.NewEncoder(rw).Encode(DeleteSessionResponse{}); err != nil {
		return fmt.Errorf("json.NewEncoder failed: %v", err)
	}
	return nil
}

// streamQueryEvents handles async_stream_query: it runs the crew's root
// agent over the ADK runner and emits one JSON line per event that carries
// an LLM response content, with errors emitted as JSON error lines.
func (c *queryController) streamQueryEvents(ctx context.Context, rw http.ResponseWriter, payload []byte) error {
	var req StreamQueryRequest

	// try to unmarshal StreamQueryRequest first
	err := json.Unmarshal(payload, &req)
	if err != nil {
		// try to unmarshal StreamQueryTextRequest
		var reqText StreamQueryTextRequest
		errText := json.Unmarshal(payload, &reqText)
		if errText != nil {
			err = fmt.Errorf("json.Unmarshal() failed both for StreamQueryRequest (%v) and StreamQueryTextRequest (%v)", err, errText)
			log.Print(err.Error())
			return err
		}
		// got text, create a full content based on that text
		req = StreamQueryRequest{
			ClassMethod: reqText.ClassMethod,
			Input: StreamQueryInput{
				UserID:    reqText.Input.UserID,
				SessionID: reqText.Input.SessionID,
				Message:   *genai.NewContentFromText(reqText.Input.Message, genai.RoleUser),
			},
		}
	}

	events, err := c.run(ctx, &req, &req.Input.Message)
	if err != nil {
		err = fmt.Errorf("run() failed: %w", err)
		log.Print(err.Error())
		return err
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	// from this moment on we must not return error. Instead, it should be
	// handled by using emitJSONError.

	for event, err := range events {
		log.Printf("Processing event: %+v err: %+v\n", event, err)
		if err != nil {
			log.Printf("error in events: %v\n", err)
			if e := emitJSONError(rw, err); e != nil {
				log.Printf("emitJSONError() failed: %v\n", e)
			}
			break
		}
		if event == nil {
			continue
		}
		if event.LLMResponse.Content == nil {
			continue
		}

		chunk := *event
		if err := emitJSON(rw, chunk); err != nil {
			log.Printf("emitJSON() failed: %v\n", err)
			if e := emitJSONError(rw, err); e != nil {
				log.Printf("emitJSONError() failed: %v\n", e)
			}
			break
		}
	}
	return nil
}

// run builds a per-request runner (auto-creating the session when the
// caller did not name one) and returns its SSE event stream.
func (c *queryController) run(ctx context.Context, req *StreamQueryRequest, message *genai.Content) (func(yield func(*session.Event, error) bool), error) {
	r, err := runner.New(runner.Config{
		AppName:           c.appName,
		Agent:             c.rootAgent,
		SessionService:    c.sessionService,
		PluginConfig:      c.plugins,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	return r.Run(ctx, req.Input.UserID, req.Input.SessionID, message, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}), nil
}
