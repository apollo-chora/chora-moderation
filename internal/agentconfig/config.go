// Package agentconfig holds the build-time, per-agent tier + prompt
// configuration for the content_moderation crew (AGENT-DRIVEN tiering,
// CR mana-is-quota-not-model-selector 2026-06-01).
//
// Mirrors the qgen agentconfig pattern. Each agent owns its OWN embedded YAML
// (separation of concern) declaring, per sub-agent: tier / prompt_version. The
// model itself is env-only. The boot layer reads each sub-agent's REQUIRED
// model env var and:
//   - constructs ONE model-gateway client per sub-agent from it (this crew
//     routes through chora-model-gateway per ADR-177 — the gateway resolves
//     the logical model id, so model selection stays fully agent-side here),
//   - carries the optional *_FALLBACK_MODELS comma list as the agent-DECLARED
//     fallback chain, forwarded to the gateway per sub-agent (mirroring qgen),
//   - selects the prompt_version'd template.
//
// No model name is declared in this package; a blank prompt_version is a
// config error, never a silent default.
//
// Model selection is AGENT-DRIVEN — mana is a token-budget QUOTA system
// (manaplugin gate), NOT a model selector. The old tieredmodelplugin is no
// longer registered (see feedback_mana_is_quota_not_model_selector).
package agentconfig

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed moderation.yaml
var moderationYAML []byte

// SubAgentConfig is one sub-agent's resolved tier + prompt selection.
type SubAgentConfig struct {
	Tier          string `yaml:"tier"`
	PromptVersion string `yaml:"prompt_version"`
}

// AgentConfig is one agent binary's full per-sub-agent config.
type AgentConfig struct {
	Agent     string                    `yaml:"agent"`
	SubAgents map[string]SubAgentConfig `yaml:"sub_agents"`
}

// Sub returns the named sub-agent config, failing loud if the YAML omits it or
// leaves its prompt_version blank — a missing sub-agent or an unversioned
// prompt is a build/config error, never a silent default (per
// feedback_no_stubs_real_wiring). The model id is not declared here: boot reads
// it from the sub-agent's required env var.
func (c AgentConfig) Sub(name string) (SubAgentConfig, error) {
	sc, ok := c.SubAgents[name]
	if !ok {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q has no sub-agent %q", c.Agent, name)
	}
	if sc.PromptVersion == "" {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q sub-agent %q has empty prompt_version", c.Agent, name)
	}
	return sc, nil
}

// Moderation parses the embedded content_moderation config.
func Moderation() (AgentConfig, error) { return parse(moderationYAML) }

func parse(raw []byte) (AgentConfig, error) {
	var c AgentConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return AgentConfig{}, fmt.Errorf("agentconfig: unmarshal: %w", err)
	}
	if c.Agent == "" {
		return AgentConfig{}, fmt.Errorf("agentconfig: missing top-level agent name")
	}
	if len(c.SubAgents) == 0 {
		return AgentConfig{}, fmt.Errorf("agentconfig: agent %q declares no sub_agents", c.Agent)
	}
	return c, nil
}
