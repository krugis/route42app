# Route42 Community Edition

**Local-first LLM router: the right model for every prompt — your GPU first, cloud only when it's worth it.**

Route42 is an OpenAI-compatible gateway that analyzes each prompt and routes it to the optimal model across local (Ollama) and cloud providers (OpenAI, Anthropic, Google, Mistral, Groq, DeepSeek, and more). Simple prompts run on your own hardware for $0.00; complex ones go to the cheapest cloud model that can actually handle them.

Runs entirely on your machine. No telemetry, no account required, your API keys never leave your device.

```
Your app ──▶ localhost:4242 ──▶ [ analyze → score → rank ] ──▶ Ollama (free)
             (OpenAI-compatible)                          └──▶ Cloud API (when needed)
```

## Why Route42 instead of a plain gateway?

Gateways like LiteLLM or Bifrost unify provider APIs and load-balance — but they don't decide *which model deserves this prompt*. Route42 adds the decision layer:

- **Complexity-aware routing** — every prompt gets a complexity score; "what's 2+2" never hits a premium model.
- **Cost arbitrage** — quality/speed/cost composite scoring picks the cheapest *qualified* model.
- **Local-first** — installed Ollama models are auto-discovered and treated as first-class, zero-cost candidates.
- **Explainable** — routing decisions are deterministic and inspectable; the response tells you *why* a model was chosen.

## Features

### Routing engine
- **OpenAI-compatible API** at `localhost:4242` — streaming (SSE) and non-streaming, works with any OpenAI SDK, Claude Code, Cursor, Continue.dev, or anything that speaks `/chat/completions`.
- **Prompt analysis with pluggable analyzers** (see below): complexity score (0–1) + category (chat / code / math / analysis / general).
- **Multi-criteria ranking** — composite quality/speed/cost scoring with weights adjusted by complexity, category, and your preference mode.
- **Preference modes** — `balanced`, `fast`, `cheap`, `accurate`.
- **Hard constraints** — max cost per request, latency tolerance, disallowed models, only-local, only-free, max response tokens, pinned default model.
- **Fallback chains** — configurable fallback depth when the selected model or provider fails.
- **Tool-calling-aware routing** — prompts that need function calling are only routed to models with verified tool support.

### Prompt analyzers (pick per deployment)
| Analyzer | Cost | How it works |
|---|---|---|
| `heuristic` (default) | free, <1ms | Deterministic signal scoring: code blocks, requirement density, reasoning cues, question fan-out, context depth. Fully explainable — per-signal scores are returned with every decision. |
| `llm` (optional) | free, ~50–200ms | Uses a small local model via Ollama (e.g. `qwen2.5:0.5b`) to classify category and complexity. Smarter on ambiguous prompts, still 100% local and private. Falls back to `heuristic` if Ollama is unavailable. |

### Providers & models
- **Cloud providers:** OpenAI, Anthropic, Google Gemini, Mistral, Groq, DeepSeek, Alibaba, Moonshot, NVIDIA, OpenRouter.
- **Local:** automatic Ollama model discovery; local models score as $0 cost with no network latency.
- **Model catalog:** versioned snapshot of models with quality/speed/price metrics, normalized across providers. Community PRs welcome.

### Privacy & operations
- **Bring your own keys** — per-provider API keys stored encrypted in a local SQLite database.
- **No telemetry** — nothing leaves your machine except the LLM calls you route.
- **Interaction log & stats** — every request records the chosen model, rationale, cost, and latency; browse usage and spend locally.
- **Single binary** — Go backend, SQLite storage, no external services.

## Quick start

```bash
# 1. Run
./route42 serve            # starts on localhost:4242

# 2. Add a provider key (or none — Ollama-only works fine)
curl -X POST localhost:4242/api/keys -d '{"provider":"openai","api_key":"sk-..."}'

# 3. Chat — Route42 picks the model
curl localhost:4242/api/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Explain quantum computing to a 10-year-old"}]}'
```

Point any OpenAI client at `http://localhost:4242` and it just works.

## How routing decisions are made

1. **Analyze** — the configured analyzer scores complexity (0–1) and detects the category.
2. **Filter** — remove models that fail your constraints (cost cap, tool support, quality floor for the detected complexity).
3. **Score** — each candidate gets a composite score from quality, speed, and cost metrics, weighted by your preference mode and the prompt's complexity.
4. **Select & fall back** — highest score wins; on failure, the next candidate in the chain takes over.

Every response includes the selected model and the analyzer's signal breakdown, so you can always answer "why did it pick that model?"

### Analyzer configuration

```yaml
analyzer:
  mode: heuristic          # heuristic | llm
  llm:
    model: qwen2.5:0.5b    # any small Ollama model
    timeout_ms: 1500       # falls back to heuristic on timeout
```

## Route42 Pro

The hosted/desktop [Route42 Pro](https://route42.app) builds on this engine with ML-trained complexity & category models, behavioral learning (routing adapts to your history), personal speed statistics measured from your own traffic, on-demand catalog updates, and a polished desktop app. Community Edition is and stays fully functional on its own.

## License

Apache-2.0. "Route42" name and logo are trademarks of Krugis.
