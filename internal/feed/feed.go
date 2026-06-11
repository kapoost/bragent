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
	"errors"
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

// ErrFeedReadOnly signals that the configured feed source is remote
// (http(s)://) and therefore cannot be mutated in place. Admin CRUD only
// works against file:// feeds; remote feeds remain authoritative and the
// admin UI surfaces this as a 409.
var ErrFeedReadOnly = errors.New("feed source is read-only (not a file:// URL)")

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

// All returns a snapshot of every product in lexicographic ID order.
// Callers receive a fresh slice; safe to mutate without locking.
func (c *Catalog) All() []Product {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.products))
	for id := range c.products {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Product, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.products[id])
	}
	return out
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

// Upsert inserts or replaces a product by ID, then persists the full
// catalog snapshot to the file feed atomically. Returns ErrFeedReadOnly
// when the configured source is not a file:// URL — remote feeds stay
// authoritative and the admin UI is expected to translate this to a 409.
func (c *Catalog) Upsert(p Product) error {
	if p.ID == "" {
		return errors.New("product.id required")
	}
	path, ok := c.filePath()
	if !ok {
		return ErrFeedReadOnly
	}
	c.mu.Lock()
	c.products[p.ID] = p
	snap := c.snapshotLocked()
	c.mu.Unlock()
	return writeFeedAtomic(path, snap)
}

// Delete removes a product by ID and persists the catalog. The bool
// return tells the caller whether the ID was present before deletion —
// useful for the admin UI to distinguish 200 vs 404.
func (c *Catalog) Delete(id string) (bool, error) {
	path, ok := c.filePath()
	if !ok {
		return false, ErrFeedReadOnly
	}
	c.mu.Lock()
	_, existed := c.products[id]
	delete(c.products, id)
	snap := c.snapshotLocked()
	c.mu.Unlock()
	if err := writeFeedAtomic(path, snap); err != nil {
		return existed, err
	}
	return existed, nil
}

// Writable reports whether the feed source can be mutated (file:// URL).
// The admin UI uses this to disable the add/edit form when running
// against a remote catalog.
func (c *Catalog) Writable() bool {
	_, ok := c.filePath()
	return ok
}

func (c *Catalog) filePath() (string, bool) {
	if !strings.HasPrefix(c.cfg.URL, "file://") {
		return "", false
	}
	return strings.TrimPrefix(c.cfg.URL, "file://"), true
}

func (c *Catalog) snapshotLocked() []Product {
	ids := make([]string, 0, len(c.products))
	for id := range c.products {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Product, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.products[id])
	}
	return out
}

// writeFeedAtomic serialises the catalog with stable key order and 2-space
// indent (matches the hand-edited feeds/example.json shape so diffs stay
// readable), writes to a sibling tempfile, then renames into place. Crash
// between write and rename leaves the original feed intact.
func writeFeedAtomic(path string, products []Product) error {
	body, err := json.MarshalIndent(products, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".feed-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
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
