package nyaa

import (
	"sync"
	"sync/atomic"
	"time"
)

// requestDelay is the minimum gap between two Nyaa page requests (config
// nyaa_request_delay_seconds), pushed by files.LoadConfigs like maxSearchPages. 0 = no wait, which
// is also the default with no config push (tests, the package used alone).
var requestDelay atomic.Int64

// The gap is GLOBAL, not per search: the pass searches several animes at once
// (maxConcurrentAnimes), and a pause inside each search would still let those searches hit
// nyaa.si together. paceMu is held across the sleep on purpose, so waiting requests go out one at
// a time, each a full gap after the previous one.
var (
	paceMu      sync.Mutex
	lastRequest time.Time
)

// sleep is an indirection for time.Sleep so tests can observe the wait without spending it.
var sleep = time.Sleep

// SetRequestDelay applies the gap between Nyaa page requests and returns a function that restores
// the previous value (the SetMaxSearchPages pattern). A value <= 0 turns the wait off.
func SetRequestDelay(seconds float64) (restore func()) {
	prev := requestDelay.Load()
	if seconds <= 0 {
		requestDelay.Store(0)
	} else {
		requestDelay.Store(int64(seconds * float64(time.Second)))
	}
	return func() { requestDelay.Store(prev) }
}

// ActiveRequestDelay returns the gap in use.
func ActiveRequestDelay() time.Duration {
	return time.Duration(requestDelay.Load())
}

// waitForRequestSlot blocks until at least ActiveRequestDelay has passed since the previous Nyaa
// page request, then claims the slot. With the delay off it returns at once and does not touch
// the lock, so the default path costs nothing.
func waitForRequestSlot() {
	gap := ActiveRequestDelay()
	if gap <= 0 {
		return
	}
	paceMu.Lock()
	defer paceMu.Unlock()
	if !lastRequest.IsZero() {
		if wait := gap - time.Since(lastRequest); wait > 0 {
			sleep(wait)
		}
	}
	lastRequest = time.Now()
}
