// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package reducer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func bashEvent(body string, extra map[string]any) map[string]any {
	event := map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "go test ./..."},
		"content":  []any{text(body)},
	}
	for k, v := range extra {
		event[k] = v
	}
	return event
}

func fusedEvent(tool string, blocks ...string) map[string]any {
	content := make([]any, len(blocks))
	for i, b := range blocks {
		content[i] = text(b)
	}
	return map[string]any{
		"toolName": tool,
		"input":    map[string]any{"then_run": map[string]any{"command": "make check"}},
		"content":  content,
	}
}

func boolPtr(b bool) *bool { return &b }

func equalBool(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestBashCandidate(t *testing.T) {
	cases := []struct {
		name  string
		event map[string]any
		body  string
		want  *bool
	}{
		{"inline body, no indicator", bashEvent("out", nil), "out", nil},
		{"structured float64 exit 2", bashEvent("out", map[string]any{"structuredContent": map[string]any{"exit_code": float64(2)}}), "out", boolPtr(true)},
		{"structured json.Number exit 2", bashEvent("out", map[string]any{"structuredContent": map[string]any{"exit_code": json.Number("2")}}), "out", boolPtr(true)},
		{"inline trailer code 2", bashEvent("out\n\nCommand exited with code 2", nil), "out\n\nCommand exited with code 2", boolPtr(true)},
		{"structured 0 beats trailer 3", bashEvent("out\nCommand exited with code 3", map[string]any{"structuredContent": map[string]any{"exit_code": float64(0)}}), "out\nCommand exited with code 3", boolPtr(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ReducibleToolResult(tc.event)
			if !ok {
				t.Fatal("not reducible")
			}
			if got.Command != "go test ./..." || got.Body != tc.body || !equalBool(got.ExitFailed, tc.want) {
				t.Fatalf("got %+v", got)
			}
			if !reflect.DeepEqual(got.Project("R"), []any{text("R")}) {
				t.Fatalf("Project = %#v", got.Project("R"))
			}
		})
	}
	if _, ok := ReducibleToolResult(bashEvent("out", map[string]any{"input": map[string]any{"command": ""}})); ok {
		t.Fatal("empty command should not be reducible")
	}
}

func TestFusedCandidate(t *testing.T) {
	got, ok := ReducibleToolResult(fusedEvent("write", "Successfully wrote to f", "[then_run:failed]\n\nout\nCommand exited with code 7"))
	if !ok {
		t.Fatal("not reducible")
	}
	if got.Command != "make check" || got.Body != "out\nCommand exited with code 7" || !equalBool(got.ExitFailed, boolPtr(true)) {
		t.Fatalf("got %+v", got)
	}
	want := []any{text("Successfully wrote to f"), text("[then_run:failed]\n\nR")}
	if !reflect.DeepEqual(got.Project("R"), want) {
		t.Fatalf("Project = %#v", got.Project("R"))
	}

	got, ok = ReducibleToolResult(fusedEvent("edit", "done", "[then_run:succeeded]\nclean"))
	if !ok || got.Body != "clean" || !equalBool(got.ExitFailed, boolPtr(false)) {
		t.Fatalf("succeeded: %+v ok=%v", got, ok)
	}

	got, ok = ReducibleToolResult(fusedEvent("edit", "done", "[then_run:succeeded]\nquotes [then_run:failed] later"))
	if !ok || got.Body != "quotes [then_run:failed] later" || !equalBool(got.ExitFailed, boolPtr(false)) {
		t.Fatalf("first marker wins: %+v", got)
	}

	noThenRun := fusedEvent("write", "[then_run:succeeded]\nx")
	noThenRun["input"] = map[string]any{"path": "f"}
	if _, ok := ReducibleToolResult(noThenRun); ok {
		t.Fatal("missing then_run should not be reducible")
	}
	if _, ok := ReducibleToolResult(fusedEvent("write", "no marker here")); ok {
		t.Fatal("no marker should not be reducible")
	}
	if _, ok := ReducibleToolResult(fusedEvent("codemode", "[then_run:succeeded]\nx")); ok {
		t.Fatal("codemode should not be reducible")
	}
}

func TestExactBodyFromTempLog(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	logPath := filepath.Join(tmp, "pi-bash-abc.log")
	if err := os.WriteFile(logPath, []byte("full\nlog"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := func(event map[string]any) string {
		got, ok := ReducibleToolResult(event)
		if !ok {
			t.Fatal("not reducible")
		}
		return got.Body
	}

	if got := body(bashEvent("tail", map[string]any{"details": map[string]any{"fullOutputPath": logPath}})); got != "full\nlog" {
		t.Fatalf("details path: %q", got)
	}
	if got := body(bashEvent("tail\n[Showing lines 1-5 of 9. Full output: "+logPath+"]", nil)); got != "full\nlog" {
		t.Fatalf("inline path: %q", got)
	}

	other := t.TempDir()
	outside := filepath.Join(other, "pi-bash-abc.log")
	os.WriteFile(outside, []byte("outside"), 0o600)
	if got := body(bashEvent("tail", map[string]any{"details": map[string]any{"fullOutputPath": outside}})); got != "tail" {
		t.Fatalf("outside dir: %q", got)
	}

	link := filepath.Join(tmp, "pi-bash-link.log")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if got := body(bashEvent("tail", map[string]any{"details": map[string]any{"fullOutputPath": link}})); got != "tail" {
		t.Fatalf("symlink: %q", got)
	}

	badName := filepath.Join(tmp, "other.log")
	os.WriteFile(badName, []byte("x"), 0o600)
	if got := body(bashEvent("tail", map[string]any{"details": map[string]any{"fullOutputPath": badName}})); got != "tail" {
		t.Fatalf("bad name: %q", got)
	}

	invalid := filepath.Join(tmp, "pi-bash-bad.log")
	os.WriteFile(invalid, []byte("a\xffb"), 0o600)
	if got := body(bashEvent("tail", map[string]any{"details": map[string]any{"fullOutputPath": invalid}})); got != "a\uFFFDb" {
		t.Fatalf("invalid utf-8: %q", got)
	}

	// TMPDIR reached through a symlink still accepts a log in the real directory.
	viaLink := filepath.Join(t.TempDir(), "tmplink")
	if err := os.Symlink(tmp, viaLink); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", viaLink)
	if got := body(bashEvent("tail", map[string]any{"details": map[string]any{"fullOutputPath": logPath}})); got != "full\nlog" {
		t.Fatalf("symlinked TMPDIR: %q", got)
	}
	if got := body(bashEvent("tail", map[string]any{"details": map[string]any{"fullOutputPath": filepath.Join(viaLink, "pi-bash-abc.log")}})); got != "full\nlog" {
		t.Fatalf("path through symlinked TMPDIR: %q", got)
	}
}

func TestTempDir(t *testing.T) {
	t.Setenv("TMPDIR", "")
	t.Setenv("TMP", "")
	t.Setenv("TEMP", "/x/y/")
	if got := TempDir(); got != "/x/y" {
		t.Fatalf("TempDir = %q", got)
	}
	t.Setenv("TEMP", "")
	if got := TempDir(); got != "/tmp" {
		t.Fatalf("TempDir = %q", got)
	}
}

func TestInlineExitFailedZeroSpellings(t *testing.T) {
	for _, trailer := range []string{"code 0", "code 00", "code -0"} {
		got, _ := ReducibleToolResult(bashEvent("out\nCommand exited with "+trailer, nil))
		if !equalBool(got.ExitFailed, boolPtr(false)) {
			t.Errorf("%s: ExitFailed = %v", trailer, got.ExitFailed)
		}
	}
}
