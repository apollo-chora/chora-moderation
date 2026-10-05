// instruction_provider.go — per-turn InstructionProvider wiring for the
// Moderation crew (ADR-169 web-mode migration; the deferred "Iter 4.5" runtime
// instruction composition).
//
// The Moderator + Critic composers interpolate the post text + identity from a
// TaskContext. At boot those values are unknown, so the agents must recompose
// their instruction PER TURN from the session state the chora-sharing caller
// populated at async_create_session (post_text / tenant_id / author_gcid).
// llmagent.Config.InstructionProvider takes precedence over the static
// Instruction field, so wiring this fixes the regression where the boot-time
// "<filled at runtime>" placeholder reached the model and every post was
// rejected as empty content.
package agent

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
)

// NewInstructionProvider returns an llmagent.InstructionProvider that composes
// the role's instruction from the live session state on each turn. Wire it into
// llmagent.Config.InstructionProvider for both the Moderator and the Critic.
func NewInstructionProvider(role AgentRole) llmagent.InstructionProvider {
	return func(rc agent.ReadonlyContext) (string, error) {
		var tc TaskContext
		if rc != nil {
			if st := rc.ReadonlyState(); st != nil {
				tc = TaskContextFromState(st)
			}
		}
		return ComposeInstruction(role, tc), nil
	}
}
