// Package config loads and validates the bragent TOML configuration.
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Brand  Brand  `toml:"brand"`
	Server Server `toml:"server"`
	Feed   Feed   `toml:"feed"`
	LLM    LLM    `toml:"llm"`
	Store  Store  `toml:"store"`
	Admin  Admin  `toml:"admin"`
}

// Admin gates the optional /admin/ surface: a small embedded web UI for
// CRUD on a file:// product feed plus an in-process chat panel against
// the brand agent's own SI handlers. Off by default — production
// deployments opt in by setting enabled=true and a token. The token is
// required when enabled is true; an empty token disables admin even if
// enabled was flipped, so a forgotten config doesn't expose CRUD.
type Admin struct {
	Enabled bool   `toml:"enabled"`
	Token   string `toml:"token"`
}

// Store points at the SQLite session-state database. Empty path or
// ":memory:" runs ephemerally — useful for tests and --simulate-host
// smoke loops. File paths persist sessions across restarts.
type Store struct {
	Path string `toml:"path"`
}

type Brand struct {
	Name    string `toml:"name"`
	Domain  string `toml:"domain"`
	LogoURL string `toml:"logo_url"`
	// SigningKeyPath is the on-disk Ed25519 keypair backing the
	// verify_brand_claim signed_response envelope (M6.1). When empty the
	// brand surface refuses to mint signed responses; verify_brand_claim
	// returns an internal error so the operator sees the missing wire
	// up. First boot with a populated path mints a fresh key.
	SigningKeyPath string `toml:"signing_key_path"`
	// PayingPrincipal is the URL identifying who economically funds this
	// agent's inference — the "who pays for the tokens" disclosure
	// primitive (M6.2). Surfaced verbatim in /.well-known/brand.json and
	// in get_adcp_capabilities so hosts (and downstream users) can render
	// a "you are talking to a representative of X" trust badge. Defaults
	// to https://<brand.domain> when empty.
	PayingPrincipal string `toml:"paying_principal"`
}

type Server struct {
	Listen string `toml:"listen"`
}

type Feed struct {
	URL             string `toml:"url"`
	Format          string `toml:"format"`
	CachePath       string `toml:"cache_path"`
	RefreshInterval string `toml:"refresh_interval"`
}

// LLM is the OpenAI-compatible endpoint used by si_send_message. Optional —
// si_get_offering does not call the model. M1 ships without LLM wiring.
type LLM struct {
	Endpoint string `toml:"endpoint"`
	APIKey   string `toml:"api_key"`
	Model    string `toml:"model"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if err := c.applyDefaultsAndValidate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaultsAndValidate() error {
	if c.Brand.Name == "" {
		return fmt.Errorf("brand.name required")
	}
	if c.Brand.Domain == "" {
		return fmt.Errorf("brand.domain required")
	}
	if c.Feed.URL == "" {
		return fmt.Errorf("feed.url required")
	}
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Feed.Format == "" {
		c.Feed.Format = "json"
	}
	if c.Feed.RefreshInterval == "" {
		c.Feed.RefreshInterval = "30m"
	}
	if c.Store.Path == "" {
		c.Store.Path = ".cache/bragent.db"
	}
	// Default paying_principal to https://<brand.domain> — the canonical
	// case is that the brand pays for its own agent's inference. Operators
	// who run a third-party SI surface override this with the actual
	// economic principal's URL (e.g., a hosting agency).
	if c.Brand.PayingPrincipal == "" && c.Brand.Domain != "" {
		c.Brand.PayingPrincipal = "https://" + c.Brand.Domain
	}
	// signing_key_path stays empty by default — operators opt in to
	// brand-rights signing by setting it. Empty → verify_brand_claim is
	// not registered in capabilities and the MCP method returns
	// method_not_found.
	// Admin without a token is unsafe — silently disable rather than ship
	// an open CRUD endpoint. Operators see this in the boot log.
	if c.Admin.Enabled && c.Admin.Token == "" {
		c.Admin.Enabled = false
	}
	return nil
}
