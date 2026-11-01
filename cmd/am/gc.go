package main

import (
	"os"
	"runtime/debug"
)

// serveGCPercent is how far am serve lets its heap grow past what it is
// using before collecting, against Go's default of 100.
//
// The daemon runs for weeks, mostly idle, so the memory it holds matters
// more than the CPU a collection takes. And on macOS the heap's high-water
// mark is what Activity Monitor goes on showing: pages the runtime hands
// back for the first time stay in the process's footprint until they are
// used again. Over this Mac's real stores, halving the headroom took the
// footprint after the usage warm-up and a search sweep from 333 MB to 258
// MB, for 2.6 s more CPU once, in the warm-up; a collection of the steady
// heap takes about 7 ms, and at the daemon's steady rate of garbage that
// comes once every few tens of seconds.
const serveGCPercent = 50

// tuneCollector sets serveGCPercent, unless whoever started am serve set
// GOGC themselves.
func tuneCollector() {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(serveGCPercent)
	}
}
