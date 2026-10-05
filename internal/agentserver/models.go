package agentserver

// Wire models for the crew's HTTP API, mirroring the ADK agent-engine API
// request/response JSON shapes exactly.

import (
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// CreateSessionRequest is the payload for async_create_session.
type CreateSessionRequest struct {
	ClassMethod string             `json:"class_method"`
	Input       CreateSessionInput `json:"input"`
}

// CreateSessionInput contains the async_create_session input.
type CreateSessionInput struct {
	UserID string         `json:"user_id"`
	State  map[string]any `json:"state,omitempty"`
}

// CreateSessionResponse is the async_create_session response.
type CreateSessionResponse struct {
	Output SessionData `json:"output"`
}

// SessionData is the session projection returned by the session methods.
type SessionData struct {
	UserID         string          `json:"user_id"`
	LastUpdateTime float64         `json:"last_update_time"`
	AppName        string          `json:"app_name"`
	ID             string          `json:"id"`
	State          map[string]any  `json:"state"`
	Events         []session.Event `json:"events"`
}

// GetSessionRequest is the payload for async_get_session.
type GetSessionRequest struct {
	ClassMethod string          `json:"class_method"`
	Input       GetSessionInput `json:"input"`
}

// GetSessionInput contains the async_get_session input.
type GetSessionInput struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

// GetSessionResponse is the async_get_session response.
type GetSessionResponse struct {
	Output SessionData `json:"output"`
}

// ListSessionRequest is the payload for async_list_sessions.
type ListSessionRequest struct {
	ClassMethod string           `json:"class_method"`
	Input       ListSessionInput `json:"input"`
}

// ListSessionInput contains the async_list_sessions input.
type ListSessionInput struct {
	UserID string `json:"user_id"`
}

// ListSessionResponse is the async_list_sessions response.
type ListSessionResponse struct {
	Output Sessions `json:"output"`
}

// Sessions is the session list projection.
type Sessions struct {
	Sessions []SessionData `json:"sessions"`
}

// DeleteSessionRequest is the payload for async_delete_session.
type DeleteSessionRequest struct {
	ClassMethod string             `json:"class_method"`
	Input       DeleteSessionInput `json:"input"`
}

// DeleteSessionInput contains the async_delete_session input.
type DeleteSessionInput struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

// DeleteSessionResponse is the async_delete_session response.
type DeleteSessionResponse struct {
	Output string `json:"output"`
}

// StreamQueryRequest is the payload for async_stream_query with a full
// genai.Content message.
type StreamQueryRequest struct {
	ClassMethod string           `json:"class_method"`
	Input       StreamQueryInput `json:"input"`
}

// StreamQueryInput contains the async_stream_query input.
type StreamQueryInput struct {
	UserID    string        `json:"user_id"`
	SessionID string        `json:"session_id"`
	Message   genai.Content `json:"message"`
}

// StreamQueryTextRequest is the payload for async_stream_query with a
// simple text message.
type StreamQueryTextRequest struct {
	ClassMethod string               `json:"class_method"`
	Input       StreamQueryTextInput `json:"input"`
}

// StreamQueryTextInput contains the text-variant async_stream_query input.
type StreamQueryTextInput struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// fromSession projects an ADK session onto the wire SessionData.
func fromSession(sess session.Session) SessionData {
	stateMap := make(map[string]any)
	for k, v := range sess.State().All() {
		stateMap[k] = v
	}

	evs := []session.Event{}
	for ev := range sess.Events().All() {
		evs = append(evs, *ev)
	}

	return SessionData{
		UserID:         sess.UserID(),
		LastUpdateTime: float64(sess.LastUpdateTime().UnixNano()) / 1e9,
		AppName:        sess.AppName(),
		ID:             sess.ID(),
		State:          stateMap,
		Events:         evs,
	}
}
