package agent

import (
	"strings"
	"testing"
)

// moderatorGolden / criticGolden capture the EXACT pre-ADR-197-M-B composer
// output for goldenCtx. They lock byte-identity: with NO operator overrides the
// SAFE-block routing (overrideOr) MUST reproduce these bytes verbatim — the
// behaviour-neutral guarantee of ADR-197 M-B.2. Regenerate ONLY via a deliberate
// prompt change (then re-justify the diff), never to make a drift pass silently.
const moderatorGolden = "## [CONTEXT]\nYou are running inside the Chora learning platform as the Moderator sub-agent of the Content Moderation crew (P6 Reflection). You gate user-generated posts on the C+ ChoraCircle feed before they reach other learners.\nTenant: tenant-x. Author GCID: author-y.\n\n## [ROLE]\nYou are the Moderator — first-pass moderation verdict. A second agent (the Critic) will review your verdict; do NOT try to anticipate the Critic, just give your honest first-pass call. You are NOT generating content; you are gating.\n\n## [EXAMPLES]\nExample (pass): input=\"Just earned my CSPO! Eira helped me through the product-backlog refinement atoms.\" → {\"verdict\":\"pass\",\"reason\":\"benign learning-achievement post; mentions Familiar by name (acceptable Chora artefact).\"}\nExample (refine): input=\"u guys r the worst, this platform sucks\" → {\"verdict\":\"refine\",\"reason\":\"profanity-adjacent + non-constructive; suggest rephrasing toward specific feedback.\"}\nExample (reject): input=\"DM me to buy CSPO exam answers, contact +65...\" → {\"verdict\":\"reject\",\"reason\":\"solicitation + PII (phone number) + academic integrity violation.\"}\n\n## [AUDIENCE]\nYour verdict is consumed by the Critic sub-agent (the next step in the reflection pipeline). The final consumer is the chora-sharing service which persists the verdict to ContentPolicyViolationLog.\n\n## [TASK]\nModerate this post against Chora policy:\n```\nEarned my CSPO!\n```\n- Issue ONE of: pass | refine | reject.\n- pass    = post is acceptable as-is.\n- refine  = post needs author edits before it can land.\n- reject  = post violates policy; will not land.\n- Consider: profanity, harassment, PII leakage (emails/phone/GCIDs), solicitation, academic integrity (exam-answer trading), advertising, hate speech.\n\n## [EXPECTED OUTPUT]\nJSON: {\"verdict\": \"pass\"|\"refine\"|\"reject\", \"reason\": string}. NEVER write commentary outside the JSON.\n"

const criticGolden = "## [CONTEXT]\nYou are running inside the Chora learning platform as the Critic sub-agent of the Content Moderation crew (P6 Reflection). You review the Moderator's first-pass verdict and either confirm it or override with a justified final verdict. You are LLM-as-judge.\nTenant: tenant-x. Author GCID: author-y.\n\n## [ROLE]\nYou are the Critic — the second pass that emits the FINAL verdict the chora-sharing service will act on. The Moderator made a first call; your job is to reflect on whether it's right. You can confirm (agree) or override (disagree with justification).\n\n## [EXAMPLES]\nExample (confirm): Moderator said pass for \"Earned my CSPO!\" → {\"final_verdict\":\"pass\",\"agreement\":\"confirm\",\"reason\":\"Moderator's pass verdict is correct; no policy concerns.\"}\nExample (override pass→refine): Moderator said pass for \"Earned my CSPO! DM @phyllis for tutoring.\" → {\"final_verdict\":\"refine\",\"agreement\":\"override\",\"reason\":\"Moderator missed the off-platform solicitation (DM @ handle). Refine to remove the contact CTA.\"}\nExample (override reject→refine): Moderator rejected \"Anyone else find the rubric confusing?\" → {\"final_verdict\":\"refine\",\"agreement\":\"override\",\"reason\":\"Moderator over-flagged a legitimate constructive question. Refine the wording for clarity, but do not reject.\"}\n\n## [AUDIENCE]\nYour verdict IS the final answer. The chora-sharing service persists it to ContentPolicyViolationLog and (if pass) publishes the post to the C+ feed.\n\n## [TASK]\nReview the Moderator's verdict for this post:\n```\nEarned my CSPO!\n```\n- The Moderator's verdict is in the conversation history above.\n- Decide: confirm (agree with Moderator) or override (give a different final_verdict + reason).\n- Apply LLM-as-judge rigour: would the same input land the same verdict on a different day? Consistency matters.\n- final_verdict must be one of: pass | refine | reject.\n\n## [EXPECTED OUTPUT]\nJSON: {\"final_verdict\": \"pass\"|\"refine\"|\"reject\", \"agreement\": \"confirm\"|\"override\", \"reason\": string}. NEVER write commentary outside the JSON.\n"

