package analyzer

import (
	"context"
	"math"
	"regexp"
	"strings"
)

// HeuristicAnalyzer scores prompts with deterministic, explainable signal
// scoring: no model files, no network calls, sub-millisecond per request.
// It is the default analyzer.
//
// Category detection sums weighted binary signals per category and picks
// the highest score above a threshold. Complexity is a weighted sum of
// seven normalized signals, clamped to [0,1]. Every fired signal appears
// in AnalysisResult.Signals with its post-weight contribution.
type HeuristicAnalyzer struct{}

// NewHeuristic returns the default heuristic analyzer.
func NewHeuristic() *HeuristicAnalyzer { return &HeuristicAnalyzer{} }

// Complexity signal weights. They sum to 1.0; complexity is their weighted
// sum. Values are calibrated against the reference analyzer on an internal
// evaluation set, bounded so no single signal dominates (see
// docs/analyzer.md for the methodology).
const (
	weightLength       = 0.35 // log-scaled prompt size
	weightRequirements = 0.15 // numbered items + constraint verbs
	weightReasoning    = 0.14 // multi-step reasoning cues
	weightCode         = 0.09 // code presence and size
	weightQuestions    = 0.08 // question fan-out
	weightDepth        = 0.10 // conversation depth
	weightVocabulary   = 0.09 // vocabulary rarity
)

// Saturation constants: signal raw counts are divided by these and capped
// at 1.0, so e.g. eight constraint hits count as "fully constrained".
const (
	satRequirements = 8
	satReasoning    = 5
	satQuestions    = 4
	satDepth        = 12 // prior conversation turns
	// lengthRefTokens is the estimated token count treated as
	// "maximally long" for the length signal (log-scaled).
	lengthRefTokens = 4000
	// bigCodeLines: a fenced block at least this long scores as large code.
	bigCodeLines = 30
	// categoryThreshold is the minimum category score; below it the
	// prompt is classified as general.
	categoryThreshold = 1.0
	// scanWindowBytes caps how much of a huge message the signals scan;
	// length-based signals still use the full size.
	scanWindowBytes = 16 * 1024
	// densityMinLen guards the digit/operator density signal against
	// firing on tiny messages like "what's 2+2?".
	densityMinLen = 24
)

// Category signal weights (DESIGN: weighted binary signals).
const (
	sigCodeFence      = 3.0
	sigCodeStackTrace = 2.5
	sigCodeSyntax     = 1.5
	sigCodeFileExt    = 1.0
	sigCodeVerbs      = 1.0

	sigMathLatex   = 3.0
	sigMathDensity = 2.0
	sigMathWords   = 1.5

	sigAnalysisWords     = 2.0
	sigAnalysisLongInput = 1.5
	sigAnalysisDocLike   = 1.0

	sigChatShort    = 1.5
	sigChatGreeting = 1.5
	sigChatCasual   = 1.0
)

// Structural patterns that genuinely need regular expressions. Keyword
// signals use plain substring scans over a lowercased window instead —
// an order of magnitude faster than Go's regexp on alternations.
var (
	reStackTrace = regexp.MustCompile(`(?m)(Traceback \(most recent call last\)|^\s*at .+\(.+:\d+(:\d+)?\)|File "[^"]+", line \d+|panic: |Exception in thread|[A-Za-z]\w+(Error|Exception):)`)
	reSyntax     = regexp.MustCompile(`(?m)(\bdef |\bfunc |\bfunction[ (]|\bclass \w+[({:]|\bimport [\w{."']|#include\s*<|console\.log|=>|;\s*$|print\(|&&|\|\|)`)
	reSQLLower   = regexp.MustCompile(`\bselect\b[\s\S]{1,120}?\bfrom\b`)
	reFileExt    = regexp.MustCompile(`\b[\w/.-]+\.(go|py|js|ts|tsx|jsx|java|rs|cpp|cc|h|hpp|cs|rb|php|sql|sh|bash|ya?ml|json|toml|html|css)\b`)
	reListItem   = regexp.MustCompile(`(?m)^\s*(\d+[.)]|[-*])\s+\S`)
)

