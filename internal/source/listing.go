package source

import "time"

// idleListing is how long an agent with nothing keeping it running stays
// listed after its last activity: long enough to come back to, and when the
// apps archive an idle agent anyway.
const idleListing = 72 * time.Hour
