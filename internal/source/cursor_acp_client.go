package source

// Cursor's `agent acp`, through the shared ACP client (acp_client.go).

// The Cursor adapter's names for the shared client.
type (
	cursorACPClient   = acpClient
	cursorACPEnvelope = acpEnvelope
)

// cursorACPSpec starts Cursor's ACP server. parameterizedModelPicker makes
// Cursor list models by name ("claude-opus-5-5") rather than as variant
// strings, and take a name back from set_config_option: the shape the
// phone's model list and switch use.
var cursorACPSpec = acpSpec{
	name: "Cursor", binary: "agent", args: []string{"acp"},
	missing: "Cursor Agent CLI is not installed",
	meta:    map[string]any{"parameterizedModelPicker": true},
	auth:    "cursor_login",
}

func newCursorACPClient(cwd string, handle func(cursorACPEnvelope)) (*cursorACPClient, error) {
	return dialACP(cursorACPSpec, cwd, handle)
}
