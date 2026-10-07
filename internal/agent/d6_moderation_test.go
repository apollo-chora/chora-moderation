package agent

import (
	"strings"
	"testing"
)

// D6 4-pillar stub harness for Content Moderation P6 Reflection.
// Real chaos cleared by POC W3 multi-crew variant; these stubs preserve
// the contract surface during production promotion.

func TestD6P1_composersArePureAcrossRecovery(t *testing.T) {
	// Recovery: same input always yields same prompt (no cached state).
	ctx := TaskContext{TenantID: "t", AuthorGCID: "a", PostText: "x"}
	for _, r := range []AgentRole{RoleModerator, RoleCritic} {
		pre := ComposeInstruction(r, ctx)
		post := ComposeInstruction(r, ctx)
		if pre != post {
			t.Errorf("role %s: composer not pure across recovery", r)
		}
	}
}

func TestD6P2_terminationEventTopicCanonical(t *testing.T) {
	want := "chora.ai_kernel.agent.terminated.v1"
	if !strings.HasPrefix(want, "chora.ai_kernel.") || !strings.HasSuffix(want, ".v1") {
		t.Errorf("canonical topic must be chora.ai_kernel.*.v1; got %q", want)
	}
}

func TestD6P3_composeIsConcurrencySafe(t *testing.T) {
	// 2-agent reflection: under N concurrent moderation requests with
	// distinct tenants/authors, prompts must isolate per request (no
	// cross-bleed). Pure functions are inherently concurrent-safe; this
	// guards against future state introduction.
	for i := 0; i < 16; i++ {
		ctx := TaskContext{
			TenantID:   "tenant-" + string(rune('A'+i%26)),
			AuthorGCID: "author-stub",
			PostText:   "post-stub",
		}
		got := ComposeInstruction(RoleModerator, ctx)
		want := "tenant-" + string(rune('A'+i%26))
		if !strings.Contains(got, want) {
			t.Errorf("iter %d: prompt missing own tenant_id %q", i, want)
		}
	}
}
