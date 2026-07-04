package analyzer

import (
	"fmt"
	"time"

	"github.com/krugis/route42app/internal/config"
)

// New builds the analyzer selected by the configuration. Unknown modes are
// rejected here as a second line of defense (config.Validate catches them
// first), so a misconfiguration fails at startup, never at request time.
func New(cfg *config.Config) (PromptAnalyzer, error) {
	switch cfg.Analyzer.Mode {
	case config.ModeHeuristic:
		return NewHeuristic(), nil
	case config.ModeLLM:
		timeout := time.Duration(cfg.Analyzer.LLM.TimeoutMs) * time.Millisecond
		return NewLLM(cfg.Ollama.BaseURL, cfg.Analyzer.LLM.Model, timeout, NewHeuristic()), nil
	default:
		return nil, fmt.Errorf("analyzer mode %q is not supported", cfg.Analyzer.Mode)
	}
}
