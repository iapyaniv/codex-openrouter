package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var options packageOptions
	flag.StringVar(&options.version, "version", "", "explicit candidate version, for example 0.2.0-local")
	flag.StringVar(&options.commit, "source-commit", "", "full commit at clean source HEAD")
	flag.StringVar(&options.output, "out", "", "new absolute output directory outside source")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := packageCandidate(ctx, options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(options.output)
}
