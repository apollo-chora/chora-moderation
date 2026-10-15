// Command moderation is the entry point for the Content Moderation crew
// (Pattern P6 Reflection: Moderator + Critic) per crew-composition SKILL
// §1 + §2.
//
// Phyllis Step 9 (ChoraCircle CSPO completion post) — gates user-generated
// posts before they hit the C+ feed.
//
// 2-agent reflection pipeline wired via sequentialagent.New(moderator, critic):
//   - Moderator (T2 Flash Preview) — first-pass verdict
//   - Critic    (T1 Pro Preview, LLM-as-judge) — reflects + emits final verdict
//
// The Critic's output is the final answer surfaced to the caller. Full
// loopagent iteration with author-edit refinement is Iter 4.5 territory.
//
// Per ADR-138 §1 Go-first + ADR-145 polyglot agent-runtime pivot + ADR-146
// Model Broker full retirement + ADR-148 region pivot.
//
// Deploy: any container runtime — the binary serves the ADK web-mode HTTP
// API on :8080 (see Dockerfile). (The old hosted-runtime sandbox deploy
// script was deleted 2026-09-03.)
// Callers MUST create the session via `:query` endpoint with
// `class_method: async_create_session` and pass:
//
//	state: {
//	  tenant_id:    "<tenant-uuid>",   // required by manaplugin
//	  user_gcid:    "<author-gcid>",   // required by manaplugin
//	  author_gcid:  "<author-gcid>",   // canonical key for moderation context
//	  post_text:    "<post body>",     // the content to moderate
//	  mana_tier:    "basic|standard|premium",
//	}
//
// Env vars (NEVER inlined per feedback_no_inline_config):
//
//	MODERATION_PORT               — HTTP port for the crew API (default 8080)
//	MODERATION_MODERATOR_MODEL    — moderator gateway logical_model_id (REQUIRED)
//	MODERATION_CRITIC_MODEL       — critic gateway logical_model_id (REQUIRED)
//	MODERATION_MODERATOR_FALLBACK_MODELS — optional comma-separated fallback chain (empty = none)
//	MODERATION_CRITIC_FALLBACK_MODELS    — optional comma-separated fallback chain (empty = none)
//	TENANCY_GRPC_ENDPOINT         — stub:// for POC; gRPC URL in prod
//	SHARING_GRPC_ENDPOINT         — stub:// for POC; gRPC URL in prod (future ContentPolicyViolationLog publish)
//	CHORA_ENV                     — dev | staging | prod
//
// AGENT-DRIVEN tiering (CR mana-is-quota-not-model-selector 2026-06-01): the
// per-sub-agent tier + prompt version is config-declared in the embedded
// agentconfig YAML, mirroring qgen. moderator = CHEAP (first-pass
// classification), critic = HIGH (LLM-as-judge reflection). The model ids are
// env-only and REQUIRED — boot fails loud when one is unset rather than
// routing to a literal name. Mana is a token-budget QUOTA system (manaplugin
// gate) — it does NOT select the model; the tieredmodelplugin is no longer
// registered.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/promptstamping"
	"github.com/apollo-chora/chora-adk-common/terminationplugin"
	"github.com/apollo-chora/chora-adk-common/tracing"

	modagent "github.com/apollo-chora/chora-moderation/internal/agent"
	"github.com/apollo-chora/chora-moderation/internal/agentconfig"
	"github.com/apollo-chora/chora-moderation/internal/agentserver"
)

const crewKind = "moderation"

// envOr returns os.Getenv(name) if non-empty, else fallback.
// Lesson learned from QGen Iter 2: adkgo plumbs only a handful of env
// vars at CreateReasoningEngine; Chora-specific vars are PATCHed in
// AFTER the initial start. Bootstrap must survive that window.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// envEnabled reports whether the env var is set to 1/true (case-insensitive),
// mirroring the agentdispatch.Enabled convention.
func envEnabled(name string) bool {
	v := strings.TrimSpace(os.Getenv(name))
	return v == "1" || strings.EqualFold(v, "true")
}

