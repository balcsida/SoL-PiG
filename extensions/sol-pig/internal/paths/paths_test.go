// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeRootPersistent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sés sion dir")
	got, err := RuntimeRoot(dir, "abc")
	if err != nil || got != filepath.Join(dir, "sol-pi", "abc") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRuntimeRootInMemory(t *testing.T) {
	first, err := RuntimeRoot("", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(first), "sol-pi-abc-") || !strings.HasPrefix(first, os.TempDir()) {
		t.Fatalf("unexpected temp root %q", first)
	}
	info, err := os.Stat(first)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("stat %q: %v %v", first, info, err)
	}
	again, _ := RuntimeRoot("", "abc")
	if again != first {
		t.Fatalf("second call %q != first %q", again, first)
	}
	other, _ := RuntimeRoot("", "def")
	if other == first || !strings.HasPrefix(filepath.Base(other), "sol-pi-def-") {
		t.Fatalf("other id root %q", other)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first root removed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(first); os.RemoveAll(other) })
}

func TestRuntimeRootRejectsUnsafeIDs(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../escape", "nested/session", `nested\session`} {
		for _, dir := range []string{"", t.TempDir()} {
			if _, err := RuntimeRoot(dir, id); !errors.Is(err, ErrUnsafeSessionID) {
				t.Fatalf("id %q dir %q: err = %v", id, dir, err)
			}
		}
	}
}
