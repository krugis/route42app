package analyzer

import (
	"context"
	"strings"
	"testing"
	"time"
)

func analyze(t *testing.T, msgs ...Message) AnalysisResult {
	t.Helper()
	res, err := NewHeuristic().Analyze(context.Background(), msgs)
	if err != nil {
		t.Fatalf("heuristic must never error, got: %v", err)
	}
	return res
}

func user(content string) Message { return Message{Role: "user", Content: content} }

// complexDesignPrompt is the reference "hard task" fixture: long,
// multi-constraint, reasoning-heavy, multi-question.
const complexDesignPrompt = `Design a distributed rate limiter that can handle millions of requests per second across regions. Walk through it step by step and justify every trade-off.

Requirements:
1. The system must guarantee fairness across tenants and handle bursty traffic.
2. It must support sliding-window and token-bucket algorithms; consider memory usage carefully.
3. Ensure graceful degradation during network partitions and handle clock skew.
4. It must support horizontal scaling; consider consensus overhead.
5. Ensure observability: expose saturation metrics and handle backpressure signals.

How would you architect the coordination layer? What data structures would you use, and also how do you optimize tail latency? Additionally, how would you prove correctness under partition?`

func TestCategoryDetection(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{"greeting", "hi", CategoryChat},
		{"tiny arithmetic", "what's 2+2?", CategoryChat},
		{"casual question", "can you tell me a fun fact?", CategoryChat},
		{"fenced code", "Why does this fail?\n```go\nfunc main() { fmt.Println(1) }\n```", CategoryCode},
		{"stack trace", "I get this error:\nTraceback (most recent call last)\n  File \"app.py\", line 10", CategoryCode},
		{"code verbs and file", "refactor utils.py to remove duplication", CategoryCode},
		{"latex equation", `Solve the equation \frac{x}{2} + 3 = 7`, CategoryMath},
		{"math words", "Calculate the probability of rolling two sixes, then prove the general theorem.", CategoryMath},
		{"comparison", "Compare PostgreSQL and MySQL: pros and cons for a startup backend.", CategoryAnalysis},
		{"summarize", "Summarize the key arguments of this article and evaluate the evidence quality.", CategoryAnalysis},
		{"no signals", "The weather in tropical regions influences agricultural planning throughout the year and impacts crop rotation schedules significantly for farmers.", CategoryGeneral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := analyze(t, user(tc.prompt))
			if res.Category != tc.want {
				t.Errorf("category = %q, want %q (signals: %v)", res.Category, tc.want, res.Signals)
			}
		})
	}
}

func TestCodeInHistoryMarksCodeSession(t *testing.T) {
	res := analyze(t,
		user("```python\ndef f():\n    return 1\n```"),
		Message{Role: "assistant", Content: "That defines a function."},
		user("now make it recursive"),
	)
	if res.Category != CategoryCode {
		t.Errorf("category = %q, want code (fences earlier in history)", res.Category)
	}
}

func TestComplexityTrivial(t *testing.T) {
	for _, prompt := range []string{"hi", "what's 2+2?", "thanks!"} {
		res := analyze(t, user(prompt))
		if res.Complexity >= 0.2 {
			t.Errorf("complexity(%q) = %.3f, want < 0.2 (signals: %v)", prompt, res.Complexity, res.Signals)
		}
	}
}

func TestComplexityComplexDesign(t *testing.T) {
	res := analyze(t, user(complexDesignPrompt))
	if res.Complexity <= 0.6 {
		t.Errorf("complexity = %.3f, want > 0.6 (signals: %v)", res.Complexity, res.Signals)
	}
}

func TestComplexityMonotonicOnCode(t *testing.T) {
	small := analyze(t, user("Fix this:\n```go\nfmt.Println(x)\n```"))
	big := analyze(t, user("Fix this:\n```go\n"+strings.Repeat("x := compute()\n", 40)+"```"))
	if big.Complexity <= small.Complexity {
		t.Errorf("large code block (%.3f) must score above small snippet (%.3f)", big.Complexity, small.Complexity)
	}
}

func TestConversationDepthRaisesComplexity(t *testing.T) {
	shallow := analyze(t, user("continue"))
	msgs := make([]Message, 0, 13)
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, Message{Role: role, Content: "context turn"})
	}
	msgs = append(msgs, user("continue"))
	deep := analyze(t, msgs...)
	if deep.Complexity <= shallow.Complexity {
		t.Errorf("deep conversation (%.3f) must score above first turn (%.3f)", deep.Complexity, shallow.Complexity)
	}
}

func TestEmptyAndWhitespaceInput(t *testing.T) {
	for _, msgs := range [][]Message{nil, {}, {user("")}, {user("   \n\t ")}} {
		res, err := NewHeuristic().Analyze(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		if res.Category != CategoryGeneral || res.Complexity != 0 {
			t.Errorf("empty input => (%q, %.3f), want (general, 0)", res.Category, res.Complexity)
		}
		if res.Analyzer != NameHeuristic {
			t.Errorf("analyzer = %q, want heuristic", res.Analyzer)
		}
	}
}

func TestSignalsAreReported(t *testing.T) {
	res := analyze(t, user(complexDesignPrompt))
	for _, key := range []string{"complexity.length", "complexity.requirements", "complexity.reasoning", "complexity.questions"} {
		if res.Signals[key] <= 0 {
			t.Errorf("signal %q missing or zero: %v", key, res.Signals)
		}
	}
}

func TestHugeInputStaysFast(t *testing.T) {
	huge := strings.Repeat("Consider the trade-offs of this design. Must it handle 10k QPS? ", 1600) // ~100KB
	start := time.Now()
	res := analyze(t, user(huge))
	elapsed := time.Since(start)
	// Generous CI-safe bound; the benchmark below tracks the real budget.
	// Skipped under -race: instrumentation overhead makes wall-clock
	// assertions meaningless on shared runners.
	if !raceEnabled && elapsed > 100*time.Millisecond {
		t.Errorf("100KB analysis took %v, want well under 100ms", elapsed)
	}
	if res.Complexity <= 0.3 {
		t.Errorf("huge constraint-dense input scored %.3f", res.Complexity)
	}
}

func BenchmarkHeuristicComplex(b *testing.B) {
	h := NewHeuristic()
	msgs := []Message{user(complexDesignPrompt)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := h.Analyze(context.Background(), msgs); err != nil {
			b.Fatal(err)
		}
	}
}
