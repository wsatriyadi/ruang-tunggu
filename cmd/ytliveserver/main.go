// Command ytliveserver runs the YouTube Live Server: HTTP API, media pipeline,
// scheduler and publisher worker in a single process.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/wisnu/ylivemasadi/internal/app"
	"github.com/wisnu/ylivemasadi/internal/config"
)

// version is stamped at build time via -ldflags "-X main.version=…".
// A development build reports "dev" so it is obvious in logs which code ran.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Printf("ytliveserver %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ytliveserver: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}
	instance, err := app.New(cfg, version)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return instance.Run(ctx)
}
