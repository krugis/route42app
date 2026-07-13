# Configuration

Route42 is configured by (in order of precedence):

1. **Built-in defaults** — a zero-config setup that works with only a local
   Ollama installation.
2. **YAML file** — `route42.yaml` in the working directory (implicit) or a
   path passed via `--config` (must exist).
3. **Environment variables** — `ROUTE42_*`, overriding file values.

A non-empty `--config` path must exist; an implicit `route42.yaml` is
picked up only if present, otherwise the defaults apply (zero-config first
run creates the database and works Ollama-only with no keys).

## Full reference

```yaml
server:
  port: 4242                # 1..65535
  api_token: ""             # optional bearer token for /api/* (empty = no auth)
  ui: true                  # serve the embedded web console at /

analyzer:
  mode: heuristic           # heuristic | llm | hybrid
  llm:
    model: qwen2.5:0.5b     # any small Ollama model (required when mode: llm or hybrid)
    timeout_ms: 1500        # >0; falls back to heuristic on timeout
    hybrid_weight: 0.5      # 0..1; weight for LLM score in hybrid mode (default 0.5)

ollama:
  base_url: http://localhost:11434   # must not be empty

db:
  path: <user config dir>/route42/route42.db   # must not be empty

providers:
  openai:
    api_key: sk-...         # optional; keys can also be added via /api/keys
    base_url: ""            # optional override (e.g. a proxy)

prefs:                      # initial routing preferences (first-run seed)
  priority: balanced        # balanced | fast | cheap | accurate
  max_cost_cents: 0         # >=0; 0 = no per-request cost cap
  latency_tolerance_ms: 0   # >=0; 0 = no TTFT cap
  only_free: false          # drop paid models (soft filter)
  only_local: false         # drop cloud models (hard filter)
  max_response_tokens: 0    # >=0; 0 = provider default (used for cost estimate)
  default_model: ""         # pinned model id (empty = always route)
  fallback_depth: 2         # >=0; extra candidates tried on retryable failure
  disallowed_models: []     # "provider/id" or bare "id", case-insensitive
```

### `server`

| Field | Default | Validation | Notes |
|---|---|---|---|
| `port` | `4242` | 1–65535 | TCP port the gateway listens on. |
| `api_token` | `""` | — | When set, `/api/*` and `/v1/*` require `Authorization: Bearer <token>`. `/health` and the web console assets are always public (the console asks for the token and sends it on its API calls). Empty (default) = no auth, for local single-user use. |
| `ui` | `true` | — | Serves the embedded web console at `/`. Set `false` for a headless gateway; the CLI and HTTP API are unaffected either way. |

### `analyzer`

| Field | Default | Validation | Notes |
|---|---|---|---|
| `mode` | `heuristic` | `heuristic` \| `llm` \| `hybrid` | Selects the prompt analyzer. See [`analyzer.md`](analyzer.md). |
| `llm.model` | `qwen2.5:0.5b` | required when `mode: llm` or `mode: hybrid` | Any Ollama-served model. |
| `llm.timeout_ms` | `1500` | >0 | On timeout the request falls back to the heuristic analyzer. |
| `llm.hybrid_weight` | `0.5` | 0..1 | Weight given to LLM score in hybrid mode. `0` = pure heuristic, `1` = pure LLM. |

### `ollama`

| Field | Default | Validation | Notes |
|---|---|---|---|
| `base_url` | `http://localhost:11434` | must not be empty | Used for discovery, completions, and the LLM analyzer. An unreachable Ollama is never fatal — it just means "no local models". |

### `db`

| Field | Default | Validation | Notes |
|---|---|---|---|
| `path` | `<user config dir>/route42/route42.db` | must not be empty | SQLite database file. Parent dirs are created on first run. Falls back to `route42.db` in the working dir if the OS config dir is unavailable. |

### `providers` (map)

Static per-provider credentials and optional base-URL overrides. Keys set
here take precedence over keys stored in the DB (added via `/api/keys` or
`route42 keys add`). A provider with a key in either place is "available"
for routing.

| Field | Notes |
|---|---|
| `api_key` | Provider API key. Also manageable at runtime via `/api/keys` (stored encrypted). |
| `base_url` | Optional override of the provider's default endpoint (e.g. a corporate proxy). Also the offline-test seam. |

### `prefs` (initial preferences)

These seed the preferences on first run. After that, preferences live in
the database and are editable via `/api/prefs` or `route42 prefs set`. All
fields are validated on `PUT /api/prefs` and `prefs set`.