// Model env names (names only; values come from the container env per
// feedback_no_inline_config). Every *_MODEL var is REQUIRED — boot fails loud
// when one is unset rather than falling back to a literal model name. The
// *_FALLBACK_MODELS vars are optional comma-separated chains: unset (the
// default) declares no fallback.
const (
	envModeratorModel    = "MODERATION_MODERATOR_MODEL"
	envModeratorFallback = "MODERATION_MODERATOR_FALLBACK_MODELS"
	envCriticModel       = "MODERATION_CRITIC_MODEL"
	envCriticFallback    = "MODERATION_CRITIC_FALLBACK_MODELS"
)

// requiredModel returns the env-provided gateway logical_model_id, failing
// loud when the var is unset. Model ids are env-only: there is no literal
// default to fall back to (feedback_no_inline_config).
func requiredModel(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("%s required (the gateway logical_model_id; env-only, no default)", name)
	}
	return v, nil
}

// fallbackModels parses a comma-separated env list into the declared fallback
// chain. Unset / blank (the default) declares no chain, so the gateway is
// asked for the primary target only.
func fallbackModels(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// subAgentModel is one sub-agent's env-resolved gateway identity: the model id
// (the gateway logical_model_id) and the declared fallback chain.
type subAgentModel struct {
	model    string
	fallback []string
}

// resolveSubAgentModel resolves one sub-agent's REQUIRED model env var and its
// optional comma-separated fallback chain. The model var is env-only, so a
// deploy that forgets it dies at boot rather than routing to a literal name.
func resolveSubAgentModel(modelEnv, fallbackEnv string) (subAgentModel, error) {
	m, err := requiredModel(modelEnv)
	if err != nil {
		return subAgentModel{}, err
	}
	return subAgentModel{model: m, fallback: fallbackModels(fallbackEnv)}, nil
}

// resolveModels resolves both sub-agents' env-only model ids and declared
// fallback chains. MODERATION_MODERATOR_MODEL and MODERATION_CRITIC_MODEL are
// both REQUIRED.
func resolveModels() (moderator subAgentModel, critic subAgentModel, err error) {
	moderator, err = resolveSubAgentModel(envModeratorModel, envModeratorFallback)
	if err != nil {
		return subAgentModel{}, subAgentModel{}, err
	}
	critic, err = resolveSubAgentModel(envCriticModel, envCriticFallback)
	if err != nil {
		return subAgentModel{}, subAgentModel{}, err
	}
	return moderator, critic, nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx := context.Background()

	// Wire the OTel exporter + W3C TraceContext propagator BEFORE any
	// agent / runner / plugin construction so every span flows to the
	// trace backend and continues an inbound traceparent. ADK Go does
	// NOT auto-wire this; per the trace wave 2026-05-29 every ADK agent
	// calls tracing.Init so it is traceable at per-agent level
	// (service.name = the registry name).
	traceShutdown, err := tracing.Init(ctx, "content_moderation")
	if err != nil {
		log.Fatalf("tracing.Init: %v", err)
	}
	defer func() {
		if err := traceShutdown(context.Background()); err != nil {
			slog.Error("trace shutdown error", "err", err)
		}
	}()

	// Session app_name for the ADK web-mode session service. The agentengine
	// launcher uses its constructor arg verbatim as the session AppName — on
	// the hosted agent runtime that arg was the reasoning-engine resource,
	// but after the hosted runtime was decommissioned (ADR-169, web-mode)
	// there is no engine ID, so an EMPTY arg makes
	// session.InMemoryService().Create reject EVERY session with
	// "app_name and user_id are required, got app_name: \"\"". We therefore
	// pass a stable, non-empty app_name DECOUPLED from any engine ID.
	// Mirrors the proven chora-familiar web-mode fix
	// (FAMILIAR_SESSION_APP_NAME).
	sessionAppName := envOr("MODERATION_SESSION_APP_NAME", "chora-moderation")

	// HTTP port for the crew's agent-engine-style API (default 8080, the
	// port the web-mode launcher served on).
	port := 8080
	if v := strings.TrimSpace(os.Getenv("MODERATION_PORT")); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("moderation: invalid MODERATION_PORT %q: %v", v, err)
		}
		port = p
	}

	// AGENT-DRIVEN tiering (CR mana-is-quota-not-model-selector 2026-06-01):
	// per-sub-agent tier + prompt version are config-declared in the embedded
	// agentconfig YAML, mirroring qgen. moderator = CHEAP, critic = HIGH. The
	// model ids are env-only and REQUIRED (MODERATION_MODERATOR_MODEL /
	// MODERATION_CRITIC_MODEL) — no literal fallback — and the optional
	// *_FALLBACK_MODELS chains are forwarded to the gateway. This crew routes
	// both sub-agent models through chora-model-gateway (ADR-177); model
	// selection stays AGENT-DRIVEN via the env-provided primaries + forwarded
	// fallback chains.
	modCfg, err := agentconfig.Moderation()
	if err != nil {
		log.Fatalf("moderation: load agent config: %v", err)
	}
	moderatorCfg, err := modCfg.Sub("moderator")
	if err != nil {
		log.Fatalf("moderation: %v", err)
	}
	criticCfg, err := modCfg.Sub("critic")
	if err != nil {
		log.Fatalf("moderation: %v", err)
	}
	moderatorModel, criticModel, err := resolveModels()
	if err != nil {
		log.Fatalf("moderation: %v", err)
	}

	tenancyEndpoint := envOr("TENANCY_GRPC_ENDPOINT", "stub://chora-tenancy")
	sharingEndpoint := envOr("SHARING_GRPC_ENDPOINT", "stub://chora-sharing")
	choraEnv := envOr("CHORA_ENV", "dev")

	if v := os.Getenv("TENANCY_GRPC_ENDPOINT"); v == "" {
		_ = os.Setenv("TENANCY_GRPC_ENDPOINT", tenancyEndpoint)
	}
	if v := os.Getenv("SHARING_GRPC_ENDPOINT"); v == "" {
		_ = os.Setenv("SHARING_GRPC_ENDPOINT", sharingEndpoint)
	}

	slog.Info("moderation boot",
		"session_app_name", sessionAppName,
		"moderator_model", moderatorModel.model,
		"moderator_tier", moderatorCfg.Tier,
		"moderator_fallback", moderatorModel.fallback,
		"critic_model", criticModel.model,
		"critic_tier", criticCfg.Tier,
		"critic_fallback", criticModel.fallback,
		"tenancy_endpoint", tenancyEndpoint,
		"sharing_endpoint", sharingEndpoint,
		"chora_env", choraEnv,
	)

	// Per-sub-agent models — route through chora-model-gateway (ADR-177 full
	// mana umbrella). Both LLM turns flow through the one chokepoint
	// (central safety policy, per-tenant budget, token-usage ledger).
	// Model selection stays AGENT-DRIVEN (moderator=CHEAP, critic=HIGH) via
	// the env-provided model ids + forwarded fallback chains. Per-request
	// tenant/gcid come from session state via the propagation plugin; env
	// values are the process fallback.
	//
	// METERING (ADR-177 §FU-3): a moderation request is ONE billable unit even
	// though it runs two sub-agents. Only the moderator (first) carries an
	// action_code; the critic carries none ⇒ the gateway debits at most once.
	// content_moderation is a SAFETY screen — until a chora_identity catalogue
	// row prices it, the gateway logs unpriced→un-metered (free safety call).
	gatewayEndpoint := envOr("CHORA_GATEWAY_ENDPOINT", "gateway.chora.site:443")
	// D6 step 1: the ID-token audience is read HERE and defaulted explicitly.
	// modelgatewayclient still defaults it internally in TWO places
	// (client.go:181-182 and image.go:110-111); passing it makes the value
	// stateable and is what lets step 4 remove those defaults safely.
	gatewayAudience := envOr("CHORA_GATEWAY_AUDIENCE", "https://gateway.chora.site")
	// Local-dev plaintext gRPC to the gateway (no TLS, no token). Production
	// MUST leave this unset and use CHORA_GATEWAY_TOKEN instead.
	gatewayInsecure := envEnabled("CHORA_GATEWAY_INSECURE")
	gatewayTenantID := os.Getenv("CHORA_GATEWAY_TENANT_ID")
	gatewayGCID := os.Getenv("CHORA_GATEWAY_GCID")
	if gatewayTenantID == "" || gatewayGCID == "" {
		log.Fatalf("moderation: CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required " +
			"(process fallback; per-request values come from session state — no silent " +
			"mis-attribution per ADR-169 + feedback_no_stubs_real_wiring)")
	}
	moderatorGemini, err := modelgatewayclient.New(ctx, moderatorGatewayConfig(crewKind, gatewayEndpoint, gatewayGCID, gatewayTenantID, moderatorModel.fallback, moderatorModel.model, gatewayAudience, gatewayInsecure))
	if err != nil {
		log.Fatalf("moderation: modelgatewayclient.New(moderator, %s): %v", moderatorModel.model, err)
	}
	criticGemini, err := modelgatewayclient.New(ctx, criticGatewayConfig(crewKind, criticModel.fallback, criticModel.model, gatewayEndpoint, gatewayGCID, gatewayTenantID, gatewayAudience, gatewayInsecure))
	if err != nil {
		log.Fatalf("moderation: modelgatewayclient.New(critic, %s): %v", criticModel.model, err)
	}

	// Per-request tenant propagation (ADR-169) — stamp tenant_id/user_gcid from
	// session state onto every gateway Invoke. No ActionCodeResolver: the
	// action codes are static per sub-agent (set above).
	tenantPropP, err := modelgatewayclient.NewTenantPropagationPlugin(crewKind)
	if err != nil {
		log.Fatalf("moderation: modelgatewayclient.NewTenantPropagationPlugin: %v", err)
	}

	// Per-turn instruction composition (ADR-169 web-mode migration — the
	// deferred "Iter 4.5" wiring). The post text + identity are unknown at
	// boot, so each sub-agent recomposes its instruction from the session
	// state the chora-sharing caller populates at async_create_session
	// (post_text / tenant_id / author_gcid). InstructionProvider takes
	// precedence over the static Instruction field; wiring it fixes the
	// regression where a "<filled at runtime>" placeholder reached the model
	// and every post was rejected as empty content.
	moderator, err := llmagent.New(llmagent.Config{
		Name:        "moderator",
		Model:       moderatorGemini,
		Description: "First-pass moderation verdict on user-generated ChoraCircle posts (CHEAP tier; first-pass classification).",
		// ADR-197 M-A: stamp prompt_version + content_hash + {role} on the span.
		InstructionProvider: promptstamping.WithStamping(
			moderatorCfg.PromptVersion,
			modagent.ModerationConditions(modagent.RoleModerator),
			modagent.NewInstructionProvider(modagent.RoleModerator),
		),
	})
	if err != nil {
		log.Fatalf("llmagent.New(moderator): %v", err)
	}

	critic, err := llmagent.New(llmagent.Config{
		Name:        "critic",
		Model:       criticGemini,
		Description: "Reflection sub-agent — reviews Moderator's verdict and emits the FINAL verdict (HIGH tier; LLM-as-judge).",
		// ADR-197 M-A: stamp prompt_version + content_hash + {role} on the span.
		InstructionProvider: promptstamping.WithStamping(
			criticCfg.PromptVersion,
			modagent.ModerationConditions(modagent.RoleCritic),
			modagent.NewInstructionProvider(modagent.RoleCritic),
		),
	})
	if err != nil {
		log.Fatalf("llmagent.New(critic): %v", err)
	}

	// P6 Reflection wired as a 2-step sequential pass. Moderator → Critic.
	// Critic's verdict is the final answer. Full loopagent iteration with
	// author-edit refinement is Iter 4.5 territory.
	pipeline, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: adkagent.Config{
			Name:        "content_moderation_pipeline",
			Description: "Content Moderation crew — P6 Reflection (Moderator + Critic). Phyllis Step 9 ChoraCircle post gate.",
			SubAgents: []adkagent.Agent{
				moderator,
				critic,
			},
		},
	})
	if err != nil {
		log.Fatalf("sequentialagent.New: %v", err)
	}

	loader := adkagent.NewSingleLoader(pipeline)

	// Mana gate + metering is now the gateway's job (ADR-177): the moderator's
	// action_code drives at most one debit per moderation request. The
	// agent-side manaplugin debit is retired; tenant/gcid attribution is
	// handled by tenantPropP (built above).

	// NOTE: the tieredmodelplugin (ADR-149 mana × growth LLM matrix) is
	// DELIBERATELY NOT registered here (CR mana-is-quota-not-model-selector
	// 2026-06-01, user directive — mirrors qgen). Mana is a token-budget QUOTA
	// system (manaplugin gate, above) — it must NOT dictate which LLM model is
	// used. Model selection is AGENT-DRIVEN via the env-provided per-sub-agent
	// model ids (moderator=cheap / critic=high), each sub-agent getting its own
	// model-gateway client at boot. The old plugin's
	// 2.5-only matrix swapped the per-call model id from session.State().mana_tier,
	// clobbering the agent-declared tier. See
	// feedback_mana_is_quota_not_model_selector + the CR tracker.

	// MaxIterations = 6 — 2 sub-agents × 3 iterations each (per-agent
	// average) per the Familiar/QGen scaling pattern.
	terminationP, err := terminationplugin.New(terminationplugin.Config{
		Publisher:     &terminationplugin.LoggingPublisher{},
		AgentID:       "content_moderation_pipeline",
		Runtime:       "AGENT_EXECUTION_RUNTIME_ADK_GO",
		CrewKind:      crewKind,
		CrewPattern:   "P6_REFLECTION",
		MaxIterations: 6,
	})
	if err != nil {
		log.Fatalf("terminationplugin.New: %v", err)
	}

	// SessionService — in-memory (ADR-169, 2026-06-01). The crew serves
	// plain HTTP in the ADK launcher's `web` mode; an in-memory session
	// store removes any hosted-runtime session dependency (the prior cloud
	// session service made live API RPCs scoped to a reasoning-engine ID
	// that no longer exists). NOTE: in-memory sessions require replicas=1.
	sessionService := session.InMemoryService()

	// Serve the crew's HTTP API (agent-engine-style session + streaming
	// endpoints) until the process is shut down. AppName is the decoupled
	// session app name (see above) — every session.Create + StreamQuery
	// carries a non-empty app_name.
	if err := agentserver.Serve(ctx, agentserver.Config{
		Port:           port,
		AppName:        sessionAppName,
		RootAgent:      loader.RootAgent(),
		SessionService: sessionService,
		Plugins: runner.PluginConfig{
			Plugins: []*plugin.Plugin{tenantPropP, terminationP},
		},
	}); err != nil {
		log.Fatalf("agentserver.Serve: %v", err)
	}
}

