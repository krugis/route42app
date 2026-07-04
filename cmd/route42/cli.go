package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/krugis/route42app/internal/config"
	"github.com/krugis/route42app/internal/store"
)

// env holds the shared runtime dependencies a subcommand needs: the
// effective config, an opened store (with prefs seeded), and a logger.
// Subcommands close the store themselves via defer env.close().
type env struct {
	cfg    *config.Config
	store  *store.Store
	logger *slog.Logger
}

func (e *env) close() {
	if e.store != nil {
		_ = e.store.Close()
	}
}

// loadEnv loads config from the given --config path ("" = implicit
// route42.yaml or zero-config defaults), opens the store, and seeds
// preferences on first run. The returned env owns the store handle.
func loadEnv(args []string) (*env, error) {
	fs := flag.NewFlagSet("route42", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "path to route42.yaml (default: route42.yaml in the working directory)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return openEnv(*configPath)
}

// openEnv is the config+store setup shared by serve and the CLI
// subcommands. It mirrors the M6 runServe setup so behavior is identical.
func openEnv(configPath string) (*env, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	slog.SetLogLoggerLevel(slog.LevelInfo)
	logger := slog.Default()

	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if err := st.EnsurePrefs(cfg.Prefs); err != nil {
		st.Close()
		return nil, fmt.Errorf("seed prefs: %w", err)
	}
	return &env{cfg: cfg, store: st, logger: logger}, nil
}