| Field | Default | Validation | Notes |
|---|---|---|---|
| `priority` | `balanced` | `balanced` \| `fast` \| `cheap` \| `accurate` | Preference mode (see below). |
| `max_cost_cents` | `0` | >=0 | Per-request cost estimate cap. `0` = no cap. **Soft filter** (reset on empty). |
| `latency_tolerance_ms` | `0` | >=0 | Max TTFT. `0` = no cap. Unknown TTFT passes. **Soft filter**. |
| `only_free` | `false` | bool | Drop paid models. **Soft filter**. |
| `only_local` | `false` | bool | Drop all cloud models. **Hard filter** — errors if no local models are available. |
| `max_response_tokens` | `0` | >=0 | Response token cap used for the cost estimate. `0` = provider default (1024). |
| `default_model` | `""` | — | Pinned model id. Empty = always route. |
| `fallback_depth` | `2` | >=0 | Extra candidates tried on retryable provider failure (429/5xx/network). Non-retryable (401/404) short-circuits. |
| `disallowed_models` | `[]` | — | List of `"provider/id"` or bare `"id"`, case-insensitive. **Hard filter**. |

## Environment variables

All `ROUTE42_*` variables override file values. Provider keys use
`ROUTE42_<PROVIDER>_API_KEY` (e.g. `ROUTE42_OPENAI_API_KEY`).

| Variable | Maps to |
|---|---|
| `ROUTE42_PORT` | `server.port` |
| `ROUTE42_API_TOKEN` | `server.api_token` |
| `ROUTE42_UI` | `server.ui` |
| `ROUTE42_ANALYZER_MODE` | `analyzer.mode` |
| `ROUTE42_ANALYZER_LLM_MODEL` | `analyzer.llm.model` |
| `ROUTE42_ANALYZER_LLM_TIMEOUT_MS` | `analyzer.llm.timeout_ms` |
| `ROUTE42_ANALYZER_LLM_HYBRID_WEIGHT` | `analyzer.llm.hybrid_weight` |
| `ROUTE42_OLLAMA_BASE_URL` | `ollama.base_url` |
| `ROUTE42_DB_PATH` | `db.path` |
| `ROUTE42_<PROVIDER>_API_KEY` | `providers.<provider>.api_key` |
| `ROUTE42_ENCRYPTION_KEY` | Encryption key for stored provider keys (any string, SHA-256 derived). If unset, a random 32-byte keyfile (`route42.key`, `0600`) is created next to the DB. |

## Preference modes and weights

The composite score is `quality*Wq + speed*Ws + cost*Wc`, with weights
chosen by preference mode and adjusted by complexity. Simple prompts favor
cost (any qualified model handles them); complex prompts favor quality (a
weak model must not serve a hard task).

| preference | complexity | weights (Q/S/C) |
|---|---|---|
| `balanced` | c<0.25 | 0.15 / 0.15 / 0.70 |
| `balanced` | 0.25–0.50 | 0.25 / 0.25 / 0.50 |
| `balanced` | 0.50–0.75 | ~0.60 / 0.24 / 0.16 (rescaled) |
| `balanced` | c≥0.75 | 0.70 / 0.15 / 0.15 |
| `cheap` | c<0.25 | 0.05 / 0.05 / 0.90 |
| `cheap` | 0.25–0.50 | 0.10 / 0.10 / 0.80 |
| `cheap` | c≥0.50 | 0.30 / 0.20 / 0.50 |
| `fast` | c<0.75 | 0.30 / 0.50 / 0.20 |
| `fast` | c≥0.75 | 0.60 / 0.30 / 0.10 |
| `accurate` | c<0.75 | 0.70 / 0.15 / 0.15 |
| `accurate` | c≥0.75 | 0.80 / 0.10 / 0.10 |

`only_local` is a hard filter, not a scoring mode — the surviving local
models are scored with `balanced` weights adjusted by complexity.

### Winner selection

- `balanced` / `only_local` — the top composite score wins.
- `cheap` — the cheapest blended price within the top-10 composite window.
- `fast` — the highest speed score within the top-10 window.
- `accurate` — the highest quality within the top-5 window.

Ties keep the earlier candidate (lower `provider/id`).

## Filters

Filters run in two stages (see `DESIGN.md` §9 for the full pipeline):

1. **Hard filters** (functional, non-resettable → `503` if empty):
   `disallowed_models`, `only_local`, `tool_capable` (only when `tools` is
   present in the request).
2. **Soft filters** (preference, resettable — `FILTER_RESET` if they would
   empty the pool): `only_free`, `max_cost`, `latency_tolerance`,
   `quality_floor`. On reset, `Policy.SoftReset` is set and the
   pre-soft-filter set is kept.

## Minimal example

A zero-config `route42 serve` works with only a local Ollama: no keys, no
YAML, no database — the DB is created on first run. To add cloud
arbitrage, add one key:

```bash
route42 keys add openai sk-...
# or via the API:
curl -X POST localhost:4242/api/keys -d '{"provider":"openai","api_key":"sk-..."}'
```

A full `route42.yaml` for a local-first, cheap, latency-bounded setup:

```yaml
analyzer:
  mode: llm
  llm: { model: qwen2.5:0.5b, timeout_ms: 1500 }
prefs:
  priority: cheap
  only_free: false
  latency_tolerance_ms: 2000
  max_response_tokens: 2048
  fallback_depth: 2
```
