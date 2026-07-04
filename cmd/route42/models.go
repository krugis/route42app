package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/llm"
)

// runModels dispatches the models subcommand. Currently only `list`.
//
//	route42 models list
func runModels(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("models: missing subcommand (list)")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return modelsList(rest)
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stderr, "usage: route42 models list")
		fmt.Fprintln(os.Stderr, "  list    list available models (catalog cloud models for configured providers + local Ollama)")
		return nil
	default:
		return fmt.Errorf("models: unknown subcommand %q (use list)", sub)
	}
}

// modelsList prints a table of routable models: catalog cloud models for
// providers with a configured key, plus discovered local Ollama models.
// A missing Ollama is reported but never fatal.
func modelsList(args []string) error {
	env, err := loadEnv(args)
	if err != nil {
		return err
	}
	defer env.close()

	cat, err := catalog.Load()
	if err != nil {
		return fmt.Errorf("models list: load catalog: %w", err)
	}

	// Available cloud providers (stored + config-file keys).
	available := make(map[string]bool)
	for name, p := range env.cfg.Providers {
		if p.APIKey != "" {
			available[llm.CanonicalProvider(name)] = true
		}
	}
	if stored, err := env.store.ListProviders(); err == nil {
		for _, p := range stored {
			available[p] = true
		}
	}

	// Discover local Ollama models (non-fatal on failure).
	locals, derr := llm.DiscoverOllama(context.Background(), env.cfg.Ollama.BaseURL)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tMODEL\tSOURCE\tQUALITY\tTOOLS\tAVAIL\t$/M (in/out)")

	seen := make(map[string]bool)
	for _, m := range cat.Models {
		if m.Source == catalog.SourceLocal {
			continue
		}
		key := m.Provider + "/" + m.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		avail := "no"
		if available[llm.CanonicalProvider(m.Provider)] {
			avail = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.0f\t%v\t%s\t$%.4g/$%.4g\n",
			m.Provider, m.ID, m.Source, m.QualityScore, m.SupportsTools, avail,
			m.InputPricePerMTok, m.OutputPricePerMTok)
	}

	for _, d := range locals {
		key := "ollama/" + d.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(w, "%s\t%s\t%s\t-\t-\tyes\t$0/$0\n", "ollama", d.Name, catalog.SourceLocal)
	}

	if err := w.Flush(); err != nil {
		return err
	}
	if len(locals) == 0 && derr != nil {
		fmt.Fprintln(os.Stderr, "(ollama unreachable — no local models discovered)")
	} else {
		fmt.Fprintf(os.Stderr, "(%d cloud models, %d local models)\n", len(cat.Models)-countLocalCatalog(cat), len(locals))
	}
	return nil
}

// countLocalCatalog returns how many catalog entries are tagged local.
func countLocalCatalog(c *catalog.Catalog) int {
	n := 0
	for _, m := range c.Models {
		if m.Source == catalog.SourceLocal {
			n++
		}
	}
	return n
}
