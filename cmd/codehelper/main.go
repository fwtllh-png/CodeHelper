package main

import (
	"context"
	"os"
	"os/signal"

	webhost "github.com/fwtllh-png/CodeHelper/internal/host/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(webhost.RunContext(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
