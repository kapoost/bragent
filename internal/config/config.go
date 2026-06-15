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
	// verify_brand_claim signed_response envelope (M6.1) AND the M6.3
	// receipt-notarisation JWS. One key per brand identity — single
	// trust story for any AdCP-side signature this brand emits. Empty
	// disables both surfaces.
	SigningKeyPath string `toml:"signing_key_path"`
	// Disclosure (M6.3) is the default disclosure_obligation bragent
	// stamps onto every emitted sponsored_context envelope. When the
	// section is omitted the defaults applied are: required=false,
	// label_text="Sponsored by <Brand.Name>", timing=at_first_influenced_output,
	// proximity=near_influenced_output, jurisdictions=[]. Operators opt
	// into legal exposure by listing jurisdictions explicitly — we do
	// not ship FTC/DSA codes as defaults.
	Disclosure DisclosureConfig `toml:"disclosure"`
}

// DisclosureConfig is the TOML shape for [brand.disclosure]. Mirrors
// si.DisclosureObligation with strings only (no nested struct types in
// TOML) — the SI handler builds the runtime struct from this on every
// sponsored_context emission.
type DisclosureConfig struct {
	Required      bool                          `toml:"required"`
	LabelText     string                        `toml:"label_text"`
	Timing        string                        `toml:"timing"`
	Proximity     string                        `toml:"proximity"`
	Jurisdictions []DisclosureJurisdictionConfig `toml:"jurisdictions"`
}

type DisclosureJurisdictionConfig struct {
	Country    string `toml:"country"`
	Region     string `toml:"region"`
	Regulation string `toml:"regulation"`
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
	c.applyEnvOverrides()
	if err := c.applyDefaultsAndValidate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyEnvOverrides lets deploy environments (Fly, K8s, plain systemd)
// override sensitive or environment-specific TOML values without baking
// them into the config file shipped with the artifact. The envelope is
// the same shape as TOML keys, prefixed with BRAGENT_ and joined by
// underscores. Set-but-empty env vars are treated as "not set" — this
// avoids accidentally blanking a TOML value when a CI runner exports
// an empty string. Mainly for secrets (LLM api_key, admin token) and
// for the per-deploy brand domain.
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("BRAGENT_BRAND_NAME"); v != "" {
		c.Brand.Name = v
	}
	if v := os.Getenv("BRAGENT_BRAND_DOMAIN"); v != "" {
		c.Brand.Domain = v
	}
	if v := os.Getenv("BRAGENT_BRAND_SIGNING_KEY_PATH"); v != "" {
		c.Brand.SigningKeyPath = v
	}
	if v := os.Getenv("BRAGENT_SERVER_LISTEN"); v != "" {
		c.Server.Listen = v
	}
	if v := os.Getenv("BRAGENT_FEED_URL"); v != "" {
		c.Feed.URL = v
	}
	if v := os.Getenv("BRAGENT_LLM_ENDPOINT"); v != "" {
		c.LLM.Endpoint = v
	}
	if v := os.Getenv("BRAGENT_LLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
	if v := os.Getenv("BRAGENT_LLM_MODEL"); v != "" {
		c.LLM.Model = v
	}
	if v := os.Getenv("BRAGENT_STORE_PATH"); v != "" {
		c.Store.Path = v
	}
	if v := os.Getenv("BRAGENT_ADMIN_TOKEN"); v != "" {
		c.Admin.Token = v
		// Setting the token via env implies the operator wants admin
		// on. The validation pass still gates: empty token after this
		// block silently disables admin.
		c.Admin.Enabled = true
	}
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
	// M6.3 disclosure defaults — operator can override any field in
	// [brand.disclosure]; absence means "ship the sensible default" and
	// not "ship an empty string". We don't validate timing/proximity
	// against the spec enum here — the SI handler does that on emission
	// so config typos surface at boot via the smoke loop rather than
	// silently producing invalid envelopes.
	if c.Brand.Disclosure.LabelText == "" {
		c.Brand.Disclosure.LabelText = "Sponsored by " + c.Brand.Name
	}
	if c.Brand.Disclosure.Timing == "" {
		c.Brand.Disclosure.Timing = "at_first_influenced_output"
	}
	if c.Brand.Disclosure.Proximity == "" {
		c.Brand.Disclosure.Proximity = "near_influenced_output"
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