var goldenCtx = TaskContext{TenantID: "tenant-x", AuthorGCID: "author-y", PostText: "Earned my CSPO!"}

// --- Behaviour-neutral golden (Overrides nil → byte-identical), BOTH roles ---

func TestComposeInstruction_noOverrides_byteIdenticalGolden_moderator(t *testing.T) {
	got := ComposeInstruction(RoleModerator, goldenCtx)
	if got != moderatorGolden {
		t.Errorf("moderator no-override output drifted from golden.\n--- got ---\n%q\n--- want ---\n%q", got, moderatorGolden)
	}
}

func TestComposeInstruction_noOverrides_byteIdenticalGolden_critic(t *testing.T) {
	got := ComposeInstruction(RoleCritic, goldenCtx)
	if got != criticGolden {
		t.Errorf("critic no-override output drifted from golden.\n--- got ---\n%q\n--- want ---\n%q", got, criticGolden)
	}
}

// --- Override substitutes role/task/examples; verdict-JSON + content + safety fixed ---

func TestComposeInstruction_overrides_substituteSafeBlocksOnly_moderator(t *testing.T) {
	ctx := goldenCtx
	ctx.Overrides = map[string]string{
		"role":     "OVERRIDE-ROLE-mod",
		"task":     "OVERRIDE-TASK-mod",
		"examples": "OVERRIDE-EXAMPLES-mod",
	}
	got := ComposeInstruction(RoleModerator, ctx)

	for _, m := range []string{"OVERRIDE-ROLE-mod", "OVERRIDE-TASK-mod", "OVERRIDE-EXAMPLES-mod"} {
		if !strings.Contains(got, m) {
			t.Errorf("SAFE-block override %q not substituted", m)
		}
	}
	if strings.Contains(got, "You are the Moderator — first-pass moderation verdict") {
		t.Error("role override must replace the embedded [ROLE] body")
	}
	if strings.Contains(got, "- Issue ONE of: pass | refine | reject.") {
		t.Error("task override must replace the embedded [TASK] verdict-path instructions")
	}
	if strings.Contains(got, "Example (pass): input=") {
		t.Error("examples override must replace the embedded [EXAMPLES] body")
	}

	// NON-overridable: [EXPECTED OUTPUT] verdict-JSON block byte-identical to golden.
	const expected = "## [EXPECTED OUTPUT]\nJSON: {\"verdict\": \"pass\"|\"refine\"|\"reject\", \"reason\": string}. NEVER write commentary outside the JSON.\n"
	if !strings.Contains(got, expected) {
		t.Error("[EXPECTED OUTPUT] verdict-JSON block must NOT be overridable")
	}
	// NON-overridable: post-text content.
	if !strings.Contains(got, "```\nEarned my CSPO!\n```") {
		t.Error("post-text content must NOT be overridable")
	}
	// NON-overridable: safety policy-category list.
	if !strings.Contains(got, "- Consider: profanity, harassment, PII leakage") {
		t.Error("safety policy-category list must NOT be overridable")
	}
}

