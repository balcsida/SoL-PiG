// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package actionfusion

import (
	"errors"
	"testing"
	"time"
)

const settle = 50 * time.Millisecond

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// waitQueued blocks until n calls are waiting behind the holder of key.
func waitQueued(t *testing.T, key string, n int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		queueMu.Lock()
		e := queueEntries[key]
		got := 0
		if e != nil {
			got = len(e.waiters)
		}
		queueMu.Unlock()
		if got == n {
			return
		}
	}
	t.Fatalf("never reached %d queued waiters", n)
}

func TestWithFileQueueSerializesSameKey(t *testing.T) {
	entered, release, secondEntered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go WithFileQueue("k", func() error { close(entered); <-release; return nil })
	waitFor(t, entered, "first body")
	go WithFileQueue("k", func() error { close(secondEntered); return nil })
	waitQueued(t, "k", 1)
	select {
	case <-secondEntered:
		t.Fatal("second body entered while the first held the key")
	case <-time.After(settle):
	}
	close(release)
	waitFor(t, secondEntered, "second body")
}

func TestWithFileQueueIsFIFO(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	go WithFileQueue("k", func() error { close(entered); <-release; return nil })
	waitFor(t, entered, "holder")
	order := make(chan int, 3)
	done := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		go WithFileQueue("k", func() error { order <- i; done <- struct{}{}; return nil })
		waitQueued(t, "k", i+1)
	}
	close(release)
	for i := 0; i < 3; i++ {
		waitFor(t, done, "waiter")
		if got := <-order; got != i {
			t.Fatalf("position %d ran waiter %d", i, got)
		}
	}
}

func TestWithFileQueueDifferentKeysOverlap(t *testing.T) {
	aIn, bIn, done := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
	meet := func(key string, mine, other chan struct{}) {
		_ = WithFileQueue(key, func() error {
			close(mine)
			select {
			case <-other:
			case <-time.After(2 * time.Second):
				t.Error("other key's body never ran concurrently")
			}
			return nil
		})
		done <- struct{}{}
	}
	go meet("a", aIn, bIn)
	go meet("b", bIn, aIn)
	waitFor(t, done, "first")
	waitFor(t, done, "second")
}

func TestWithFileQueueReleasesAfterError(t *testing.T) {
	boom := errors.New("boom")
	if err := WithFileQueue("k", func() error { return boom }); err != boom {
		t.Fatalf("got %v, want boom", err)
	}
	ran := false
	if err := WithFileQueue("k", func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("second call: ran=%v err=%v", ran, err)
	}
	queueMu.Lock()
	defer queueMu.Unlock()
	if len(queueEntries) != 0 {
		t.Fatalf("entries leaked: %d", len(queueEntries))
	}
}
