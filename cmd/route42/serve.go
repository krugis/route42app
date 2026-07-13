package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/krugis/route42app/internal/api"
)

// runServe starts the gateway. It shares config+store setup with the CLI
// subcommands via loadEnv, then builds the api.Server and runs it until
// interrupted. Zero-config first run creates the database and works
// Ollama-only with no keys.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "path to route42.yaml (default: route42.yaml in the working directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	env, err := openEnv(*configPath)
	if err != nil {
		return err
	}
	defer env.close()

	srv, err := api.New(env.cfg, env.store, api.Options{Logger: env.logger, Version: version})
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	env.logger.Info("route42 starting", "version", version, "port", env.cfg.Server.Port, "analyzer", env.cfg.Analyzer.Mode)
	if env.cfg.Server.UI {
		env.logger.Info("web console enabled", "url", fmt.Sprintf("http://localhost:%d", env.cfg.Server.Port))
	}
	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	env.logger.Info("route42 stopped")
	return nil
}
