package protocol

// Usage: what the agents on this Mac spent over a stretch of time, in
// tokens and in what the API would have charged for them, read from each
// agent's own records. The phone and the Mac draw the same summary.

const (
	// ReqUsage asks for the last Days days (1, 7, 30 or 90), answered with
	// an EvtUsage.
	ReqUsage RequestType = "usage"
	EvtUsage EventType   = "usage"
)

// Tokens are counted by kind: fresh input, input read from the cache, input
// written to it, and output, thinking included.
type Tokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Output     int64 `json:"output"`
}

// Total is every token the models processed.
func (t Tokens) Total() int64 { return t.Input + t.CacheRead + t.CacheWrite + t.Output }

// Add sums two counts.
func (t Tokens) Add(o Tokens) Tokens {
	return Tokens{Input: t.Input + o.Input, CacheRead: t.CacheRead + o.CacheRead, CacheWrite: t.CacheWrite + o.CacheWrite, Output: t.Output + o.Output}
}

// UsageCost is a cost in dollars split as Tokens are.
type UsageCost struct {
	Input      float64 `json:"input"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Output     float64 `json:"output"`
}

func (c UsageCost) Total() float64 { return c.Input + c.CacheRead + c.CacheWrite + c.Output }

// AgentUsage is one agent's share.
type AgentUsage struct {
	Kind     Kind    `json:"kind"`
	Sessions int     `json:"sessions"`
	Tokens   Tokens  `json:"tokens"`
	Cost     float64 `json:"cost"`
}

// UsageBucket is a day (an hour, over the last day) of the chart: each
// agent's cost and tokens, by kind.
type UsageBucket struct {
	// Start is when it begins, in milliseconds since 1970, local midnight
	// for a day.
	Start  int64            `json:"start"`
	Cost   map[Kind]float64 `json:"cost,omitempty"`
	Tokens map[Kind]int64   `json:"tokens,omitempty"`
}

// ModelUsage is one model's share, under the agent that used it.
type ModelUsage struct {
	Model  string  `json:"model"`
	Kind   Kind    `json:"kind"`
	Tokens Tokens  `json:"tokens"`
	Cost   float64 `json:"cost"`
	// Priced is false for a model with no known price: its cost is left out.
	Priced bool `json:"priced"`
}

// Usage is the summary.
type Usage struct {
	Days int `json:"days"`
	// Hourly is true when the buckets are hours: over the last day.
	Hourly   bool      `json:"hourly,omitempty"`
	Sessions int       `json:"sessions"`
	Tokens   Tokens    `json:"tokens"`
	Cost     UsageCost `json:"cost"`
	// CacheSavings is what reading from the cache saved over fresh input.
	CacheSavings float64       `json:"cacheSavings"`
	Agents       []AgentUsage  `json:"agents"`
	Buckets      []UsageBucket `json:"buckets"`
	Models       []ModelUsage  `json:"models"`
	// Skipped counts records that couldn't be read: usage may be short.
	Skipped int `json:"skipped,omitempty"`
}
