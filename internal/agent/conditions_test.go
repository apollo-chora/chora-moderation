package agent

// RED-first test for the ADR-197 M-A condition extractor for the moderation
// crew. The only prompt discriminant is the role (moderator vs critic) — the
// post text is the input content, NOT a discriminant, and is never surfaced.

import "testing"

func TestModerationConditions_role(t *testing.T) {
	if c := ModerationConditions(RoleModerator)(nil); c["role"] != "moderator" {
		t.Errorf("moderator role: got %q", c["role"])
	}
	if c := ModerationConditions(RoleCritic)(nil); c["role"] != "critic" {
		t.Errorf("critic role: got %q", c["role"])
	}
}