// crewSurface is the ADR-254 D7 surface value of this crew.
const crewSurface = "content_moderation"

// moderatorGatewayConfig is the gateway client identity of the moderator call. Surface is
// the crew id (ADR-254 D7): the D7 gateway refuses an unstamped Invoke
// (FAILED_PRECONDITION surface_unstamped), so it is set here, once, and
// asserted by a test; everything else is what the call always sent. The model
// id and the declared fallback chain are env-provided (see resolveModels).
func moderatorGatewayConfig(crewKind string, gatewayEndpoint string, gatewayGCID string, gatewayTenantID string, moderatorFallback []string, moderatorModel string, gatewayAudience string, gatewayInsecure bool) modelgatewayclient.Config {
	return modelgatewayclient.Config{
		Endpoint:         gatewayEndpoint,
		LogicalModelID:   moderatorModel,
		FallbackModelIDs: moderatorFallback,
		AgentID:          "moderator",
		CrewKind:         crewKind,
		TenantID:         gatewayTenantID,
		GCID:             gatewayGCID,
		Audience:         gatewayAudience,
		Insecure:         gatewayInsecure,
		ActionCode:       "content_moderation", // metered once per request (moderator only)
		Surface:          crewSurface,
	}
}

// criticGatewayConfig is the gateway client identity of the critic call. Surface is
// the crew id (ADR-254 D7): the D7 gateway refuses an unstamped Invoke
// (FAILED_PRECONDITION surface_unstamped), so it is set here, once, and
// asserted by a test; everything else is what the call always sent. The model
// id and the declared fallback chain are env-provided (see resolveModels).
func criticGatewayConfig(crewKind string, criticFallback []string, criticModel string, gatewayEndpoint string, gatewayGCID string, gatewayTenantID string, gatewayAudience string, gatewayInsecure bool) modelgatewayclient.Config {
	return modelgatewayclient.Config{
		Endpoint:         gatewayEndpoint,
		LogicalModelID:   criticModel,
		FallbackModelIDs: criticFallback,
		AgentID:          "critic",
		CrewKind:         crewKind,
		TenantID:         gatewayTenantID,
		GCID:             gatewayGCID,
		Audience:         gatewayAudience,
		Insecure:         gatewayInsecure,
		// No ActionCode — the critic's call is part of the same moderation
		// request already billed by the moderator (one debit per request).
		Surface: crewSurface,
	}
}
