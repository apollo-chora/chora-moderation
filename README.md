# chora-moderation

The Chora **content-moderation agent crew**: a 2-agent ADK (Agent Development
Kit) Go crew that gates user-generated ChoraCircle posts before they reach the
C+ feed (Phyllis Step 9). It runs a **P6 Reflection** pair — a Moderator
(first-pass verdict) and a Critic (LLM-as-judge reflection that emits the
final verdict) — wired as a sequential pipeline.

> **Not to be confused with `chora-governance`.** That is the standalone
> governance *service* (policy rules, roles, permissions). This repository is
> the ADK content-moderation *agent crew*: an LLM pipeline that classifies a
> single post as `pass | refine | reject`. The two are separate deployables
> with separate repos.

Module path: `github.com/apollo-chora/chora-moderation`.

The crew is cloud-neutral: NATS is not required, no database is required, and
model calls flow through `chora-model-gateway` (gRPC) using the shared
`chora-adk-common` adapters. Traces go to standard OTLP via
`chora-common/otel`. No cloud account or managed service is required.

## How it works

1. The caller (chora-sharing) creates a session via the HTTP API with the
   post text and author identity in the session state.
2. The **Moderator** (CHEAP tier, default `gemini-3.5-flash`) issues a
   first-pass verdict: `pass`, `refine`, or `reject`.
3. The **Critic** (HIGH tier, default `gemini-3.1-pro-preview`) reviews the
   Moderator's verdict and emits the **final** verdict
   (`final_verdict` + `agreement`: confirm | override).
4. The Critic's verdict is the final answer surfaced to the caller.

Per-sub-agent model selection is **agent-driven**: the tier ladders are
declared in the embedded `internal/agentconfig/moderation.yaml` (single source
of truth) and may be overridden per-primary via env vars. Mana is a
token-budget quota system enforced by the model gateway — it does not select
models.

## HTTP API

The binary serves the agent-engine-style HTTP surface on port **8080**
(override with `MODERATION_PORT`):

| Endpoint | Purpose |
|---|---|
| `POST /api/reasoning_engine` | Session methods, dispatched by `class_method` |
| `POST /api/stream_reasoning_engine` | Streaming agent run (`async_stream_query`), SSE-style JSON lines |

`class_method` values on `/api/reasoning_engine`:

| `class_method` | Input | Output |
|---|---|---|
| `async_create_session` | `{user_id, state?}` | `{output: session}` |
| `async_get_session` | `{user_id, session_id}` | `{output: session}` |
| `async_list_sessions` | `{user_id}` | `{output: {sessions: [...]}}` |
| `async_delete_session` | `{user_id, session_id}` | `{output: ""}` |

Callers MUST create the session via `async_create_session` and pass:

```json
{
  "class_method": "async_create_session",
  "input": {
    "user_id": "<user-id>",
    "state": {
      "tenant_id": "<tenant-uuid>",
      "user_gcid": "<author-gcid>",
      "author_gcid": "<author-gcid>",
      "post_text": "<post body>",
      "mana_tier": "basic|standard|premium"
    }
  }
}
```

The per-turn instruction is recomposed from the session state on every run
(`internal/agent/instruction_provider.go`), so the real post text — never a
boot-time placeholder — reaches the model.

## Configuration

| Variable | Purpose | Local default |
| --- | --- | --- |
| `MODERATION_PORT` | HTTP port for the crew API | `8080` |
| `MODERATION_SESSION_APP_NAME` | ADK session AppName (session namespace) | `chora-moderation` |
| `MODERATION_MODERATOR_MODEL` | Override moderator primary model | `gemini-3.5-flash` (from agentconfig YAML) |
| `MODERATION_CRITIC_MODEL` | Override critic primary model | `gemini-3.1-pro-preview` (from agentconfig YAML) |
| `CHORA_GATEWAY_ENDPOINT` | `chora-model-gateway` gRPC endpoint | `gateway.chora.site:443` |
| `CHORA_GATEWAY_AUDIENCE` | ID-token audience for the gateway | `https://gateway.chora.site` |
| `CHORA_GATEWAY_TENANT_ID` | Process-fallback tenant for gateway calls (required) | unset |
| `CHORA_GATEWAY_GCID` | Process-fallback gcid for gateway calls (required) | unset |
| `CHORA_GATEWAY_TOKEN` | Static bearer token for the model gateway | unset |
| `CHORA_GATEWAY_INSECURE` | Plaintext gRPC to a local gateway (dev only) | unset |
| `TENANCY_GRPC_ENDPOINT` | Tenancy service endpoint (`stub://…` for the in-memory stub) | `stub://chora-tenancy` |
| `SHARING_GRPC_ENDPOINT` | Sharing service endpoint (`stub://…` for the in-memory stub) | `stub://chora-sharing` |
| `CHORA_ENV` | Environment label | `dev` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/gRPC trace endpoint | stdout (local dev) |
| `CHORA_SERVICE_VERSION` | Stamped as the OTLP `service.version` attribute | `dev` |

## Dependencies

- **Model gateway** (`chora-model-gateway`) — required for actual LLM calls;
  both sub-agents' models are resolved and metered there.
- **No database** — sessions are in-memory (`session.InMemoryService`), so
  the service runs best with replicas=1.
- **No NATS** — this crew does not subscribe to the event bus.

## Layout

| Path | Purpose |
|---|---|
| `cmd/moderation/` | Binary entry point: wires config, model clients, plugins, and the HTTP server |
| `internal/agent/` | Moderator + Critic prompt composers, condition extractor, instruction provider |
| `internal/agentconfig/` | Embedded per-sub-agent model tier + prompt config (`moderation.yaml`) |
| `internal/agentserver/` | The crew's HTTP API (session + streaming endpoints) |

## Build and test

```sh
go build ./...
go vet ./...
go test ./...
```

The suite is hermetic — no broker, database, gateway, or network is required.

## Docker

```sh
docker build -t chora-moderation .
docker run -p 8080:8080 \
  -e CHORA_GATEWAY_TENANT_ID=<tenant-uuid> \
  -e CHORA_GATEWAY_GCID=<gcid> \
  -e CHORA_GATEWAY_ENDPOINT=host.docker.internal:9090 \
  -e CHORA_GATEWAY_INSECURE=1 \
  chora-moderation
```
