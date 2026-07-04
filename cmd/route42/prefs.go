package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/krugis/route42app/internal/config"
)

// configPrefs is a local alias for config.Prefs so the prefs subcommand
// reads cleanly without repeating the package qualifier everywhere.
type configPrefs = config.Prefs

// runPrefs dispatches the prefs subcommand: get, set.
//
//	route42 prefs get
//	route42 prefs set <field>=<value> ...     (e.g. priority=cheap fallback_depth=3)
//	route42 prefs set --json '{"priority":"fast",...}'
func runPrefs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("prefs: missing subcommand (get|set)")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "get":
		return prefsGet(rest)
	case "set":
		return prefsSet(rest)
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stderr, "usage: route42 prefs <get|set>")
		fmt.Fprintln(os.Stderr, "  get                       print current preferences as JSON")
		fmt.Fprintln(os.Stderr, "  set <field>=<value> ...   update one or more fields")
		fmt.Fprintln(os.Stderr, "  set --json '{...}'        replace all preferences from JSON")
		return nil
	default:
		return fmt.Errorf("prefs: unknown subcommand %q (use get|set)", sub)
	}
}

// prefsGet prints the stored preferences as pretty JSON.
func prefsGet(args []string) error {
	env, err := loadEnv(args)
	if err != nil {
		return err
	}
	defer env.close()

	prefs, err := env.store.GetPrefs()
	if err != nil {
		return fmt.Errorf("prefs get: %w", err)
	}
	return printJSON(prefs)
}

// prefsSet updates preferences. Fields are given as <name>=<value> pairs;
// "--json '{...}'" replaces the whole record. Unknown fields error.
func prefsSet(args []string) error {
	fs := flag.NewFlagSet("prefs set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonFlag := fs.String("json", "", "replace all preferences from this JSON document")
	if err := fs.Parse(args); err != nil {
		return err
	}

	env, err := loadEnv(nil)
	if err != nil {
		return err
	}
	defer env.close()

	if *jsonFlag != "" {
		var p configPrefs
		if err := json.Unmarshal([]byte(*jsonFlag), &p); err != nil {
			return fmt.Errorf("prefs set --json: %w", err)
		}
		if err := env.store.SetPrefs(p); err != nil {
			return fmt.Errorf("prefs set: %w", err)
		}
		return printJSON(p)
	}

	// Field-by-field update: load current, apply patches, save.
	prefs, err := env.store.GetPrefs()
	if err != nil {
		return fmt.Errorf("prefs set: %w", err)
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("prefs set: no fields given (use field=value ... or --json)")
	}
	for _, kv := range fs.Args() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("prefs set: %q is not field=value", kv)
		}
		if err := applyPrefField(&prefs, name, val); err != nil {
			return fmt.Errorf("prefs set %s: %w", name, err)
		}
	}
	if err := env.store.SetPrefs(prefs); err != nil {
		return fmt.Errorf("prefs set: %w", err)
	}
	return printJSON(prefs)
}

// applyPrefField sets a single preference field by name. Booleans accept
// true/false/1/0; numbers accept integers/floats; disallowed_models is a
// comma-separated list (clear with "disallowed_models=").
func applyPrefField(p *configPrefs, name, val string) error {
	switch name {
	case "priority":
		switch val {
		case "balanced", "fast", "cheap", "accurate":
			p.Priority = val
		default:
			return fmt.Errorf("%q is not a valid priority (balanced|fast|cheap|accurate)", val)
		}
	case "max_cost_cents":
		v, err := strconv.ParseFloat(val, 64)
		if err != nil || v < 0 {
			return fmt.Errorf("%q is not a non-negative number", val)
		}
		p.MaxCostCents = v
	case "latency_tolerance_ms":
		v, err := strconv.Atoi(val)
		if err != nil || v < 0 {
			return fmt.Errorf("%q is not a non-negative integer", val)
		}
		p.LatencyToleranceMs = v
	case "only_free":
		b, err := parseBool(val)
		if err != nil {
			return err
		}
		p.OnlyFree = b
	case "only_local":
		b, err := parseBool(val)
		if err != nil {
			return err
		}
		p.OnlyLocal = b
	case "max_response_tokens":
		v, err := strconv.Atoi(val)
		if err != nil || v < 0 {
			return fmt.Errorf("%q is not a non-negative integer", val)
		}
		p.MaxResponseTokens = v
	case "default_model":
		p.DefaultModel = val
	case "fallback_depth":
		v, err := strconv.Atoi(val)
		if err != nil || v < 0 {
			return fmt.Errorf("%q is not a non-negative integer", val)
		}
		p.FallbackDepth = v
	case "disallowed_models":
		if val == "" {
			p.DisallowedModels = nil
		} else {
			parts := strings.Split(val, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			p.DisallowedModels = parts
		}
	default:
		return fmt.Errorf("unknown field %q", name)
	}
	return nil
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%q is not a boolean", s)
	}
}

// printJSON pretty-prints v to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
