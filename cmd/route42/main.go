// Command route42 is the Route42 Community Edition binary: a local-first
// LLM router exposing an OpenAI-compatible API on localhost.
//
// Usage:
//
//	route42 serve [--config path]   # start the gateway (default)
//	route42 version                 # print the version
//
// Other subcommands (models, keys, prefs, analyze) land in M7.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/krugis/route42app/internal/api"
	"github.com/krugis/route42app/internal/config"
	"github.com/krugis/route42app/internal/store"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "version":
		fmt.Printf("route42 %s\n", version)
		return
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "route42:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "route42: unknown command %q\n", cmd)
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: route42 <command> [flags]")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  serve    start the gateway (default)")
	fmt.Fprintln(os.Stderr, "  version  print the version")
}

// runServe loads config, opens the store, builds the API server, and runs
// it until interrupted. Zero-config first run creates the database and
// works Ollama-only with no keys.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to route42.yaml (default: route42.yaml in the working directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	slog.SetLogLoggerLevel(slog.LevelInfo)
	logger := slog.Default()

	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// Seed initial preferences on first run without overwriting any edits.
	if err := st.EnsurePrefs(cfg.Prefs); err != nil {
		return fmt.Errorf("seed prefs: %w", err)
	}

	srv, err := api.New(cfg, st, api.Options{Logger: logger})
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("route42 starting", "version", version, "port", cfg.Server.Port, "analyzer", cfg.Analyzer.Mode)
	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	logger.Info("route42 stopped")
	return nil
}
