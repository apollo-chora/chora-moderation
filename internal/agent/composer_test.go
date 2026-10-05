package agent

import (
	"strings"
	"testing"
)

func TestComposeModerator_emitsSixCreateBlocksInOrder(t *testing.T) {
	ctx := TaskContext{
		TenantID:   "tenant-x",
		AuthorGCID: "author-y",
		PostText:   "Earned my CSPO!",
	}
	got := ComposeInstruction(RoleModerator, ctx)
	assertCreateBlocksOrdered(t, "moderator", got)
}

func TestComposeCritic_emitsSixCreateBlocksInOrder(t *testing.T) {
	ctx := TaskContext{
		TenantID:   "tenant-x",
		AuthorGCID: "author-y",
		PostText:   "Earned my CSPO!",
	}
	got := ComposeInstruction(RoleCritic, ctx)
	assertCreateBlocksOrdered(t, "critic", got)
}

func assertCreateBlocksOrdered(t *testing.T, role, got string) {
	t.Helper()
	blocks := []string{
		"[CONTEXT]",
		"[ROLE]",
		"[EXAMPLES]",
		"[AUDIENCE]",
		"[TASK]",
		"[EXPECTED OUTPUT]",
	}
	lastIdx := -1
	for _, b := range blocks {
		i := strings.Index(got, b)
		if i < 0 {
			t.Errorf("%s: CREATE block %q missing", role, b)
			continue
		}
		if i <= lastIdx {
			t.Errorf("%s: %q at %d after %d (out of order)", role, b, i, lastIdx)
		}
		lastIdx = i
	}
}

func TestCompose_isDeterministic(t *testing.T) {
	ctx := TaskContext{TenantID: "t", AuthorGCID: "a", PostText: "hi"}
	for _, r := range []AgentRole{RoleModerator, RoleCritic} {
		x := ComposeInstruction(r, ctx)
		y := ComposeInstruction(r, ctx)
		if x != y {
			t.Errorf("role %s: not deterministic (IMDA D2 break)", r)
		}
	}
}

func TestCompose_moderatorAndCriticDiverge(t *testing.T) {
	ctx := TaskContext{TenantID: "t", AuthorGCID: "a", PostText: "hi"}
	mod := ComposeInstruction(RoleModerator, ctx)
	crit := ComposeInstruction(RoleCritic, ctx)
	if mod == crit {
		t.Error("Moderator and Critic prompts must be distinct (P6 Reflection)")
	}
}

func TestCompose_postTextSurfacesInTaskBlock(t *testing.T) {
	post := "specific-marker-text-7f3a2c"
	got := ComposeInstruction(RoleModerator, TaskContext{
		TenantID:   "t",
		AuthorGCID: "a",
		PostText:   post,
	})
	if !strings.Contains(got, post) {
		t.Errorf("post text must surface in TASK block; got prompt without %q", post)
	}
}

func TestCompose_moderatorMentionsThreeVerdictPaths(t *testing.T) {
	got := ComposeInstruction(RoleModerator, TaskContext{TenantID: "t"})
	for _, verdict := range []string{"pass", "refine", "reject"} {
		if !strings.Contains(got, verdict) {
			t.Errorf("Moderator prompt must mention %q verdict path", verdict)
		}
	}
}

func TestCompose_criticEmitsFinalVerdictField(t *testing.T) {
	got := ComposeInstruction(RoleCritic, TaskContext{TenantID: "t"})
	if !strings.Contains(got, "final_verdict") {
		t.Error("Critic prompt must declare final_verdict output field")
	}
	if !strings.Contains(got, "agreement") {
		t.Error("Critic prompt must declare agreement field (confirm|override)")
	}
}

func TestComposeUnknownRoleDefaultsToModerator(t *testing.T) {
	defaultRole := ComposeInstruction(AgentRole("unknown"), TaskContext{TenantID: "t"})
	moderator := ComposeInstruction(RoleModerator, TaskContext{TenantID: "t"})
	if defaultRole != moderator {
		t.Error("unknown role must default to Moderator")
	}
}

func TestMandatorySpanAttributes_coversReflectionPair(t *testing.T) {
	required := []string{
		"chora.tenant_id",
		"chora.author_gcid",
		"chora.mana_tier",
		"chora.crew_kind",
		"chora.moderation.role",
		"gen_ai.request.model",
		"gen_ai.usage.output_tokens",
	}
	got := MandatorySpanAttributes()
	seen := make(map[string]struct{}, len(got))
	for _, k := range got {
		seen[k] = struct{}{}
	}
	for _, r := range required {
		if _, ok := seen[r]; !ok {
			t.Errorf("MandatorySpanAttributes missing %q", r)
		}
	}
}

func TestMandatorySpanAttributes_noDuplicates(t *testing.T) {
	got := MandatorySpanAttributes()
	seen := make(map[string]struct{}, len(got))
	for _, k := range got {
		if _, dup := seen[k]; dup {
			t.Errorf("duplicate attribute %q", k)
		}
		seen[k] = struct{}{}
	}
}
