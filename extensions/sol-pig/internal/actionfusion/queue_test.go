package actionfusion

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// span runs a 30 ms body on key and reports when it entered and left.
func span(key string) (enter, exit time.Time) {
	_ = WithFileQueue(key, func() error {
		enter = time.Now()
		time.Sleep(30 * time.Millisecond)
		exit = time.Now()
		return nil
	})
	return
}

func runPair(k1, k2 string) (e1, x1, e2, x2 time.Time) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e1, x1 = span(k1) }()
	go func() { defer wg.Done(); e2, x2 = span(k2) }()
	wg.Wait()
	return
}

func TestWithFileQueueSerializesSameKey(t *testing.T) {
	e1, x1, e2, x2 := runPair("k", "k")
	if e1.Before(x2) && e2.Before(x1) {
		t.Fatal("same-key bodies overlapped")
	}
}

func TestWithFileQueueDifferentKeysOverlap(t *testing.T) {
	e1, x1, e2, x2 := runPair("a", "b")
	if !(e1.Before(x2) && e2.Before(x1)) {
		t.Fatal("different-key bodies did not overlap")
	}
}

func TestWithFileQueueReleasesAfterError(t *testing.T) {
	boom := errors.New("boom")
	if err := WithFileQueue("k", func() error { return boom }); err != boom {
		t.Fatalf("got %v, want boom", err)
	}
	ran := false
	if err := WithFileQueue("k", func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("third call: ran=%v err=%v", ran, err)
	}
	queueMu.Lock()
	defer queueMu.Unlock()
	if len(queueEntries) != 0 {
		t.Fatalf("entries leaked: %d", len(queueEntries))
	}
}
