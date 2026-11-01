package usage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Prices come from models.dev, the public catalog OpenCode reads too: a
// model's price per million tokens of input, output, cache reads and cache
// writes, and the dearer prices some charge past a context length. Usage
// is costed as if paid through the API, an estimate: a subscription doesn't
// bill per token.

const (
	catalogURL     = "https://models.dev/api.json"
	catalogMaxAge  = 24 * time.Hour
	maxCatalogSize = 64 << 20
)

// Price is a model's price per million tokens.
type Price struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	// Above, when set, is the price past a prompt of AboveTokens.
	Above       *Price
	AboveTokens int64
}

type catalogPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Tiers      []struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cache_read"`
		CacheWrite float64 `json:"cache_write"`
		Tier       struct {
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tier"`
	} `json:"tiers"`
}

// Catalog is the prices of each provider's models: "anthropic", "openai",
// "google", by model id.
type Catalog map[string]map[string]Price

func parseCatalog(data []byte) (Catalog, error) {
	var raw map[string]struct {
		Models map[string]struct {
			Cost *catalogPrice `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := Catalog{}
	for provider, p := range raw {
		models := map[string]Price{}
		for id, m := range p.Models {
			if m.Cost == nil {
				continue
			}
			price := Price{Input: m.Cost.Input, Output: m.Cost.Output, CacheRead: m.Cost.CacheRead, CacheWrite: m.Cost.CacheWrite}
			for _, t := range m.Cost.Tiers {
				if t.Tier.Type == "context" && t.Tier.Size > 0 {
					price.Above = &Price{Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite}
					price.AboveTokens = t.Tier.Size
					break
				}
			}
			models[id] = price
		}
		out[provider] = models
	}
	return out, nil
}

// dated is a model id's date or snapshot suffix: "-20250929", "@20250929".
var dated = regexp.MustCompile(`[-@]\d{8}$`)

// Lookup finds a model's price: by its id, then without a date suffix, then
// by the longest id the model's own begins with ("claude-opus-5-5-thinking"
// is priced as claude-opus-5-5).
func (c Catalog) Lookup(provider, model string) (Price, bool) {
	models := c[provider]
	if len(models) == 0 || model == "" {
		return Price{}, false
	}
	model = strings.ToLower(model)
	if p, ok := models[model]; ok {
		return p, true
	}
	if p, ok := models[dated.ReplaceAllString(model, "")]; ok {
		return p, true
	}
	best, found := "", Price{}
	for id, p := range models {
		if strings.HasPrefix(model, id) && len(id) > len(best) {
			best, found = id, p
		}
	}
	return found, best != ""
}

// fast is the price of a Claude model in fast mode, which the catalog
// doesn't list: Anthropic charges every kind of token, cache reads and
// writes too, at twice the model's price on Opus 4.8 and later, and six
// times on Opus 4.6 and 4.7, where fast mode began. It holds however long
// the prompt. Claude Code prices its own fast turns the same way.
func (p Price) fast(model string) Price {
	times := 2.0
	if strings.HasPrefix(model, "claude-opus-4-6") || strings.HasPrefix(model, "claude-opus-4-7") {
		times = 6
	}
	return Price{Input: p.Input * times, Output: p.Output * times, CacheRead: p.CacheRead * times, CacheWrite: p.CacheWrite * times}
}

// Prices keeps the catalog: read from models.dev at most once a day and
// kept on disk, OpenCode's own copy standing in when neither can be had.
type Prices struct {
	home   string
	client *http.Client

	mu      sync.Mutex
	catalog Catalog
	read    time.Time
}

func NewPrices(home string) *Prices {
	return &Prices{home: home, client: &http.Client{Timeout: 15 * time.Second}}
}

// WithCatalog is a Prices holding these prices, for tests.
func WithCatalog(c Catalog) *Prices {
	return &Prices{catalog: c, read: time.Now().Add(1000 * time.Hour)}
}

func (p *Prices) Catalog(ctx context.Context) Catalog {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.catalog != nil && time.Since(p.read) < catalogMaxAge {
		return p.catalog
	}
	kept := filepath.Join(p.home, ".agentman", "models-dev.json")
	if info, err := os.Stat(kept); err == nil && time.Since(info.ModTime()) < catalogMaxAge {
		if c := readCatalog(kept); c != nil {
			p.catalog, p.read = c, info.ModTime()
			return c
		}
	}
	if data, err := p.fetch(ctx); err == nil {
		if c, err := parseCatalog(data); err == nil && len(c) > 0 {
			_ = os.MkdirAll(filepath.Dir(kept), 0o700)
			_ = os.WriteFile(kept, data, 0o600)
			p.catalog, p.read = c, time.Now()
			return c
		}
	}
	// Offline: the copy on disk however old, or OpenCode's.
	for _, path := range []string{kept, filepath.Join(p.home, ".cache", "opencode", "models.json")} {
		if c := readCatalog(path); c != nil {
			// Asked for again in an hour.
			p.catalog, p.read = c, time.Now().Add(-catalogMaxAge+time.Hour)
			return c
		}
	}
	return p.catalog
}

func (p *Prices) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, io.ErrUnexpectedEOF
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxCatalogSize))
}

func readCatalog(path string) Catalog {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	c, err := parseCatalog(data)
	if err != nil || len(c) == 0 {
		return nil
	}
	return c
}
