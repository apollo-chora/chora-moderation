package agentconfig_test

import (
	"testing"

	"github.com/apollo-chora/chora-moderation/internal/agentconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModeration_TierLadders(t *testing.T) {
	cfg, err := agentconfig.Moderation()
	require.NoError(t, err)
	assert.Equal(t, "content_moderation", cfg.Agent)

	// The model ids are env-only (MODERATION_MODERATOR_MODEL /
	// MODERATION_CRITIC_MODEL), so the YAML carries no model name to assert —
	// the Tier assertions below carry the discrimination between the entries.

	// CHEAP tier — moderator (first-pass classification: pass|refine|reject).
	moderator, err := cfg.Sub("moderator")
	require.NoError(t, err)
	assert.Equal(t, "cheap", moderator.Tier)
	assert.Equal(t, "v1", moderator.PromptVersion)

	// HIGH tier — critic (LLM-as-judge reflection; emits final verdict).
	critic, err := cfg.Sub("critic")
	require.NoError(t, err)
	assert.Equal(t, "high", critic.Tier)
	assert.Equal(t, "v1", critic.PromptVersion)
}

func TestSub_MissingSubAgentFailsLoud(t *testing.T) {
	cfg, err := agentconfig.Moderation()
	require.NoError(t, err)
	_, err = cfg.Sub("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sub-agent")
}
