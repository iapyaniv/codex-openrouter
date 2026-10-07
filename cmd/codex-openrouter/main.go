package main

import (
	"os"

	"codex-openrouter/internal/launcher"
)

func main() {
	os.Exit(launcher.Run(os.Args[1:], os.Stdout, os.Stderr))
}
