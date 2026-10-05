package agentserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
	"iter"
)

// stubAgent returns a minimal agent that yields one event carrying the
// supplied text, recording that it was invoked.
func stubAgent(t *testing.T, text string, invoked *bool) agent.Agent {
	t.Helper()
	a, err := agent.New(agent.Config{
		Name:        "stub",
		Description: "test stub",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				*invoked = true
				ev := session.NewEvent("inv-stub")
				ev.LLMResponse.Content = &genai.Content{
					Role:  "model",
					Parts: []*genai.Part{{Text: text}},
				}
				yield(ev, nil)
			}
		},
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a
}

func testController(rootAgent agent.Agent) *queryController {
	return &queryController{
		appName:        "chora-moderation",
		sessionService: session.InMemoryService(),
		rootAgent:      rootAgent,
		maxPayloadSize: defaultMaxPayloadSize,
		sseTimeout:     defaultSSEWriteTimeout,
	}
}

func post(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/reasoning_engine", strings.NewReader(body))
	rw := httptest.NewRecorder()
	h(rw, req)
	return rw
}

func TestSessionLifecycle_overHTTP(t *testing.T) {
	invoked := false
	ctrl := testController(stubAgent(t, "unused", &invoked))

	// create
	rw := post(t, ctrl.query, `{"class_method":"async_create_session","input":{"user_id":"user-1","state":{"post_text":"hello"}}}`)
	if rw.Code != http.StatusOK {
		t.Fatalf("create: code = %d, body = %s", rw.Code, rw.Body.String())
	}
	var created CreateSessionResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &created); err != nil {
		t.Fatalf("create: unmarshal: %v", err)
	}
	if created.Output.ID == "" || created.Output.UserID != "user-1" || created.Output.AppName != "chora-moderation" {
		t.Fatalf("create: unexpected output: %+v", created.Output)
	}
	if created.Output.State["post_text"] != "hello" {
		t.Fatalf("create: state not carried: %+v", created.Output.State)
	}

	// get
	rw = post(t, ctrl.query, fmt.Sprintf(`{"class_method":"async_get_session","input":{"user_id":"user-1","session_id":%q}}`, created.Output.ID))
	if rw.Code != http.StatusOK {
		t.Fatalf("get: code = %d, body = %s", rw.Code, rw.Body.String())
	}
	var got GetSessionResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatalf("get: unmarshal: %v", err)
	}
	if got.Output.ID != created.Output.ID {
		t.Fatalf("get: id = %q, want %q", got.Output.ID, created.Output.ID)
	}

	// list
	rw = post(t, ctrl.query, `{"class_method":"async_list_sessions","input":{"user_id":"user-1"}}`)
	if rw.Code != http.StatusOK {
		t.Fatalf("list: code = %d, body = %s", rw.Code, rw.Body.String())
	}
	var listed ListSessionResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &listed); err != nil {
		t.Fatalf("list: unmarshal: %v", err)
	}
	if len(listed.Output.Sessions) != 1 || listed.Output.Sessions[0].ID != created.Output.ID {
		t.Fatalf("list: unexpected sessions: %+v", listed.Output.Sessions)
	}

	// delete
	rw = post(t, ctrl.query, fmt.Sprintf(`{"class_method":"async_delete_session","input":{"user_id":"user-1","session_id":%q}}`, created.Output.ID))
	if rw.Code != http.StatusOK {
		t.Fatalf("delete: code = %d, body = %s", rw.Code, rw.Body.String())
	}

	// get after delete fails
	rw = post(t, ctrl.query, fmt.Sprintf(`{"class_method":"async_get_session","input":{"user_id":"user-1","session_id":%q}}`, created.Output.ID))
	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("get after delete: code = %d, want 500", rw.Code)
	}
}

func TestQuery_unknownClassMethod(t *testing.T) {
	ctrl := testController(stubAgent(t, "unused", new(bool)))
	rw := post(t, ctrl.query, `{"class_method":"async_nope","input":{}}`)
	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "unrecognized class method") {
		t.Fatalf("body = %q, want unrecognized class method", rw.Body.String())
	}
}

