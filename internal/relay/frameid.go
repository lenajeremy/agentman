package relay

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

// newFrameID mints an identifier for a frame, used to correlate a reply with
// its request.
//
// Uniqueness only has to hold within one live connection, so this is not a
// UUID: random bytes with a timestamp prefix are enough, and the timestamp
// makes frames sortable when reading a debug log.
func newFrameID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		// A frame without a usable ID is worse than a slightly weaker one:
		// fall back to the clock rather than fail to send.
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return strconv.FormatInt(time.Now().UnixMilli(), 36) + "-" + hex.EncodeToString(buf)
}