// Keyword tables, matched on the lowercased window. "Prefix" entries match
// any word starting with the token (design → designing); "phrase" entries
// are plain substring hits. latexTokens match case-sensitively.
var (
	codeVerbPrefixes = []string{"implement", "refactor", "debug", "compile", "segfault"}
	codeVerbPhrases  = []string{"stack trace", "stacktrace", "unit test", "null pointer"}

	latexTokens = []string{`\(`, `\[`, "$$", `\frac`, `\int`, `\sum`, `\sqrt`, `\begin{`}

	mathPrefixes = []string{"solv", "equation", "derivativ", "integral", "prove", "theorem", "calculat", "probabilit", "polynomial", "matrix"}

	analysisPrefixes = []string{"compar", "summari", "evaluat", "analy", "assess", "critique"}
	analysisPhrases  = []string{"pros and cons", "trade-off", "tradeoff"}

	greetingPrefixes = []string{"hi", "hey", "hello", "yo", "thanks", "thank you", "good morning", "good afternoon", "good evening", "how are you", "what's up", "whats up"}
	casualPhrases    = []string{"can you", "could you", "do you", "are you", "what do you think", "tell me"}

	requireWords = []string{"must", "ensure", "consider", "handle", "support", "require", "requires", "required"}

	reasoningPrefixes = []string{"design", "architect", "optimi", "justif"}
	reasoningPhrases  = []string{"step by step", "step-by-step", "explain why", "walk through", "walk me through", "trade-off", "tradeoff"}

	multipartPhrases = []string{"and also", "as well as", "additionally", "furthermore", "in addition"}
)

// Analyze implements PromptAnalyzer. It never returns an error.
func (h *HeuristicAnalyzer) Analyze(_ context.Context, messages []Message) (AnalysisResult, error) {
	res := AnalysisResult{
		Category: CategoryGeneral,
		Analyzer: NameHeuristic,
		Signals:  map[string]float64{},
	}

	last := lastUserMessage(messages)
	trimmed := strings.TrimSpace(last)
	if trimmed == "" {
		return res, nil
	}

	// Signals scan a capped window so pathological inputs stay fast;
	// size-based signals use the true length.
	window := last
	if len(window) > scanWindowBytes {
		window = window[:scanWindowBytes]
	}
	lower := strings.ToLower(window)

	res.Category = h.detectCategory(window, lower, last, messages, res.Signals)
	res.Complexity = h.scoreComplexity(window, lower, last, messages, res.Signals)
	return res, nil
}

