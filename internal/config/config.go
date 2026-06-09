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
}

type Brand struct {
	Name    string `toml:"name"`
	Domain  string `toml:"domain"`
	LogoURL string `toml:"logo_url"`
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
	return nil
}
