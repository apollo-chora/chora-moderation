package main

// Interim W3 stamp (ADR-254 D7, coordinator ruling 2026-08-22 17:57Z): every
// gateway client of this web-mode binary names its crew as the Invoke
// surface; the D7 gateway refuses an unstamped call. The tests pin the stamp
// per client so a future edit cannot drop it silently.

import (
	"testing"

	"github.com/apollo-chora/chora-moderation/internal/agentconfig"
)

func Test_moderatorGatewayConfig_stampsTheSurface(t *testing.T) {
	cfg := moderatorGatewayConfig("crew-x", "gw:443", "gcid-1", "tenant-1", agentconfig.SubAgentConfig{FallbackModels: []string{"fb"}}, "gemini-2.5-flash", "https://gateway.test.invalid", false)
	if cfg.Surface != crewSurface || crewSurface != "content_moderation" {
		t.Fatalf("surface = %q, want the ADR-254 D7 crew id content_moderation", cfg.Surface)
	}
	if cfg.AgentID != "moderator" || cfg.Endpoint != "gw:443" || cfg.TenantID != "tenant-1" || cfg.GCID != "gcid-1" || cfg.CrewKind != "crew-x" || cfg.LogicalModelID != "gemini-2.5-flash" || len(cfg.FallbackModelIDs) != 1 || cfg.FallbackModelIDs[0] != "fb" {
		t.Fatalf("identity changed: %+v", cfg)
	}
	if cfg.ActionCode != "content_moderation" {
		t.Fatalf("action code = %q, want %q", cfg.ActionCode, "content_moderation")
	}
}

func Test_criticGatewayConfig_stampsTheSurface(t *testing.T) {
	cfg := criticGatewayConfig("crew-x", agentconfig.SubAgentConfig{FallbackModels: []string{"fb"}}, "gemini-2.5-flash", "gw:443", "gcid-1", "tenant-1", "https://gateway.test.invalid", false)
	if cfg.Surface != crewSurface || crewSurface != "content_moderation" {
		t.Fatalf("surface = %q, want the ADR-254 D7 crew id content_moderation", cfg.Surface)
	}
	if cfg.AgentID != "critic" || cfg.Endpoint != "gw:443" || cfg.TenantID != "tenant-1" || cfg.GCID != "gcid-1" || cfg.CrewKind != "crew-x" || cfg.LogicalModelID != "gemini-2.5-flash" || len(cfg.FallbackModelIDs) != 1 || cfg.FallbackModelIDs[0] != "fb" {
		t.Fatalf("identity changed: %+v", cfg)
	}
	if cfg.ActionCode != "" {
		t.Fatalf("action code = %q, want %q", cfg.ActionCode, "")
	}
}
