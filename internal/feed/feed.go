// Package feed loads, caches, and queries a brand's product feed.
//
// Supports http(s):// and file:// URLs for the source. JSON is the only
// format in M1 (array of Product objects). Periodic refresh runs in a
// goroutine started by main; reads are RWMutex-guarded so concurrent
// si_get_offering calls don't block each other against a writer.
package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kapoost/bragent/internal/config"
)

type Product struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Price       float64           `json:"price,omitempty"`
	Currency    string            `json:"currency,omitempty"`
	URL         string            `json:"url,omitempty"`
	Available   bool              `json:"available"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Catalog struct {
	cfg      config.Feed
	interval time.Duration

	mu        sync.RWMutex
	products  map[string]Product
	fetchedAt time.Time
}

func New(cfg config.Feed) (*Catalog, error) {
	interval, err := time.ParseDuration(cfg.RefreshInterval)
	if err != nil {
		return nil, fmt.Errorf("parse feed.refresh_interval %q: %w", cfg.RefreshInterval, err)
	}
	c := &Catalog{
		cfg:      cfg,
		interval: interval,
		products: map[string]Product{},
	}
	// Try a live refresh first. If it fails but a cache exists, boot in
	// degraded mode (stale catalog) so the agent doesn't go dark for a
	// transient upstream blip.
	if err := c.refresh(context.Background()); err != nil {
		if loadErr := c.loadFromCache(); loadErr != nil {
			return nil, fmt.Errorf("initial refresh failed and no usable cache: refresh=%v cache=%v", err, loadErr)
		}
	}
	return c, nil
}

func (c *Catalog) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.products)
}

func (c *Catalog) RefreshLoop(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.refresh(ctx); err != nil {
				// Stay on stale data — last-good cache continues serving.
				// One log line per failure is enough; operators can tail.
				fmt.Fprintf(os.Stderr, "feed refresh failed: %v\n", err)
			}
		}
	}
}

func (c *Catalog) refresh(ctx context.Context) error {
	body, err := fetch(ctx, c.cfg.URL)
	if err != nil {
		return err
	}
	products, err := decode(body, c.cfg.Format)
	if err != nil {
		return err
	}

	next := make(map[string]Product, len(products))
	for _, p := range products {
		next[p.ID] = p
	}

	c.mu.Lock()
	c.products = next
	c.fetchedAt = time.Now()
	c.mu.Unlock()

	if c.cfg.CachePath != "" {
		_ = writeCache(c.cfg.CachePath, body)
	}
	return nil
}

func (c *Catalog) loadFromCache() error {
	if c.cfg.CachePath == "" {
		return fmt.Errorf("no cache_path configured")
	}
	b, err := os.ReadFile(c.cfg.CachePath)
	if err != nil {
		return err
	}
	products, err := decode(b, c.cfg.Format)
	if err != nil {
		return err
	}
	c.mu.Lock()
	for _, p := range products {
		c.products[p.ID] = p
	}
	c.mu.Unlock()
	return nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	if strings.HasPrefix(url, "file://") {
		return os.ReadFile(strings.TrimPrefix(url, "file://"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("feed status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func writeCache(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func decode(body []byte, format string) ([]Product, error) {
	switch strings.ToLower(format) {
	case "", "json":
		var products []Product
		if err := json.Unmarshal(body, &products); err != nil {
			return nil, fmt.Errorf("decode json feed: %w", err)
		}
		return products, nil
	default:
		return nil, fmt.Errorf("unsupported feed format %q", format)
	}
}

// Search returns up to `limit` products whose name/description/tags contain
// ALL whitespace-separated terms in query (case-insensitive AND). Empty
// query returns the first `limit` products by lexicographic ID — sufficient
// for "browse" intent and keeps the response deterministic for tests.
func (c *Catalog) Search(query string, limit int) []Product {
	if limit <= 0 {
		limit = 10
	}
	terms := tokenize(strings.ToLower(query))

	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]string, 0, len(c.products))
	for id, p := range c.products {
		if matches(p, terms) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]Product, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.products[id])
	}
	return out
}

func tokenize(s string) []string {
	var out []string
	for _, t := range strings.Fields(s) {
		if len(t) >= 2 {
			out = append(out, t)
		}
	}
	return out
}

func matches(p Product, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	hay := strings.ToLower(p.Name + " " + p.Description + " " + strings.Join(p.Tags, " "))
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}
