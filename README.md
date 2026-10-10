# chora-moderation

## About

`chora-moderation` is a Go service that runs a two-agent content-moderation pipeline for ChoraCircle posts. A Moderator makes the first-pass `pass | refine | reject` decision, then a Critic reviews that decision and returns the final verdict. Both agents run through `chora-model-gateway`; sessions are stored in memory and the service exposes an ADK-compatible HTTP API.

## Quick start

Prerequisites:

- Go 1.26.6 or newer
- Access to a running `chora-model-gateway` for real model calls

Build and run the service:

```sh
go build ./cmd/moderation
./moderation
```

The service listens on port `8080` by default. For local development against a plaintext gateway, set the gateway endpoint and `CHORA_GATEWAY_INSECURE=1`. The gateway also requires a tenant ID and GCID:

```sh
CHORA_GATEWAY_ENDPOINT=localhost:9090 \
CHORA_GATEWAY_INSECURE=1 \
CHORA_GATEWAY_TENANT_ID=<tenant-uuid> \
CHORA_GATEWAY_GCID=<gcid> \
./moderation
```

The repository also includes a Docker build:

```sh
docker build -t chora-moderation .
docker run -p 8080:8080 \
  -e CHORA_GATEWAY_ENDPOINT=host.docker.internal:9090 \
  -e CHORA_GATEWAY_INSECURE=1 \
  -e CHORA_GATEWAY_TENANT_ID=<tenant-uuid> \
  -e CHORA_GATEWAY_GCID=<gcid> \
  chora-moderation
```

## Usage

The server exposes two POST endpoints:

- `/api/reasoning_engine` for session operations
- `/api/stream_reasoning_engine` for agent runs

Create a session before sending a moderation request:

```sh
curl -sS http://localhost:8080/api/reasoning_engine \
  -H 'Content-Type: application/json' \
  -d '{
    "class_method": "async_create_session",
    "input": {
      "user_id": "<user-id>",
      "state": {
        "tenant_id": "<tenant-uuid>",
        "user_gcid": "<author-gcid>",
        "author_gcid": "<author-gcid>",
        "post_text": "<post body>",
        "mana_tier": "basic"
      }
    }
  }'
```

The session response contains the generated `id`. Use it with `async_stream_query` to run the Moderator -> Critic pipeline:

```sh
curl -N http://localhost:8080/api/stream_reasoning_engine \
  -H 'Content-Type: application/json' \
  -d '{
    "class_method": "async_stream_query",
    "input": {
      "user_id": "<user-id>",
      "session_id": "<session-id>",
      "message": "Run moderation for the post in session state."
    }
  }'
```

The streaming endpoint returns JSON event lines. The request can also use a structured GenAI content object in `input.message`. The moderation prompt is built from the session state on every turn, so `post_text`, tenant ID, and author GCID come from the current session rather than startup-time placeholders.

Session methods supported by `/api/reasoning_engine` are:

| `class_method` | Required input | Result |
| --- | --- | --- |
| `async_create_session` | `user_id`, optional `state` | Created session in `output` |
| `async_get_session` | `user_id`, `session_id` | Session in `output` |
| `async_list_sessions` | `user_id` | `output.sessions` |
| `async_delete_session` | `user_id`, `session_id` | Empty `output` |

Model selection is declared in `internal/agentconfig/moderation.yaml`. Both sub-agents (Moderator and Critic) dispatch the single logical model id `longcat-2.5-preview` as primary and as their only fallback; the former CHEAP/HIGH tiering has collapsed into that one route. `MODERATION_MODERATOR_MODEL` and `MODERATION_CRITIC_MODEL` override the primary model for each sub-agent.

Configuration:

| Variable | Purpose | Default |
| --- | --- | --- |
| `MODERATION_PORT` | HTTP listen port | `8080` |
| `MODERATION_SESSION_APP_NAME` | ADK session app name | `chora-moderation` |
| `MODERATION_MODERATOR_MODEL` | Moderator primary model override | Configured in YAML |
| `MODERATION_CRITIC_MODEL` | Critic primary model override | Configured in YAML |
| `CHORA_GATEWAY_ENDPOINT` | Model gateway gRPC endpoint | `gateway.chora.site:443` |
| `CHORA_GATEWAY_AUDIENCE` | Gateway ID-token audience | `https://gateway.chora.site` |
| `CHORA_GATEWAY_TENANT_ID` | Fallback tenant ID for gateway calls | Required |
| `CHORA_GATEWAY_GCID` | Fallback GCID for gateway calls | Required |
| `CHORA_GATEWAY_TOKEN` | Static gateway bearer token | Unset |
| `CHORA_GATEWAY_INSECURE` | Use plaintext gRPC for a local gateway | Unset |
| `TENANCY_GRPC_ENDPOINT` | Tenancy endpoint | `stub://chora-tenancy` |
| `SHARING_GRPC_ENDPOINT` | Sharing endpoint | `stub://chora-sharing` |
| `CHORA_ENV` | Environment label | `dev` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/gRPC trace endpoint | Stdout |
| `CHORA_SERVICE_VERSION` | OTLP `service.version` value | `dev` |

The service uses an in-memory session store, so sessions are lost when the process stops and the service should run with a single replica when session continuity matters. The moderation service does not require NATS or a database.

Repository layout:

| Path | Purpose |
| --- | --- |
| `cmd/moderation/` | Service entry point |
| `internal/agent/` | Moderator/Critic prompt composition and per-turn state wiring |
| `internal/agentconfig/` | Embedded model and prompt configuration |
| `internal/agentserver/` | HTTP API and ADK runner/session integration |

## Development

Format, vet, test, and build from the repository root:

```sh
gofmt -w .
go vet ./...
go test ./...
go build ./...
```

The test suite covers prompt composition and determinism, session lifecycle over HTTP, streaming event output, request validation, gateway configuration, and payload limits. Tests use in-memory services and stubs, so they do not require a database, NATS, model gateway, or external network.

GitHub Actions runs formatting checks, `go mod tidy` consistency checks, `go vet ./...`, and `go test ./...` on pushes and pull requests targeting `main`.