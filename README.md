# agentboard API

A lightweight HTTP service for managing agent-friendly kanban boards. The runtime is designed for Railway, runs a single binary (`agentboard-api`), and persists state in SQLite.

## Quick start

```bash
git clone https://github.com/markx3/agentboard.git
cd agentboard

export AGENTBOARD_DB_PATH="$PWD/board.db"
export AGENTBOARD_API_KEY="$(openssl rand -hex 16)"
export PORT=8080

go run ./cmd/agentboard-api
```

Then create a project and a task:

```bash
BASE=http://127.0.0.1:$PORT
HDR=(-H "X-API-Key: $AGENTBOARD_API_KEY")

PROJECT=$(curl "${HDR[@]}" -s -X POST -H 'Content-Type: application/json' \
  -d '{"slug":"default","name":"Default"}' "$BASE/projects" | jq -r .id)

curl "${HDR[@]}" -H "X-Agentboard-Project: default" -H 'Content-Type: application/json' \
  -d '{"title":"hello","description":"world","project_id":"'$PROJECT'"}' "$BASE/tasks"
```

## Environment variables

| Variable | Required | Description |
| --- | --- | --- |
| `AGENTBOARD_DB_PATH` | ✓ | Absolute path to the SQLite database. Mount `/data` on Railway and set `/data/board.db`. |
| `AGENTBOARD_API_KEY` | ✓ | Shared secret; sent via `X-API-Key` or `Authorization: Bearer`. |
| `PORT` |  | Listener port (defaults to `8080`). |

## API overview

All requests must include `X-API-Key`. Every non-project route also needs either `X-Agentboard-Project: <slug>` or `?project_id=<uuid>`.

- `GET /projects`, `POST /projects`, `GET /projects/{id|slug}`, `PUT /projects/{id}`
- `GET /tasks` (filters: `status`, `assignee`, `search`)
- `POST /tasks` with `{"title": "...", "project_id": "<uuid>"}` plus optional `description` and `enrich`
- Task subroutes: `/claim`, `/unclaim`, `/agent-activity`, `/comments`, `/dependencies`, `/suggestions`
- `GET/POST /suggestions`, `/suggestions/{id}/accept`, `/suggestions/{id}/dismiss`
- `GET /status` — accepts `Accept: text/plain` or `?format=text` for a simple board view
- `GET /board?project=<slug>` — public, read-only HTML/plaintext snapshot (no API key needed)

Plaintext responses are supported for `/tasks` and `/status` to mirror the old TUI at a glance.

## Railway deployment

The provided `Dockerfile` builds a static binary and copies only `/usr/local/bin/agentboard-api` into Distroless. Typical Railway setup:

1. Mount a volume at `/data`.
2. Set `AGENTBOARD_DB_PATH=/data/board.db`.
3. Set `AGENTBOARD_API_KEY=<your-secret>`.
4. Deploy the repository—no additional services are required.

## Testing

```bash
go test ./...
```

## Acknowledgements

This project is a derivative of the original Agentboard created by [@markx3](https://github.com/markx3).