func TestComposeInstruction_overrides_substituteSafeBlocksOnly_critic(t *testing.T) {
	ctx := goldenCtx
	ctx.Overrides = map[string]string{
		"role":     "OVERRIDE-ROLE-crit",
		"task":     "OVERRIDE-TASK-crit",
		"examples": "OVERRIDE-EXAMPLES-crit",
	}
	got := ComposeInstruction(RoleCritic, ctx)

	for _, m := range []string{"OVERRIDE-ROLE-crit", "OVERRIDE-TASK-crit", "OVERRIDE-EXAMPLES-crit"} {
		if !strings.Contains(got, m) {
			t.Errorf("SAFE-block override %q not substituted", m)
		}
	}
	if strings.Contains(got, "You are the Critic — the second pass") {
		t.Error("role override must replace the embedded [ROLE] body")
	}
	if strings.Contains(got, "- The Moderator's verdict is in the conversation history above.") {
		t.Error("task override must replace the embedded [TASK] critic instructions")
	}
	if strings.Contains(got, "Example (confirm): Moderator said pass") {
		t.Error("examples override must replace the embedded [EXAMPLES] body")
	}

	// NON-overridable: [EXPECTED OUTPUT] verdict-JSON block byte-identical to golden.
	const expected = "## [EXPECTED OUTPUT]\nJSON: {\"final_verdict\": \"pass\"|\"refine\"|\"reject\", \"agreement\": \"confirm\"|\"override\", \"reason\": string}. NEVER write commentary outside the JSON.\n"
	if !strings.Contains(got, expected) {
		t.Error("[EXPECTED OUTPUT] verdict-JSON block must NOT be overridable")
	}
	// NON-overridable: post-text content.
	if !strings.Contains(got, "```\nEarned my CSPO!\n```") {
		t.Error("post-text content must NOT be overridable")
	}
}

// --- Empty override value → embedded fallback (arch-clean) ---

func TestComposeInstruction_emptyOverrideValue_fallsBackToEmbedded(t *testing.T) {
	ctx := goldenCtx
	ctx.Overrides = map[string]string{"role": "", "task": "", "examples": ""}
	if got := ComposeInstruction(RoleModerator, ctx); got != moderatorGolden {
		t.Errorf("empty override values must fall back to embedded (byte-identical golden)\n got=%q", got)
	}
}

// --- prompt_overrides_json reads (JSON string / native map / malformed / absent) ---

func TestTaskContextFromState_promptOverrides_jsonString(t *testing.T) {
	st := fakeState{m: map[string]any{
		"prompt_overrides_json": `{"role":"R","task":"T","examples":"E"}`,
	}}
	tc := TaskContextFromState(st)
	if tc.Overrides["role"] != "R" || tc.Overrides["task"] != "T" || tc.Overrides["examples"] != "E" {
		t.Errorf("overrides not parsed from JSON string: %+v", tc.Overrides)
	}
}

func TestTaskContextFromState_promptOverrides_nativeMap(t *testing.T) {
	st := fakeState{m: map[string]any{
		"prompt_overrides_json": map[string]string{"task": "T"},
	}}
	if tc := TaskContextFromState(st); tc.Overrides["task"] != "T" {
		t.Errorf("native map overrides not parsed: %+v", tc.Overrides)
	}
}

func TestTaskContextFromState_promptOverrides_malformedIsNil(t *testing.T) {
	st := fakeState{m: map[string]any{"prompt_overrides_json": "{not valid json"}}
	if tc := TaskContextFromState(st); tc.Overrides != nil {
		t.Errorf("malformed overrides must degrade to nil; got %+v", tc.Overrides)
	}
}

func TestTaskContextFromState_promptOverrides_absentIsNil(t *testing.T) {
	st := fakeState{m: map[string]any{"post_text": "hi"}}
	if tc := TaskContextFromState(st); tc.Overrides != nil {
		t.Errorf("absent overrides must be nil; got %+v", tc.Overrides)
	}
}
