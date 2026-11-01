package protocol

// Limits: how much of each agent's plan is left in each of its windows (a
// Claude subscription's five hours and its week, a ChatGPT plan's for
// Codex), as each agent itself reports it. The phone and the Mac draw the
// same answer.

const (
	// ReqLimits asks what is left of each agent's plan, answered with an
	// EvtLimits. The daemon keeps its last answer for a couple of minutes;
	// Force reads the limits again, unless the last read was moments ago.
	ReqLimits RequestType = "limits"
	EvtLimits EventType   = "limits"
)

// Limits answers ReqLimits: one entry for each agent on this Mac that has
// plan limits to read, the agents that have none left out.
type Limits struct {
	Agents []AgentLimits `json:"agents"`
}

// AgentLimits is one agent's plan.
type AgentLimits struct {
	Kind Kind `json:"kind"`
	// Plan is the plan's name, "Max" or "Plus", when the agent says.
	Plan string `json:"plan,omitempty"`
	// CheckedAt is when the numbers were read, in milliseconds since 1970.
	CheckedAt int64 `json:"checkedAt"`
	// LastTurn is true when the numbers are the ones the agent wrote down
	// with its last reply, not asked of the account just now: Codex's own
	// records, read while its shared daemon isn't running. They are still
	// right unless the plan was used elsewhere since.
	LastTurn bool          `json:"lastTurn,omitempty"`
	Windows  []LimitWindow `json:"windows,omitzero"`
	// Problem is a sentence for a person: why there are no numbers, or why
	// the ones shown are the last that could be read.
	Problem string `json:"problem,omitempty"`
}

// LimitWindow is one window of a plan: a share of it used, which goes back
// to nothing when the window resets.
type LimitWindow struct {
	// ID tells the window apart from the plan's others: "five_hour",
	// "seven_day", "primary" and so on.
	ID string `json:"id"`
	// Kind is "session", "weekly", "monthly" or "other": the order windows
	// are shown in.
	Kind string `json:"kind"`
	// Label names it for a person: "Session", "Weekly", "Weekly · Opus".
	Label string `json:"label"`
	// Used is the share used, from 0 to 100.
	Used float64 `json:"used"`
	// ResetsAt is when the window resets, in milliseconds since 1970; 0
	// when the agent doesn't say.
	ResetsAt int64 `json:"resetsAt,omitempty"`
	// Minutes is how long the window lasts, for how far through it the
	// plan is; 0 when the agent doesn't say.
	Minutes int `json:"minutes,omitempty"`
}