// detectCategory scores each category's weighted signals over the scan
// window and returns the winner, or CategoryGeneral below the threshold.
// Ties break by specificity: code > math > analysis > chat.
func (h *HeuristicAnalyzer) detectCategory(window, lower, full string, messages []Message, signals map[string]float64) string {
	var code, mathScore, analysis, chat float64

	// Code blocks anywhere in the conversation mark a coding session.
	if strings.Contains(window, "```") || historyHasFence(messages) {
		code += sigCodeFence
		signals["category.code.fence"] = sigCodeFence
	}
	if reStackTrace.MatchString(window) {
		code += sigCodeStackTrace
		signals["category.code.stack_trace"] = sigCodeStackTrace
	}
	// Syntax tokens appear in prose too; require two distinct hits.
	syntaxHits := len(reSyntax.FindAllString(window, 2))
	if syntaxHits < 2 && strings.Contains(lower, "select") && reSQLLower.MatchString(lower) {
		syntaxHits++
	}
	if syntaxHits >= 2 {
		code += sigCodeSyntax
		signals["category.code.syntax"] = sigCodeSyntax
	}
	if reFileExt.MatchString(window) {
		code += sigCodeFileExt
		signals["category.code.file_ext"] = sigCodeFileExt
	}
	if countTokens(lower, codeVerbPrefixes, codeVerbPhrases, 1) > 0 {
		code += sigCodeVerbs
		signals["category.code.verbs"] = sigCodeVerbs
	}

	if containsAny(window, latexTokens) {
		mathScore += sigMathLatex
		signals["category.math.latex"] = sigMathLatex
	}
	if len(full) >= densityMinLen && digitOperatorDensity(window) > 0.15 {
		mathScore += sigMathDensity
		signals["category.math.density"] = sigMathDensity
	}
	if countTokens(lower, mathPrefixes, nil, 1) > 0 {
		mathScore += sigMathWords
		signals["category.math.words"] = sigMathWords
	}

	if countTokens(lower, analysisPrefixes, analysisPhrases, 1) > 0 {
		analysis += sigAnalysisWords
		signals["category.analysis.words"] = sigAnalysisWords
	}
	if len(full) > 2000 {
		analysis += sigAnalysisLongInput
		signals["category.analysis.long_input"] = sigAnalysisLongInput
	}
	if strings.Count(window, "\n\n") >= 3 {
		analysis += sigAnalysisDocLike
		signals["category.analysis.doc_like"] = sigAnalysisDocLike
	}

	if len(full) < 120 && !strings.Contains(full, "\n") && !strings.Contains(full, "```") {
		chat += sigChatShort
		signals["category.chat.short"] = sigChatShort
	}
	if hasGreetingPrefix(lower) {
		chat += sigChatGreeting
		signals["category.chat.greeting"] = sigChatGreeting
	}
	if len(full) < 200 && containsAny(lower, casualPhrases) {
		chat += sigChatCasual
		signals["category.chat.casual"] = sigChatCasual
	}

	best, bestScore := CategoryGeneral, 0.0
	for _, c := range []struct {
		name  string
		score float64
	}{
		// Order encodes tie-break specificity.
		{CategoryCode, code},
		{CategoryMath, mathScore},
		{CategoryAnalysis, analysis},
		{CategoryChat, chat},
	} {
		if c.score > bestScore {
			best, bestScore = c.name, c.score
		}
	}
	if bestScore < categoryThreshold {
		return CategoryGeneral
	}
	return best
}

// scoreComplexity computes the seven-signal weighted sum from DESIGN §4.3.
func (h *HeuristicAnalyzer) scoreComplexity(window, lower, full string, messages []Message, signals map[string]float64) float64 {
	record := func(key string, raw, weight float64) float64 {
		contribution := raw * weight
		if contribution > 0 {
			signals[key] = contribution
		}
		return contribution
	}

	// 1. Log-scaled prompt size (full length, not the scan window).
	estTokens := float64(len(full)) / 4
	var length float64
	if estTokens >= 1 {
		length = clamp01(math.Log10(estTokens) / math.Log10(lengthRefTokens))
	}

	// 2. Requirement density: list items plus constraint verbs.
	reqCount := len(reListItem.FindAllString(window, satRequirements)) +
		countWords(lower, requireWords, satRequirements)
	requirements := clamp01(float64(min(reqCount, satRequirements)) / satRequirements)

	// 3. Reasoning cues.
	reasoning := clamp01(float64(countTokens(lower, reasoningPrefixes, reasoningPhrases, satReasoning)) / satReasoning)

	// 4. Code presence and size.
	code := codeSizeSignal(window)

	// 5. Question fan-out.
	fanOut := strings.Count(window, "?") + countPhrases(lower, multipartPhrases, satQuestions)
	questions := clamp01(float64(fanOut) / satQuestions)

	// 6. Conversation depth: messages before the current user turn.
	depth := clamp01(float64(len(messages)-1) / satDepth)

	// 7. Vocabulary rarity.
	vocabulary := vocabularySignal(window)

	total := record("complexity.length", length, weightLength) +
		record("complexity.requirements", requirements, weightRequirements) +
		record("complexity.reasoning", reasoning, weightReasoning) +
		record("complexity.code", code, weightCode) +
		record("complexity.questions", questions, weightQuestions) +
		record("complexity.depth", depth, weightDepth) +
		record("complexity.vocabulary", vocabulary, weightVocabulary)

	return clamp01(total)
}

// --- token scanning helpers (fast substitutes for keyword regexes) ---

func isWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// countToken counts occurrences of tok in s up to max. Matches must start
// at a word boundary; when wholeWord is set the match must also end at one
// (prefix tokens like "optimi" leave the right side open).
func countToken(s, tok string, wholeWord bool, max int) int {
	n, i := 0, 0
	for n < max {
		j := strings.Index(s[i:], tok)
		if j < 0 {
			break
		}
		j += i
		end := j + len(tok)
		leftOK := j == 0 || !isWordByte(s[j-1])
		rightOK := !wholeWord || end >= len(s) || !isWordByte(s[end])
		if leftOK && rightOK {
			n++
		}
		i = end
	}
	return n
}

// countWords counts whole-word hits across words, capped at max.
func countWords(s string, words []string, max int) int {
	n := 0
	for _, w := range words {
		n += countToken(s, w, true, max-n)
		if n >= max {
			return max
		}
	}
	return n
}

// countPhrases counts substring hits across phrases, capped at max.
func countPhrases(s string, phrases []string, max int) int {
	n := 0
	for _, p := range phrases {
		n += strings.Count(s, p)
		if n >= max {
			return max
		}
	}
	return n
}

// countTokens combines word-prefix and phrase hits, capped at max.
func countTokens(s string, prefixes, phrases []string, max int) int {
	n := 0
	for _, p := range prefixes {
		n += countToken(s, p, false, max-n)
		if n >= max {
			return max
		}
	}
	return n + countPhrases(s, phrases, max-n)
}

func containsAny(s string, tokens []string) bool {
	for _, t := range tokens {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

// hasGreetingPrefix reports whether the (lowercased) message opens with a
// conversational greeting followed by a word boundary, so "hi" matches
// but "hire" does not.
func hasGreetingPrefix(lower string) bool {
	trimmed := strings.TrimLeft(lower, " \t\n")
	for _, g := range greetingPrefixes {
		if strings.HasPrefix(trimmed, g) &&
			(len(trimmed) == len(g) || !isWordByte(trimmed[len(g)])) {
			return true
		}
	}
	return false
}

// codeSizeSignal returns 0 (no code), 0.5 (small snippet or stack trace),
// or 1.0 (a >=30-line block, or multiple fenced blocks).
func codeSizeSignal(s string) float64 {
	parts := strings.Split(s, "```")
	blocks := len(parts) / 2 // fenced blocks are the odd-indexed segments
	if blocks == 0 {
		if reStackTrace.MatchString(s) {
			return 0.5
		}
		return 0
	}
	if blocks >= 2 {
		return 1.0
	}
	if strings.Count(parts[1], "\n") >= bigCodeLines {
		return 1.0
	}
	return 0.5
}

// digitOperatorDensity is the fraction of bytes that are digits or
// arithmetic operators.
func digitOperatorDensity(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	n := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			n++
		case c == '+' || c == '-' || c == '*' || c == '/' || c == '=' || c == '^' || c == '%' || c == '<' || c == '>':
			n++
		}
	}
	return float64(n) / float64(len(s))
}

// vocabularySignal blends average word length with the share of long
// (>=10 char) words as a cheap proxy for domain-specific vocabulary.
func vocabularySignal(s string) float64 {
	words := strings.Fields(s)
	if len(words) == 0 {
		return 0
	}
	totalLen, long := 0, 0
	for _, w := range words {
		totalLen += len(w)
		if len(w) >= 10 {
			long++
		}
	}
	avg := float64(totalLen) / float64(len(words))
	longRatio := float64(long) / float64(len(words))
	return 0.5*clamp01((avg-3)/6) + 0.5*clamp01(longRatio/0.2)
}

// lastUserMessage returns the content of the most recent user message,
// or the last message of any role when no user message exists.
func lastUserMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	if len(messages) > 0 {
		return messages[len(messages)-1].Content
	}
	return ""
}

func historyHasFence(messages []Message) bool {
	for i := range messages {
		if strings.Contains(messages[i].Content, "```") {
			return true
		}
	}
	return false
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
