package protocol

// The editor in Agentman for Mac gets its language servers from am serve
// over a WebSocket of its own on loopback, and asks where it is over the
// local socket. The relay never carries this: the endpoint and its token are
// for apps on this Mac alone.

const (
	// ReqLSPEndpoint asks where the language servers are, answered with an
	// EvtLSPEndpoint carrying LSP.
	ReqLSPEndpoint RequestType = "lsp_endpoint"
	// EvtLSPEndpoint carries LSP.
	EvtLSPEndpoint EventType = "lsp_endpoint"
)

// LSPEndpoint is where the editor's page connects for language servers, and
// the token it must bring.
type LSPEndpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}
