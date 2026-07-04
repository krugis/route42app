# Model catalog

The model catalog (`data/catalog.json`) is a versioned snapshot of model
metadata normalized across providers. It is the data the ranking engine
scores on: quality, speed, and pricing for every routable model.

The catalog is embedded in the binary at build time (`data/embed.go`), so a
single `route42` binary ships with the full catalog and no external files.
`route42 models list` shows the merged view of catalog cloud models (for
providers with a configured key) plus locally discovered Ollama models.

## Schema

The catalog is a single JSON object:

```jsonc
{
  "schema_version": 1,                 // breaking-schema bump
  "snapshot_date": "2026-07-04",       // YYYY-MM-DD, when metrics were captured
  "attribution": "...",                // upstream data-source credits
  "models": [ /* ModelInfo[] */ ]
}
```

### `ModelInfo`

| Field | Type | Notes |
|---|---|---|
| `id` | string | Provider-scoped model id, e.g. `"gpt-4o-mini"`. |
| `provider` | string | Canonical provider, e.g. `"openai"`, `"anthropic"`, `"ollama"`. |
| `display_name` | string | Optional human-readable name. |
| `source` | string | `"cloud"` or `"local"`. Catalog local entries are **skipped** at routing time — discovery is the source of truth for what is running. |
| `quality_score` | number | Normalized capability score in `[0,100]`. `0` means **unknown** (the model is not filtered out by the quality floor). |
| `output_tokens_per_second` | number | Median throughput from public benchmarks. `0` = unknown. |
| `time_to_first_token_ms` | number | Median TTFT in milliseconds. `0` = unknown. |
| `input_price_per_mtok` | number | USD per million input tokens. `0` for free models. |
| `output_price_per_mtok` | number | USD per million output tokens. `0` for free models. |
| `context_window` | number | Context window in tokens. |
| `supports_tools` | bool | Whether the model supports function/tool calling. Drives the tool-capable filter. |
| `supports_vision` | bool | Whether the model accepts image inputs. |

### Validation

`catalog.Load()` rejects a catalog that fails any of:

- `schema_version` is not `1` (this build supports only schema 1).
- the catalog is empty.
- a model is missing `id` or `provider`.
- a `(provider, id)` pair is duplicated.
- `source` is not `cloud` or `local`.
- `quality_score` is outside `[0,100]`.
- any price is negative.

A custom catalog can be loaded for testing via `catalog.LoadFile(path)`,
overriding the embedded snapshot.

## Attribution

The embedded catalog combines two public data sources:

- **Capability and pricing data** — derived from LiteLLM's
  `litellm_tool_capability_map.json` (MIT-licensed): callable API ids,
  context windows, capability flags, and prices.
- **Quality and speed metrics** — Route42 composite scores derived from
  public benchmark data. `quality_score` is a composite
  (`0.75 * intelligence + 0.25 * coding`, rounded).

The `attribution` field on the catalog records this. Community PRs that
update metrics must keep the attribution accurate.

## How the ranker uses catalog fields

| Catalog field | Ranking use |
|---|---|
| `quality_score` | Quality factor (`/100`); also gates the complexity-derived quality floor (unknown `0` is exempt). |
| `output_tokens_per_second` + `time_to_first_token_ms` | Speed factor: `1000 / (ttft + 1000/tps)`. Local models with no data score as a fast latency tier. |
| `input_price_per_mtok` + `output_price_per_mtok` | Cost factor: log-scale, P95-clipped normalization of the 3:1 blended price. Free models score 1.0. Also drives the `max_cost` filter and the response's `est_cost_cents`. |
| `supports_tools` | Hard filter when the request carries `tools`. |
| `source` | `local` entries are skipped in favor of runtime Ollama discovery. |

See [`analyzer.md`](analyzer.md) for how complexity drives the quality
floor and weight adjustment, and [`config.md`](config.md) for the
preference modes and constraints.

## Updating the catalog

The catalog is a versioned snapshot — we welcome PRs that fix or update
entries. To keep review simple and the data trustworthy:

- **Keep changes factual and sourced.** Link the provider's pricing or
  model page in the PR description.
- **Do not change the schema.** Schema changes (`schema_version` bumps)
  need an issue first.
- **One provider per PR** where possible.
- **Run validation locally** before pushing:
  ```bash
  go test ./internal/catalog/...
  ```
  `catalog.Load()` validates on every load, so a broken entry fails the
  tests and the build.
- **Keep `attribution` accurate** if you add a new metric source.

### Adding a model

Add a `ModelInfo` object to the `models` array:

```jsonc
{
  "id": "gpt-4o-mini",
  "provider": "openai",
  "source": "cloud",
  "quality_score": 65,
  "input_price_per_mtok": 0.15,
  "output_price_per_mtok": 0.60,
  "context_window": 128000,
  "supports_tools": true,
  "supports_vision": true,
  "output_tokens_per_second": 120,
  "time_to_first_token_ms": 400
}
```

If you don't know a metric, omit it or set it to `0` — unknown quality is
treated conservatively-but-fairly (the model is not filtered out), and
unknown speed penalizes only the speed factor.

### Provider names

Use canonical provider names (the registry resolves aliases, but the
catalog should use the canonical form):

`openai`, `anthropic`, `gemini`, `mistral`, `groq`, `deepseek`, `alibaba`,
`moonshot`, `nvidia`, `openrouter`, `zai`, `ollama`.

> **Route42 Pro** has an on-demand catalog update pipeline that refreshes
> pricing and metrics. Community Edition ships a static snapshot; keeping
> it fresh is a community effort via PRs.
