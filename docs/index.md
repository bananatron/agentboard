# Agentboard API Reference

The API exposes every task-management surface area of Agentboard. This document is GitHub Pages friendly—commit it under `docs/` and enable Pages → `docs/` to publish.

## Base URL & Versioning

- **Local:** `http://127.0.0.1:8080`
- **Railway:** `https://<your-domain>.up.railway.app`
- **Version:** `v1` (implicit). Backward-incompatible changes will bump the base path.

## Authentication & Project Context

All requests require the shared secret generated at deploy time:

```
X-API-Key: <AGENTBOARD_API_KEY>
```

`Authorization: Bearer <key>` is also accepted.

Every **non-project** endpoint must additionally specify a project:

- Header: `X-Agentboard-Project: <slug>`
- OR query: `?project_id=<uuid>`
- Bodies that create resources must include a matching `"project_id"` field.

Requests missing the project context return `400 {"error":"project_required"}`.

## Projects

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/projects` | List all projects |
| `POST` | `/projects` | `{ "slug": "default", "name": "Default" }` |
| `GET` | `/projects/{slug-or-id}` | Fetch single project |
| `PUT` | `/projects/{slug-or-id}` | `{ "name": "New Name" }` |

Use the returned ID when creating tasks. The server auto-seeds a `default` project on migration.

## Tasks

Headers for the examples below:

```
HDR=(-H "X-API-Key: $KEY" -H "X-Agentboard-Project: default")
```

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/tasks?status=&assignee=&search=` | List tasks scoped to the project |
| `POST` | `/tasks` | `{ "title": "...", "description": "...", "project_id": "<uuid>", "enrich": false }` |
| `GET` | `/tasks/{id}` | Fetch single task (includes `blocked_by`) |
| `PATCH` | `/tasks/{id}` | Update fields (`title`, `description`, `status`, `assignee`, `branch`, `pr_url`, `pr_number`, `enrichment_status`, `enrichment_agent`) |
| `DELETE` | `/tasks/{id}` | Delete task |
| `POST` | `/tasks/{id}/claim` | `{ "assignee": "alice" }` |
| `POST` | `/tasks/{id}/unclaim` | Clears assignment |
| `POST` | `/tasks/{id}/agent-activity` | `{ "activity": "reviewing diff" }` (200 chars max) |

Comments & dependencies:

| Method | Path | Body |
| --- | --- | --- |
| `GET` | `/tasks/{id}/comments` | — |
| `POST` | `/tasks/{id}/comments` | `{ "author": "...", "body": "..." }` |
| `GET` | `/tasks/{id}/dependencies` | — |
| `POST` | `/tasks/{id}/dependencies` | `{ "depends_on": "<task-id>" }` |
| `DELETE` | `/tasks/{id}/dependencies` | `{ "depends_on": "<task-id>" }` |

## Suggestions

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/suggestions?status=pending&task_id=` | Filters by project + optional task |
| `POST` | `/suggestions` | Global proposals (set `"project_id"` and leave `"task_id"` empty) |
| `POST` | `/tasks/{id}/suggestions` | Suggestion tied to a task (body must include `"project_id"`) |
| `GET` | `/suggestions/{id}` | Fetch single suggestion |
| `POST` | `/suggestions/{id}/accept` | Accept (proposals become tasks scoped to the same project) |
| `POST` | `/suggestions/{id}/dismiss` | Dismiss |

Body structure:

```json
{
  "project_id": "<uuid>",
  "task_id": "",
  "type": "proposal|hint|enrichment",
  "author": "agent-01",
  "title": "Add tests",
  "message": "Full description",
  "reason": "Optional context"
}
```

## Status & Text Mode

`GET /status` aggregates counts, active agents, enrichment queue, and pending suggestions for the requested project.

```
curl "${HDR[@]}" http://localhost:8080/status
curl "${HDR[@]}" -H 'Accept: text/plain' http://localhost:8080/status
```

Both `/status` and `/tasks` support `Accept: text/plain` (or `?format=text`) to emit a plaintext board layout for quick debugging/`kubectl logs`.

## Error Format

```
HTTP/1.1 400 Bad Request
{"error":"invalid_status"}
```

Codes used: `400`, `401`, `404`, `500`.

## Hosting with GitHub Pages

1. Commit this `docs/` folder.
2. Settings → Pages → select `main` / `/docs`.
3. GitHub Pages will serve the Markdown (Jekyll disabled via theme config).
