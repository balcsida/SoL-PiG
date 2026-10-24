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

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestAgentDir(t *testing.T) {
	const home = "/home/u"
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default", map[string]string{}, "/home/u/.pig/agent"},
		{"pig home", map[string]string{"PIG_HOME": "/p"}, "/p/agent"},
		{"xdg", map[string]string{"XDG_CONFIG_HOME": "/x"}, "/x/pig/agent"},
		{"pig home beats xdg", map[string]string{"PIG_HOME": "/p", "XDG_CONFIG_HOME": "/x"}, "/p/agent"},
		{"explicit agent dir with tilde", map[string]string{"PIG_CODING_AGENT_DIR": "~/a"}, "/home/u/a"},
		{"shared mode", map[string]string{"PIG_USE_PI_DIRS": "1"}, "/home/u/.pi/agent"},
		{"shared mode explicit", map[string]string{"PIG_USE_PI_DIRS": "1", "PI_CODING_AGENT_DIR": "/q"}, "/q"},
		{"shared mode ignores pig var", map[string]string{"PIG_USE_PI_DIRS": "1", "PIG_CODING_AGENT_DIR": "/ignored"}, "/home/u/.pi/agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AgentDir(env(tc.env), home); got != filepath.FromSlash(tc.want) {
				t.Fatalf("AgentDir = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProjectConfigDirName(t *testing.T) {
	if got := ProjectConfigDirName(env(map[string]string{})); got != ".pig" {
		t.Fatalf("default = %q", got)
	}
	if got := ProjectConfigDirName(env(map[string]string{"PIG_USE_PI_DIRS": "1"})); got != ".pi" {
		t.Fatalf("shared = %q", got)
	}
}

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
