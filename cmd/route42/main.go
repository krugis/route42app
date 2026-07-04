// Command route42 is the Route42 Community Edition binary: a local-first
// LLM router exposing an OpenAI-compatible API on localhost.
//
// Usage:
//
//	route42 serve [--config path]            start the gateway (default)
//	route42 models list [--config path]      list available models
//	route42 keys add|list|remove ...         manage provider API keys
//	route42 prefs get|set ...                manage routing preferences
//	route42 analyze "<prompt>" [--config]    print the prompt analysis
//	route42 version                          print the version
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "version", "-v", "--version":
		fmt.Printf("route42 %s\n", version)
		return
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "route42:", err)
			os.Exit(1)
		}
	case "models":
		if err := runModels(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "route42:", err)
			os.Exit(1)
		}
	case "keys":
		if err := runKeys(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "route42:", err)
			os.Exit(1)
		}
	case "prefs":
		if err := runPrefs(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "route42:", err)
			os.Exit(1)
		}
	case "analyze":
		if err := runAnalyze(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "route42:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "route42: unknown command %q\n", cmd)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "usage: route42 <command> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  serve                start the gateway (default)")
	fmt.Fprintln(w, "  models list          list available models (catalog + local Ollama)")
	fmt.Fprintln(w, "  keys add|list|remove manage provider API keys (stored encrypted)")
	fmt.Fprintln(w, "  prefs get|set        view or set routing preferences")
	fmt.Fprintln(w, "  analyze \"<prompt>\"  print the prompt analysis (complexity + category)")
	fmt.Fprintln(w, "  version              print the version")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "global flags:")
	fmt.Fprintln(w, "  --config <path>      path to route42.yaml (default: ./route42.yaml)")
}
