// bragent: open-source AdCP Sponsored Intelligence brand agent.
//
// Single binary. Reads a product feed, exposes an MCP server over HTTP that
// answers AdCP capability discovery and SI tasks (M1: si_get_offering;
// M2: si_initiate_session + SQLite session store + --simulate-host).
//
// Spec status as of 2026-06-10: sponsored_intelligence.core is experimental
// in AdCP 3.0 — schemas may shift with 6 weeks' notice. Types in internal/si
// reflect best-effort interpretation against the published example flow.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kapoost/bragent/internal/admin"
	"github.com/kapoost/bragent/internal/brand"
	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/llm"
	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/si"
	"github.com/kapoost/bragent/internal/signing"
	"github.com/kapoost/bragent/internal/store"
	"github.com/kapoost/bragent/internal/wellknown"
)

// Version is overridable at link time via `-ldflags "-X main.Version=..."`.
// Default tracks the latest tagged release so `go install`-ed binaries report
// something sensible without ldflags wiring.
var Version = "0.1.0"

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML configuration file")
	simulateHost := flag.Bool("simulate-host", false, "after boot, issue a localhost si_initiate_session against ourselves and log the wire response, then exit. Useful for CI smoke and offline testing when no real SI host is available.")
	showVersion := flag.Bool("version", false, "print bragent version and exit")
	flag.Parse()

	if *showVersion {
		os.Stdout.WriteString("bragent " + Version + "\n")
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	catalog, err := feed.New(cfg.Feed)
	if err != nil {
		log.Fatalf("feed: %v", err)
	}

	st, err := store.Open(cfg.Store.Path)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go catalog.RefreshLoop(ctx)

	// Provider selection is config-gated: empty [llm] endpoint keeps the
	// deterministic Mock (offline-safe, used by CI smoke), a non-empty
	// endpoint switches to the OpenAI-compatible HTTP provider. The wire
	// shape is the same /v1/chat/completions used by Ollama, llama.cpp,
	// vLLM, and OpenAI itself.
	var provider llm.Provider
	providerName := "mock"
	if cfg.LLM.Endpoint != "" {
		provider = llm.NewOpenAI(cfg.LLM.Endpoint, cfg.LLM.APIKey, cfg.LLM.Model)
		providerName = "openai:" + cfg.LLM.Endpoint
	} else {
		provider = llm.NewMock()
	}

	handlers := si.NewHandlers(cfg, catalog, st, provider)
	wk := wellknown.New(cfg)

	// brand-rights signing (M6.1) is opt-in: when [brand].signing_key_path
	// is set, mint or load the Ed25519 keypair, wire the brand handler
	// onto SI handlers, publish the public key via JWKS. Failure to load
	// is fatal — operators who configured signing meant it.
	brandState := "off"
	if cfg.Brand.SigningKeyPath != "" {
		signer, err := signing.LoadOrCreate(cfg.Brand.SigningKeyPath)
		if err != nil {
			log.Fatalf("brand signing key: %v", err)
		}
		handlers.WithBrand(brand.NewHandler(cfg.Brand, signer, nil))
		wk.WithSigner(signer)
		brandState = "kid=" + signer.KeyID()
	}

	server := mcp.NewServer(cfg.Server, handlers, wk)

	adminState := "off"
	if cfg.Admin.Enabled {
		server.WithAdmin(admin.New(cfg.Admin.Token, catalog, handlers, cfg.Brand.Name))
		adminState = "on"
	}

	log.Printf("bragent listening listen=%s brand=%q domain=%s products=%d store=%s llm=%s admin=%s brand_rights=%s",
		cfg.Server.Listen, cfg.Brand.Name, cfg.Brand.Domain, catalog.Size(), cfg.Store.Path, providerName, adminState, brandState)

	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx) }()

	if *simulateHost {
		// Hand-rolled host loopback: wait for the listener to come up, send a
		// single si_initiate_session over MCP, log the response, then cancel.
		// Stays in-process so the same binary doubles as integration harness.
		go runSimulateHost(ctx, cfg.Server.Listen, cancel)
	}

	if err := <-errCh; err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}

// runSimulateHost issues a single si_initiate_session against the local
// MCP listener, prints the wire response, and triggers cancel() to shut
// the server down. Pure stdlib so the smoke loop doesn't drag in test deps.
func runSimulateHost(ctx context.Context, listen string, cancel context.CancelFunc) {
	defer cancel()

	addr := listen
	if strings.HasPrefix(addr, ":") {
		addr = "http://127.0.0.1" + addr
	} else if !strings.HasPrefix(addr, "http") {
		addr = "http://" + addr
	}
	url := addr + "/mcp"

	// Poll readiness on /.well-known/healthz so we don't race the listener.
	deadline := time.Now().Add(5 * time.Second)
	healthz := strings.TrimSuffix(addr, "/") + "/.well-known/healthz"
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		resp, err := http.Get(healthz)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "si_initiate_session",
		"params": si.InitiateSessionRequest{
			Intent: "I'm looking for a lightweight 2-person tent for a weekend trip.",
			Identity: &si.Identity{
				ConsentGranted: true,
				UserPseudoID:   "simulate-host-user-001",
				UserLanguage:   "en",
			},
			OfferingID: "tent-2p",
			Locale:     "en-US",
		},
	})

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[simulate-host] request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[simulate-host] status=%d body=%s", resp.StatusCode, string(respBody))
}