func TestQuery_malformedJSON(t *testing.T) {
	ctrl := testController(stubAgent(t, "unused", new(bool)))
	rw := post(t, ctrl.query, `{not json`)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rw.Code)
	}
}

func TestStreamQuery_wrongClassMethod(t *testing.T) {
	ctrl := testController(stubAgent(t, "unused", new(bool)))
	rw := post(t, ctrl.streamQuery, `{"class_method":"async_create_session","input":{}}`)
	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rw.Code)
	}
}

func TestStreamQuery_emitsSnakeCaseEventLines(t *testing.T) {
	invoked := false
	ctrl := testController(stubAgent(t, "hello moderation", &invoked))

	req := httptest.NewRequest(http.MethodPost, "/api/stream_reasoning_engine",
		strings.NewReader(`{"class_method":"async_stream_query","input":{"user_id":"user-1","message":"a post"}}`))
	rw := httptest.NewRecorder()
	ctrl.streamQuery(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rw.Code, rw.Body.String())
	}
	if !invoked {
		t.Fatal("stub agent was not invoked")
	}
	ct := rw.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	lines := strings.Split(strings.TrimSpace(rw.Body.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 event line, got %d: %q", len(lines), rw.Body.String())
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("event line not JSON: %v (%q)", err, lines[0])
	}
	if ev["invocation_id"] != "inv-stub" {
		t.Fatalf("invocation_id = %v, want inv-stub", ev["invocation_id"])
	}
	if _, ok := ev["llm_response"]; !ok {
		// model.LLMResponse is embedded in session.Event, so its fields are
		// projected at the top level under their snake_case names.
		if _, ok := ev["content"]; !ok {
			t.Fatalf("event line has no llm_response/content projection: %v", ev)
		}
	}
	if !strings.Contains(lines[0], "hello moderation") {
		t.Fatalf("event line missing the model text: %q", lines[0])
	}
}

func TestStreamQuery_malformedJSON(t *testing.T) {
	ctrl := testController(stubAgent(t, "unused", new(bool)))

	// Malformed outer JSON fails the class_method envelope parse → 400,
	// matching the ADK controller.
	rw := post(t, ctrl.streamQuery, `{not json`)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rw.Code)
	}

	// Valid envelope but an input that is neither StreamQueryRequest nor
	// StreamQueryTextRequest reaches the stream handler, which fails → 500.
	rw = post(t, ctrl.streamQuery, `{"class_method":"async_stream_query","input":"not-an-object"}`)
	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rw.Code)
	}
}

func TestServe_endToEnd(t *testing.T) {
	invoked := false
	root := stubAgent(t, "unused", &invoked)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Serve(ctx, Config{
			Port:           port,
			AppName:        "chora-moderation",
			RootAgent:      root,
			SessionService: session.InMemoryService(),
		})
	}()

	// wait for the server to come up
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%d/api/reasoning_engine", port),
			"application/json",
			strings.NewReader(`{"class_method":"async_create_session","input":{"user_id":"u","state":{"k":"v"}}}`))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never came up: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var created CreateSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Output.ID == "" || created.Output.State["k"] != "v" {
		t.Fatalf("unexpected session: %+v", created.Output)
	}
}

func TestServe_requiresRootAgentAndSessionService(t *testing.T) {
	if err := Serve(context.Background(), Config{SessionService: session.InMemoryService()}); err == nil {
		t.Error("Serve without RootAgent should fail")
	}
	if err := Serve(context.Background(), Config{RootAgent: stubAgent(t, "x", new(bool))}); err == nil {
		t.Error("Serve without SessionService should fail")
	}
}

// Ensure the payload limit is enforced on the reasoning_engine endpoint.
func TestQuery_payloadLimit(t *testing.T) {
	ctrl := testController(stubAgent(t, "unused", new(bool)))
	ctrl.maxPayloadSize = 10
	rw := post(t, ctrl.query, `{"class_method":"async_create_session","input":{"user_id":"user-1"}}`)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rw.Code)
	}
}
