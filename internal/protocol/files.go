package protocol

// A file sent to an agent from the phone: not an image (those go through the
// relay's upload locker) but anything else, a PDF, a log, a spreadsheet. The
// relay carries at most 128 KiB in a request from the phone, so the file
// travels in slices of its base64 over the connection the phone already has,
// as a voice recording does, and the daemon joins them and keeps the file in
// its own folder. The message then names it as @path, which the agent reads.

const (
	// ReqAttachFile sends one part of a file (FilePart), answered with an
	// EvtAttached: Done and Path once the last part is in.
	ReqAttachFile RequestType = "attach_file"
	EvtAttached   EventType   = "attached"
)

// FilePart is one part of a file on its way to the Mac.
type FilePart struct {
	// ID names the file, the same in every part.
	ID    string `json:"id"`
	Part  int    `json:"part"`
	Parts int    `json:"parts"`
	// Name is the file's name on the phone, kept so the agent sees it.
	Name string `json:"name"`
	// Data is this part's slice of the file's base64.
	Data string `json:"data"`
}

// AttachedFile answers a part: received, or, for the last, where the file
// now is on the Mac.
type AttachedFile struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
	Path string `json:"path,omitempty"`
}
