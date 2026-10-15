package main

// Interim W3 stamp (ADR-254 D7, coordinator ruling 2026-08-22 17:57Z): every
// gateway client of this web-mode binary names its crew as the Invoke
// surface; the D7 gateway refuses an unstamped call. The tests pin the stamp
// per client so a future edit cannot drop it silently.
//
// The model ids are env-only: resolveModels reads MODERATION_MODERATOR_MODEL /
// MODERATION_CRITIC_MODEL (both REQUIRED) and the optional *_FALLBACK_MODELS
// chains. The tests below assert an unset var is a boot error and that the
// env-provided values are what reach the gateway client identity.

import (
	"testing"
)

func Test_resolveModels_requiresBothModelVars(t *testing.T) {
	t.Setenv(envModeratorModel, "")
	t.Setenv(envCriticModel, "")
	if _, _, err := resolveModels(); err == nil {
		t.Fatal("want an error when MODERATION_MODERATOR_MODEL / MODERATION_CRITIC_MODEL are unset")
	}
}

func Test_resolveModels_requiresCriticModel(t *testing.T) {
	t.Setenv(envModeratorModel, "env-moderator")
	t.Setenv(envCriticModel, "")
	if _, _, err := resolveModels(); err == nil {
		t.Fatal("want an error when MODERATION_CRITIC_MODEL is unset")
	}
}

func Test_resolveModels_envOnly(t *testing.T) {
	t.Setenv(envModeratorModel, "env-moderator")
	t.Setenv(envModeratorFallback, "env-moderator-fb-a, env-moderator-fb-b")
	t.Setenv(envCriticModel, "env-critic")
	t.Setenv(envCriticFallback, "")

	moderator, critic, err := resolveModels()
	if err != nil {
		t.Fatalf("resolveModels: %v", err)
	}
	if moderator.model != "env-moderator" || critic.model != "env-critic" {
		t.Fatalf("models = %q / %q, want the env-provided values", moderator.model, critic.model)
	}
	if len(moderator.fallback) != 2 || moderator.fallback[0] != "env-moderator-fb-a" || moderator.fallback[1] != "env-moderator-fb-b" {
		t.Fatalf("moderator fallback = %v, want the env-provided chain", moderator.fallback)
	}
	if critic.fallback != nil {
		t.Fatalf("critic fallback = %v, want no chain when the env var is blank", critic.fallback)
	}
}

func Test_moderatorGatewayConfig_stampsTheSurface(t *testing.T) {
	t.Setenv(envModeratorModel, "env-moderator")
	t.Setenv(envModeratorFallback, "env-moderator-fb")
	moderator, err := resolveSubAgentModel(envModeratorModel, envModeratorFallback)
	if err != nil {
		t.Fatalf("resolveSubAgentModel: %v", err)
	}

	cfg := moderatorGatewayConfig("crew-x", "gw:443", "gcid-1", "tenant-1", moderator.fallback, moderator.model, "https://gateway.test.invalid", false)
	if cfg.Surface != crewSurface || crewSurface != "content_moderation" {
		t.Fatalf("surface = %q, want the ADR-254 D7 crew id content_moderation", cfg.Surface)
	}
	if cfg.AgentID != "moderator" || cfg.Endpoint != "gw:443" || cfg.TenantID != "tenant-1" || cfg.GCID != "gcid-1" || cfg.CrewKind != "crew-x" || cfg.LogicalModelID != "env-moderator" || len(cfg.FallbackModelIDs) != 1 || cfg.FallbackModelIDs[0] != "env-moderator-fb" {
		t.Fatalf("identity changed: %+v", cfg)
	}
	if cfg.ActionCode != "content_moderation" {
		t.Fatalf("action code = %q, want %q", cfg.ActionCode, "content_moderation")
	}
}

func Test_criticGatewayConfig_stampsTheSurface(t *testing.T) {
	t.Setenv(envCriticModel, "env-critic")
	t.Setenv(envCriticFallback, "env-critic-fb")
	critic, err := resolveSubAgentModel(envCriticModel, envCriticFallback)
	if err != nil {
		t.Fatalf("resolveSubAgentModel: %v", err)
	}

	cfg := criticGatewayConfig("crew-x", critic.fallback, critic.model, "gw:443", "gcid-1", "tenant-1", "https://gateway.test.invalid", false)
	if cfg.Surface != crewSurface || crewSurface != "content_moderation" {
		t.Fatalf("surface = %q, want the ADR-254 D7 crew id content_moderation", cfg.Surface)
	}
	if cfg.AgentID != "critic" || cfg.Endpoint != "gw:443" || cfg.TenantID != "tenant-1" || cfg.GCID != "gcid-1" || cfg.CrewKind != "crew-x" || cfg.LogicalModelID != "env-critic" || len(cfg.FallbackModelIDs) != 1 || cfg.FallbackModelIDs[0] != "env-critic-fb" {
		t.Fatalf("identity changed: %+v", cfg)
	}
	if cfg.ActionCode != "" {
		t.Fatalf("action code = %q, want %q", cfg.ActionCode, "")
	}
}
