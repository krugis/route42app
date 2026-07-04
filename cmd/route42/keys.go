package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/krugis/route42app/internal/llm"
)

// runKeys dispatches the keys subcommand: add, list, remove.
//
//	route42 keys add <provider> <api-key>
//	route42 keys list
//	route42 keys remove <provider>
func runKeys(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("keys: missing subcommand (add|list|remove)")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "add":
		return keysAdd(rest)
	case "list":
		return keysList(rest)
	case "remove", "rm", "delete":
		return keysRemove(rest)
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stderr, "usage: route42 keys <add|list|remove>")
		fmt.Fprintln(os.Stderr, "  add <provider> <api-key>   store (or replace) a provider API key")
		fmt.Fprintln(os.Stderr, "  list                      list configured providers (keys masked)")
		fmt.Fprintln(os.Stderr, "  remove <provider>         delete a stored provider key")
		return nil
	default:
		return fmt.Errorf("keys: unknown subcommand %q (use add|list|remove)", sub)
	}
}

// keysAdd stores (or replaces) a provider API key.
func keysAdd(args []string) error {
	fs := flag.NewFlagSet("keys add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("keys add: usage: route42 keys add <provider> <api-key>")
	}
	provider, apiKey := rest[0], rest[1]

	env, err := loadEnv(nil)
	if err != nil {
		return err
	}
	defer env.close()

	if err := env.store.SetProviderKey(provider, apiKey); err != nil {
		return fmt.Errorf("keys add: %w", err)
	}
	fmt.Printf("stored key for provider %q\n", llm.CanonicalProvider(provider))
	return nil
}

// keysList prints configured providers with masked keys.
func keysList(args []string) error {
	env, err := loadEnv(args)
	if err != nil {
		return err
	}
	defer env.close()

	providers, err := env.store.ListProviders()
	if err != nil {
		return fmt.Errorf("keys list: %w", err)
	}
	// Include config-file providers too.
	seen := make(map[string]bool)
	for _, p := range providers {
		seen[p] = true
	}
	for name, p := range env.cfg.Providers {
		if p.APIKey == "" {
			continue
		}
		c := llm.CanonicalProvider(name)
		if !seen[c] {
			seen[c] = true
			providers = append(providers, c)
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tKEY")
	for prov := range seen {
		fmt.Fprintf(w, "%s\t%s\n", prov, maskKey(env.keyFor(prov)))
	}
	return w.Flush()
}

// keysRemove deletes a stored provider key.
func keysRemove(args []string) error {
	fs := flag.NewFlagSet("keys remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("keys remove: usage: route42 keys remove <provider>")
	}
	provider := rest[0]

	env, err := loadEnv(nil)
	if err != nil {
		return err
	}
	defer env.close()

	if err := env.store.DeleteProviderKey(provider); err != nil {
		return fmt.Errorf("keys remove: %w", err)
	}
	fmt.Printf("removed key for provider %q\n", llm.CanonicalProvider(provider))
	return nil
}

// keyFor resolves a key from the store (with config-file fallback), shared
// with the api package's masking logic.
func (e *env) keyFor(provider string) string {
	k, err := e.store.GetProviderKey(provider)
	if err != nil || k != "" {
		return k
	}
	if p, ok := e.cfg.Providers[provider]; ok {
		return p.APIKey
	}
	for name, p := range e.cfg.Providers {
		if llm.CanonicalProvider(name) == provider {
			return p.APIKey
		}
	}
	return ""
}

// maskKey renders a key as "prefix...suffix" for display. Empty keys return
// an empty mask.
func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return strings.Repeat("*", len(k))
	}
	return k[:3] + "..." + k[len(k)-4:]
}
