package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/krugis/route42app/internal/analyzer"
	"github.com/krugis/route42app/internal/config"
)

// runAnalyze runs the configured analyzer on a prompt and prints the
// AnalysisResult (complexity, category, per-signal contributions) as JSON.
// This is the showcase demo/debug command: it shows exactly why Route42
// would route a given prompt the way it does.
//
//	route42 analyze "explain quantum computing to a 10-year-old"
//	route42 analyze "def fib(n): ..." --config route42.yaml
func runAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "path to route42.yaml (default: route42.yaml in the working directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := fs.Arg(0)
	if prompt == "" {
		return fmt.Errorf("analyze: usage: route42 analyze \"<prompt>\"")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	pa, err := analyzer.New(cfg)
	if err != nil {
		return fmt.Errorf("analyze: build analyzer: %w", err)
	}

	// Analyze as a single user message. The analyzer weights the last user
	// message most heavily, matching the chat path.
	result, err := pa.Analyze(context.Background(), []analyzer.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return printJSON(result)
}
