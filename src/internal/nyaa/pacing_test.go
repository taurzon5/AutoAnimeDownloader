package nyaa

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordSleeps swaps the sleep hook for one that records the waits without spending them, and
// resets the pacing state so each test starts with no previous request.
func recordSleeps(t *testing.T) *[]time.Duration {
	t.Helper()
	var mu sync.Mutex
	var waits []time.Duration
	prevSleep := sleep
	sleep = func(d time.Duration) {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
	}
	paceMu.Lock()
	lastRequest = time.Time{}
	paceMu.Unlock()
	t.Cleanup(func() {
		sleep = prevSleep
		paceMu.Lock()
		lastRequest = time.Time{}
		paceMu.Unlock()
	})
	return &waits
}

func TestWaitForRequestSlot_OffNeverSleeps(t *testing.T) {
	waits := recordSleeps(t)
	defer SetRequestDelay(0)()

	for range 3 {
		waitForRequestSlot()
	}
	if len(*waits) != 0 {
		t.Fatalf("delay 0 must not wait, got %v", *waits)
	}
}

func TestWaitForRequestSlot_FirstRequestGoesOutAtOnce(t *testing.T) {
	waits := recordSleeps(t)
	defer SetRequestDelay(2)()

	waitForRequestSlot()
	if len(*waits) != 0 {
		t.Fatalf("the first request has no previous one to wait for, got %v", *waits)
	}
}

func TestWaitForRequestSlot_SecondRequestWaitsTheRestOfTheGap(t *testing.T) {
	waits := recordSleeps(t)
	defer SetRequestDelay(2)()

	waitForRequestSlot()
	waitForRequestSlot()
	if len(*waits) != 1 {
		t.Fatalf("expected one wait, got %v", *waits)
	}
	if w := (*waits)[0]; w <= 0 || w > 2*time.Second {
		t.Fatalf("wait should be the rest of the 2s gap, got %v", w)
	}
}

func TestWaitForRequestSlot_NoWaitOnceTheGapHasPassed(t *testing.T) {
	waits := recordSleeps(t)
	defer SetRequestDelay(2)()

	paceMu.Lock()
	lastRequest = time.Now().Add(-3 * time.Second)
	paceMu.Unlock()

	waitForRequestSlot()
	if len(*waits) != 0 {
		t.Fatalf("3s since the last request is past the 2s gap, got %v", *waits)
	}
}

// The gap is global: requests from concurrent searches each wait their turn, so N requests made
// at once produce N-1 waits.
func TestWaitForRequestSlot_ConcurrentRequestsAreSpaced(t *testing.T) {
	waits := recordSleeps(t)
	defer SetRequestDelay(1)()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			waitForRequestSlot()
		}()
	}
	wg.Wait()
	if len(*waits) != 3 {
		t.Fatalf("4 concurrent requests should produce 3 waits, got %v", *waits)
	}
}

func TestFetchNyaaPage_WaitsBetweenRequests(t *testing.T) {
	waits := recordSleeps(t)
	defer SetRequestDelay(1)()
	defer MockNyaaHttpGet(func(string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("<html><body></body></html>")),
		}, nil
	})()

	for range 2 {
		if _, err := fetchNyaaPage("https://nyaa.example/?q=x"); err != nil {
			t.Fatalf("fetchNyaaPage: %v", err)
		}
	}
	if len(*waits) != 1 {
		t.Fatalf("two page fetches should wait once between them, got %v", *waits)
	}
}

func TestSetRequestDelay_NegativeTurnsItOffAndRestoreBringsItBack(t *testing.T) {
	restoreOuter := SetRequestDelay(1.5)
	defer restoreOuter()

	restore := SetRequestDelay(-1)
	if got := ActiveRequestDelay(); got != 0 {
		t.Fatalf("negative delay should be off, got %v", got)
	}
	restore()
	if got := ActiveRequestDelay(); got != 1500*time.Millisecond {
		t.Fatalf("restore should bring back 1.5s, got %v", got)
	}
}
