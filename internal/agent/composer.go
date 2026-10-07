// Package agent holds the Content Moderation P6 Reflection crew's
// per-agent CREATE-pattern composers + D6 attribute contract.
//
// 2-agent P6 Reflection pattern per crew-composition SKILL §1 + §2:
//   - Moderator (T2 Flash Preview) — first-pass verdict on the input post
//   - Critic    (T1 Pro Preview, LLM-as-judge) — reviews + finalises verdict
//
// The pipeline is sequential: Moderator → Critic. Critic's verdict is the
// final answer surfaced to the caller. Critic's role is reflection:
// either confirm or override Moderator's verdict with justification.
//
// Phyllis Step 9 (ChoraCircle CSPO completion post) — gates user-generated
// posts before they hit the C+ feed.
//
// Unlike Familiar (P1 + per-instance) and Recommender (P1), Moderation is
// 2-agent and author-facing only (NOT learner-facing). No learner_persona
// axis — moderation policy is tenant-scoped, not learner-scoped.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TaskContext is the per-call moderation context.
type TaskContext struct {
	TenantID   string
	AuthorGCID string
	PostText   string
	// Optional: ChoraCircle-specific signals from upstream (likes/follows
	// of the author; whether the post references atom_ids; etc.). Iter 4.5.

	// Overrides carries operator-supplied SAFE-block replacements (ADR-197 M-B)
	// keyed by segment_id. Only the SAFE blocks are overridable: "role", "task",
	// "examples". The [EXPECTED OUTPUT] verdict-JSON block, the post-text content,
	// and the safety policy-category list are NEVER routed through an override.
	// Absent / nil / empty ⇒ the embedded blocks are used, keeping the composed
	// prompt byte-identical to pre-override behaviour (proven by the golden test).
	// The orchestrator stamps prompt_overrides_json into session state (read by
	// TaskContextFromState). Mirrors the shipped qgen_question pattern.
	Overrides map[string]string
}

// AgentRole identifies which composer to use (Moderator vs Critic).
type AgentRole string

const (
	RoleModerator AgentRole = "moderator"
	RoleCritic    AgentRole = "critic"
)

// StateReader is the minimal readonly session-state seam used to build a
// per-turn TaskContext. It matches the ADK ReadonlyState contract
// (Get(key) → (value, error); error when the key is absent), so an
// llmagent.InstructionProvider can pass `rc.ReadonlyState()` directly while
// tests substitute a fake.
type StateReader interface {
	Get(key string) (any, error)
}

// TaskContextFromState builds a per-call TaskContext from the session state the
// caller populated at async_create_session (tenant_id / author_gcid /
// post_text). This is the runtime wiring the boot-time static instruction
// could never carry: previously the agents were composed once at startup with
// a literal "<filled at runtime>" placeholder for PostText (the deferred "Iter
// 4.5" InstructionProvider was never wired), so the model received the
// placeholder and rejected every post as "empty content". Missing / non-string
// keys yield empty fields (safe fallback handled downstream by safe()).
func TaskContextFromState(state StateReader) TaskContext {
	if state == nil {
		return TaskContext{}
	}
	return TaskContext{
		TenantID:   stateString(state, "tenant_id"),
		AuthorGCID: stateString(state, "author_gcid"),
		PostText:   stateString(state, "post_text"),
		// ADR-197 M-B (read side) — operator SAFE-block overrides. Absent on the
		// dominant path ⇒ nil ⇒ byte-identical to pre-override behaviour.
		Overrides: readPromptOverridesFromState(state, "prompt_overrides_json"),
	}
}

// readPromptOverridesFromState decodes the operator SAFE-block overrides the
// orchestrator threads into session state under prompt_overrides_json (ADR-197
// M-B). Two wire forms are tolerated: a JSON-object STRING (json.dumps of a
// map[string]string), OR a native map[string]any/map[string]string from the
// ReasoningEngine JSON round-trip. Both normalise to map[string]string. Absent /
// empty / malformed ⇒ nil — fail-soft: a broken override map MUST degrade to the
// embedded blocks (byte-identical default), never panic and never partially
// apply. Mirrors the shipped qgen_question readPromptOverridesFromState.
func readPromptOverridesFromState(state StateReader, key string) map[string]string {
	raw, err := state.Get(key)
	if err != nil {
		return nil
	}
	var jsonBytes []byte
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		jsonBytes = []byte(v)
	case map[string]string:
		if len(v) == 0 {
			return nil
		}
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		jsonBytes = b
	}
	var out map[string]string
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		return nil // malformed → embedded blocks (fail-soft)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// overrideOr returns the operator override for segmentID when present and
// non-empty, else the embedded block. ADR-197 M-B SAFE-block seam: applied ONLY
// at the role / task / examples assembly sites — NEVER to the [EXPECTED OUTPUT]
// verdict-JSON block, the post-text content, or the safety policy-category list.
// A nil / absent override map (the dominant path) always returns embedded ⇒
// byte-identical output. Mirrors the shipped qgen_question overrideOr.
func overrideOr(overrides map[string]string, segmentID, embedded string) string {
	if overrides != nil {
		if v, ok := overrides[segmentID]; ok && v != "" {
			return v
		}
	}
	return embedded
}

