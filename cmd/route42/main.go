// Command route42 is the Route42 Community Edition binary: a local-first
// LLM router exposing an OpenAI-compatible API on localhost.
//
// Subcommands (serve, models, keys, prefs, analyze) are implemented in
// later milestones; for now the binary only reports its version.
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("route42 %s\n", version)
		return
	}
	fmt.Fprintln(os.Stderr, "route42: not yet implemented — see README.md")
	os.Exit(1)
}
