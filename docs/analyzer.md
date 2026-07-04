# Prompt analyzer

Route42 routes every prompt through an analyzer before model selection. The
analyzer produces two outputs that drive routing:

- **`complexity`** — a `0..1` score estimating task difficulty (0 = trivial
  one-liner, 1 = multi-constraint expert task). It controls the quality
  floor (weak models are filtered out for hard prompts) and the weight
  given to quality vs. cost in the composite score.
- **`category`** — one of `chat`, `code`, `math`, `analysis`, `general`.
- **`signals`** — the per-signal contributions that produced the score, so
  every decision is explainable.

Both analyzers implement the same interface, so they are true drop-ins:
routing, thresholds, and weight adjustment work unchanged.

```go
type AnalysisResult struct {
    Complexity float64            // 0..1
    Category   string             // chat | code | math | analysis | general
    Signals    map[string]float64 // per-signal contributions
    Analyzer   string             // "heuristic" | "llm"
}

type PromptAnalyzer interface {
    Analyze(ctx context.Context, messages []Message) (AnalysisResult, error)
}
```

## Choosing an analyzer

| Analyzer | Cost | How it works |
|---|---|---|
| `heuristic` (default) | free, <1ms | Deterministic signal scoring: code blocks, requirement density, reasoning cues, question fan-out, context depth. Fully explainable — per-signal scores are returned with every decision. |
| `llm` (optional) | free, ~50–200ms | Uses a small local model via Ollama (e.g. `qwen2.5:0.5b`) to classify category and complexity. Smarter on ambiguous prompts, still 100% local and private. Falls back to `heuristic` if Ollama is unavailable. |

```yaml
analyzer:
  mode: heuristic          # heuristic | llm
  llm:
    model: qwen2.5:0.5b    # any small Ollama model
    timeout_ms: 1500       # falls back to heuristic on timeout
```

You can inspect an analyzer's output without running the gateway:

```bash
route42 analyze "def fib(n): return n if n<2 else fib(n-1)+fib(n-2)"
```

## Heuristic: category detection

Category detection sums weighted binary signals per category and picks the
highest score above a threshold (1.0). Below the threshold the prompt is
classified `general`. Matching runs on the last user message plus a cheap
scan of prior context (code blocks anywhere in history count toward `code`).

Keyword signals use plain substring/word-boundary scans over a
once-lowercased window; regular expressions are reserved for genuinely
structural patterns (stack traces, syntax tokens, SQL, file extensions,
list items). This keeps the analyzer under 1ms per request.

| Category | Signals (weight) |
|---|---|
| `code` | fenced code block (3.0), stack-trace pattern (2.5), syntax tokens `def/function/class/import/SELECT/=>/{}` (1.5), file extensions (1.0), verbs implement/refactor/debug/fix (1.0) |
| `math` | LaTeX delimiters (3.0), digit+operator density > 15% (2.0, needs len ≥ 24 so "what's 2+2?" stays chat), solve/equation/derivative/integral/prove/calculate (1.5) |
| `analysis` | compare/summarize/evaluate/analyze/pros and cons/trade-offs (2.0), input length > 2000 chars (1.5), document-like structure (1.0) |
| `chat` | < 120 chars and no structure (1.5), greeting/conversational opener (1.5), second-person casual (1.0) |

## Heuristic: complexity score

Complexity is a transparent weighted sum of seven normalized signals,
clamped to `[0, 1]`. Every fired signal appears in `AnalysisResult.Signals`
with its **post-weight** contribution, so the score is fully auditable.

| # | Signal | Weight | Computation |
|---|---|---|---|
| 1 | `complexity.length` | 0.20 | `min(1, log10(est_tokens)/log10(4000))`, est_tokens = chars/4 |
| 2 | `complexity.requirements` | 0.20 | count of numbered-list items + must/ensure/consider/handle/support, saturating at 8 |
| 3 | `complexity.reasoning` | 0.20 | step by step / design / architect / optimize / explain why / trade-off, saturating at 5 |
| 4 | `complexity.code` | 0.15 | 0 if none; 0.5 small snippet; 1.0 for ≥30-line blocks or multiple blocks |
| 5 | `complexity.questions` | 0.10 | `min(1, (question_marks + multipart_conjunctions) / 4)` |
| 6 | `complexity.depth` | 0.10 | `min(1, prior_turns / 12)` |
| 7 | `complexity.vocabulary` | 0.05 | avg word length + technical-term ratio, normalized |

The weights sum to 1.0. The seven numbers and saturation constants are the
only tunable parameters; they are calibrated against a reference analyzer
on an internal evaluation set.

## How complexity drives routing

- **Quality floor** — the ranker drops models below a complexity-dependent
  quality threshold:

  | complexity | quality floor (0..1) |
  |---|---|
  | <0.25 | 0.30 |
  | 0.25–0.50 | 0.50 |
  | 0.50–0.75 | 0.70 |
  | 0.75–0.90 | 0.85 |
  | ≥0.90 | 0.95 |

  Models with unknown quality (catalog `quality_score: 0`) are exempt —
  lack of data is not evidence of inadequacy.
- **Weight adjustment** — simple prompts favor cost (any qualified model
  handles them); complex prompts favor quality (a weak model must not serve
  a hard task). See [`config.md`](config.md) for the weight tables.

## LLM analyzer (optional)

Uses a small local model via Ollama. 100% local, $0, on-brand.

- Default model: `qwen2.5:0.5b` (configurable; anything Ollama-served works).
- Prompt (single-shot, JSON-forced):
  ```
  Classify this user request. Respond with ONLY JSON:
  {"category":"chat|code|math|analysis|general","complexity":0.0-1.0}
  complexity: 0=trivial one-liner, 0.5=typical task, 1=multi-constraint expert task.
  Request: <last user message, truncated to 1500 chars>
  ```
- Guardrails: 1500ms timeout, strict JSON parse, clamp complexity to
  `[0,1]`, category whitelist. **Any failure → fall back to the
  heuristic analyzer** (routing must never be blocked by analysis errors).
- Cache: LRU on `hash(last user message)` to avoid re-analyzing
  retries/regenerations.

> **Route42 Pro** loads an ML-trained analyzer (`mode: ml`) when the
> commercial model bundle is installed. It slots into the same interface,
> so routing behavior is identical — Pro just has a more accurate
> complexity/category model. Community Edition is and stays fully
> functional on its own.