// stateString reads a string value from session state; absent / wrong-typed
// keys return "".
func stateString(state StateReader, key string) string {
	raw, err := state.Get(key)
	if err != nil {
		return ""
	}
	s, _ := raw.(string)
	return s
}

// ComposeInstruction emits the deterministic 6-block CREATE prompt for
// the specified role + task context. Pure function (IMDA D2 transparency).
func ComposeInstruction(role AgentRole, ctx TaskContext) string {
	switch role {
	case RoleModerator:
		return composeModerator(ctx)
	case RoleCritic:
		return composeCritic(ctx)
	default:
		return composeModerator(ctx) // safe default
	}
}

func composeModerator(ctx TaskContext) string {
	var b strings.Builder

	b.WriteString("## [CONTEXT]\n")
	b.WriteString("You are running inside the Chora learning platform as the Moderator " +
		"sub-agent of the Content Moderation crew (P6 Reflection). You gate user-generated " +
		"posts on the C+ ChoraCircle feed before they reach other learners.\n")
	fmt.Fprintf(&b, "Tenant: %s. Author GCID: %s.\n\n",
		safe(ctx.TenantID, "<unset>"), safe(ctx.AuthorGCID, "<unset>"))

	// [ROLE] — ADR-197 M-B: SAFE block, operator-overridable.
	b.WriteString("## [ROLE]\n")
	b.WriteString(overrideOr(ctx.Overrides, "role",
		"You are the Moderator — first-pass moderation verdict. A second agent "+
			"(the Critic) will review your verdict; do NOT try to anticipate the Critic, just "+
			"give your honest first-pass call. You are NOT generating content; you are gating."))
	b.WriteString("\n\n")

	// [EXAMPLES] — ADR-197 M-B: SAFE block, operator-overridable (the embedded
	// body is assembled into exBuf so the no-override path stays byte-identical).
	b.WriteString("## [EXAMPLES]\n")
	var exBuf strings.Builder
	exBuf.WriteString("Example (pass): input=\"Just earned my CSPO! Eira helped me through the " +
		"product-backlog refinement atoms.\" → {\"verdict\":\"pass\",\"reason\":\"benign " +
		"learning-achievement post; mentions Familiar by name (acceptable Chora artefact).\"}\n")
	exBuf.WriteString("Example (refine): input=\"u guys r the worst, this platform sucks\" → " +
		"{\"verdict\":\"refine\",\"reason\":\"profanity-adjacent + non-constructive; suggest " +
		"rephrasing toward specific feedback.\"}\n")
	exBuf.WriteString("Example (reject): input=\"DM me to buy CSPO exam answers, contact +65...\" " +
		"→ {\"verdict\":\"reject\",\"reason\":\"solicitation + PII (phone number) + academic " +
		"integrity violation.\"}\n\n")
	b.WriteString(overrideOr(ctx.Overrides, "examples", exBuf.String()))

	b.WriteString("## [AUDIENCE]\n")
	b.WriteString("Your verdict is consumed by the Critic sub-agent (the next step in the " +
		"reflection pipeline). The final consumer is the chora-sharing service which " +
		"persists the verdict to ContentPolicyViolationLog.\n\n")

	b.WriteString("## [TASK]\n")
	// Post-text content presentation — NEVER overridable (the user-generated post
	// being moderated must always reach the model verbatim).
	fmt.Fprintf(&b, "Moderate this post against Chora policy:\n```\n%s\n```\n",
		safe(ctx.PostText, "<no post text supplied>"))
	// Verdict-path task instructions — ADR-197 M-B: SAFE block, operator-overridable.
	var taskBuf strings.Builder
	taskBuf.WriteString("- Issue ONE of: pass | refine | reject.\n")
	taskBuf.WriteString("- pass    = post is acceptable as-is.\n")
	taskBuf.WriteString("- refine  = post needs author edits before it can land.\n")
	taskBuf.WriteString("- reject  = post violates policy; will not land.\n")
	b.WriteString(overrideOr(ctx.Overrides, "task", taskBuf.String()))
	// Safety policy-category list — NEVER overridable.
	b.WriteString("- Consider: profanity, harassment, PII leakage (emails/phone/GCIDs), " +
		"solicitation, academic integrity (exam-answer trading), advertising, hate speech.\n\n")

	b.WriteString("## [EXPECTED OUTPUT]\n")
	b.WriteString("JSON: {\"verdict\": \"pass\"|\"refine\"|\"reject\", \"reason\": string}. " +
		"NEVER write commentary outside the JSON.\n")

	return b.String()
}

