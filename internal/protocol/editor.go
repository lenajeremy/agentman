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

	// ReqLSPStatus asks which language servers there are and where each
	// comes from, for Settings; ReqLSPSet changes them (Language) and
	// ReqLSPRemove removes the copy am downloaded of Language.Server. All
	// three are answered with an EvtLSPStatus carrying Languages.
	ReqLSPStatus RequestType = "lsp_status"
	ReqLSPSet    RequestType = "lsp_set"
	ReqLSPRemove RequestType = "lsp_remove"
	EvtLSPStatus EventType   = "lsp_status"
)

// LanguagesState is the editor's language servers, for Settings.
type LanguagesState struct {
	// NoDownloads keeps am to the servers already on the Mac.
	NoDownloads bool             `json:"noDownloads"`
	Servers     []LanguageServer `json:"servers"`
	// NodeSize is the Node.js am downloaded for npm servers, in bytes, or 0.
	NodeSize int64 `json:"nodeSize,omitempty"`
}

// LanguageServer is one server and where it comes from.
type LanguageServer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Language string `json:"language"`
	// Source is "yours" (a program the user chose), "mac" (one already on
	// the Mac), "downloaded", "download" (am gets it when first needed),
	// "missing" or "off".
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	// Size is what the downloaded copy takes, in bytes.
	Size int64 `json:"size,omitempty"`
	// DownloadSize says about how big a download is: "about 45 MB".
	DownloadSize string `json:"downloadSize,omitempty"`
	// Missing says how to get it when am can't.
	Missing string `json:"missing,omitempty"`
	// Running are the project folders it runs for now.
	Running []string `json:"running,omitempty"`
}

// LanguageChange is one change to the language servers' settings: a
// server's own program ("" for the usual one) or whether it's off, or
// whether am may download servers.
type LanguageChange struct {
	Server      string  `json:"server,omitempty"`
	Path        *string `json:"path,omitempty"`
	Disabled    *bool   `json:"disabled,omitempty"`
	NoDownloads *bool   `json:"noDownloads,omitempty"`
}

// LSPEndpoint is where the editor's page connects for language servers, and
// the token it must bring.
type LSPEndpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}
