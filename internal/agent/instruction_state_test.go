package agent

import (
	"errors"
	"testing"
)

// fakeState is a minimal StateReader for testing TaskContextFromState without
// the ADK runtime. Get returns ErrAbsent for keys not in the map (mirroring
// the ADK session store's key-absent error).
type fakeState struct{ m map[string]any }

var errAbsent = errors.New("absent")

func (f fakeState) Get(key string) (any, error) {
	if v, ok := f.m[key]; ok {
		return v, nil
	}
	return nil, errAbsent
}

func TestTaskContextFromState_readsAllKeys(t *testing.T) {
	st := fakeState{m: map[string]any{
		"tenant_id":   "11111111-1111-7111-8111-111111111111",
		"author_gcid": "00000000-0000-7000-8000-000000001999",
		"post_text":   "Just earned my CSPO!",
	}}
	tc := TaskContextFromState(st)
	if tc.TenantID != "11111111-1111-7111-8111-111111111111" {
		t.Errorf("TenantID = %q", tc.TenantID)
	}
	if tc.AuthorGCID != "00000000-0000-7000-8000-000000001999" {
		t.Errorf("AuthorGCID = %q", tc.AuthorGCID)
	}
	if tc.PostText != "Just earned my CSPO!" {
		t.Errorf("PostText = %q; want the real post text (not a placeholder)", tc.PostText)
	}
}

func TestTaskContextFromState_missingKeysAreEmpty(t *testing.T) {
	tc := TaskContextFromState(fakeState{m: map[string]any{}})
	if tc.TenantID != "" || tc.AuthorGCID != "" || tc.PostText != "" {
		t.Errorf("missing keys should yield empty fields; got %+v", tc)
	}
}

func TestTaskContextFromState_nilStateIsEmpty(t *testing.T) {
	// TaskContext carries a map (Overrides) since ADR-197 M-B, so it is no longer
	// comparable with ==; assert each field is its zero value explicitly.
	tc := TaskContextFromState(nil)
	if tc.TenantID != "" || tc.AuthorGCID != "" || tc.PostText != "" || tc.Overrides != nil {
		t.Errorf("nil state should yield zero TaskContext; got %+v", tc)
	}
}

// The composed instruction must carry the real post text — the regression that
// stranded moderation (static placeholder "<filled at runtime>" reached the
// model → "post content is empty").
func TestComposeInstruction_fromState_carriesPostText(t *testing.T) {
	st := fakeState{m: map[string]any{"post_text": "spam buy now"}}
	got := ComposeInstruction(RoleModerator, TaskContextFromState(st))
	if !contains(got, "spam buy now") {
		t.Errorf("moderator instruction must contain the runtime post text")
	}
	if contains(got, "filled at runtime") {
		t.Errorf("moderator instruction must NOT contain the boot placeholder")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
