// bragent: open-source AdCP Sponsored Intelligence brand agent.
//
// Single binary. Reads a product feed, exposes an MCP server over HTTP that
// answers AdCP capability discovery and SI tasks (M1: si_get_offering).
//
// Spec status as of 2026-06-09: sponsored_intelligence.core is experimental
// in AdCP 3.0 — schemas may shift with 6 weeks' notice. Types in internal/si
// reflect best-effort interpretation against the published example flow.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/si"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	catalog, err := feed.New(cfg.Feed)
	if err != nil {
		log.Fatalf("feed: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go catalog.RefreshLoop(ctx)

	handlers := si.NewHandlers(cfg, catalog)
	server := mcp.NewServer(cfg.Server, handlers)

	log.Printf("bragent listening listen=%s brand=%q domain=%s products=%d",
		cfg.Server.Listen, cfg.Brand.Name, cfg.Brand.Domain, catalog.Size())

	if err := server.Run(ctx); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