func composeCritic(ctx TaskContext) string {
	var b strings.Builder

	b.WriteString("## [CONTEXT]\n")
	b.WriteString("You are running inside the Chora learning platform as the Critic " +
		"sub-agent of the Content Moderation crew (P6 Reflection). You review the " +
		"Moderator's first-pass verdict and either confirm it or override with a " +
		"justified final verdict. You are LLM-as-judge.\n")
	fmt.Fprintf(&b, "Tenant: %s. Author GCID: %s.\n\n",
		safe(ctx.TenantID, "<unset>"), safe(ctx.AuthorGCID, "<unset>"))

	// [ROLE] — ADR-197 M-B: SAFE block, operator-overridable.
	b.WriteString("## [ROLE]\n")
	b.WriteString(overrideOr(ctx.Overrides, "role",
		"You are the Critic — the second pass that emits the FINAL verdict the "+
			"chora-sharing service will act on. The Moderator made a first call; your job is "+
			"to reflect on whether it's right. You can confirm (agree) or override (disagree "+
			"with justification)."))
	b.WriteString("\n\n")

	// [EXAMPLES] — ADR-197 M-B: SAFE block, operator-overridable (embedded body
	// assembled into exBuf so the no-override path stays byte-identical).
	b.WriteString("## [EXAMPLES]\n")
	var exBuf strings.Builder
	exBuf.WriteString("Example (confirm): Moderator said pass for \"Earned my CSPO!\" → " +
		"{\"final_verdict\":\"pass\",\"agreement\":\"confirm\",\"reason\":\"Moderator's " +
		"pass verdict is correct; no policy concerns.\"}\n")
	exBuf.WriteString("Example (override pass→refine): Moderator said pass for \"Earned my CSPO! " +
		"DM @phyllis for tutoring.\" → {\"final_verdict\":\"refine\",\"agreement\":\"override\"," +
		"\"reason\":\"Moderator missed the off-platform solicitation (DM @ handle). Refine " +
		"to remove the contact CTA.\"}\n")
	exBuf.WriteString("Example (override reject→refine): Moderator rejected \"Anyone else find " +
		"the rubric confusing?\" → {\"final_verdict\":\"refine\",\"agreement\":\"override\"," +
		"\"reason\":\"Moderator over-flagged a legitimate constructive question. Refine " +
		"the wording for clarity, but do not reject.\"}\n\n")
	b.WriteString(overrideOr(ctx.Overrides, "examples", exBuf.String()))

	b.WriteString("## [AUDIENCE]\n")
	b.WriteString("Your verdict IS the final answer. The chora-sharing service persists it " +
		"to ContentPolicyViolationLog and (if pass) publishes the post to the C+ feed.\n\n")

	b.WriteString("## [TASK]\n")
	// Post-text content presentation — NEVER overridable.
	fmt.Fprintf(&b, "Review the Moderator's verdict for this post:\n```\n%s\n```\n",
		safe(ctx.PostText, "<no post text supplied>"))
	// Critic task instructions — ADR-197 M-B: SAFE block, operator-overridable.
	var taskBuf strings.Builder
	taskBuf.WriteString("- The Moderator's verdict is in the conversation history above.\n")
	taskBuf.WriteString("- Decide: confirm (agree with Moderator) or override (give a different " +
		"final_verdict + reason).\n")
	taskBuf.WriteString("- Apply LLM-as-judge rigour: would the same input land the same verdict " +
		"on a different day? Consistency matters.\n")
	taskBuf.WriteString("- final_verdict must be one of: pass | refine | reject.\n")
	b.WriteString(overrideOr(ctx.Overrides, "task", taskBuf.String()))
	b.WriteString("\n")

	b.WriteString("## [EXPECTED OUTPUT]\n")
	b.WriteString("JSON: {\"final_verdict\": \"pass\"|\"refine\"|\"reject\", " +
		"\"agreement\": \"confirm\"|\"override\", \"reason\": string}. " +
		"NEVER write commentary outside the JSON.\n")

	return b.String()
}

func safe(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
