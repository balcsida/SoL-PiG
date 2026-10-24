// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package actionfusion_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/actionfusion"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/testhost"
)

const (
	editThenRun  = "Command to run next on this file after the edit succeeds — e.g. run, build, start/restart, install, or check it; optional timeout in seconds. Skipped if the edit fails; a non-zero exit is reported but keeps the edit."
	writeThenRun = "Command to run next on this file after the write succeeds — e.g. run, build, start/restart, install, or check it; optional timeout in seconds. Skipped if the write fails; a non-zero exit is reported but keeps the write."
)

func ptr(s string) *string { return &s }

// start runs a minimal extension whose session_start loads the shell options and registers Action Fusion.
func start(t *testing.T, opts testhost.Options) (*testhost.Host, string) {
	t.Helper()
	ext := sdk.New("fusion-test")
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		shell, err := actionfusion.LoadShellOptions(ctx, t.TempDir(), opts.ProjectTrusted)
		if err != nil {
			return nil, err
		}
		return nil, actionfusion.Register(ext, ctx, shell)
	})
	opts.Cwd = t.TempDir()
	opts.SessionDir = t.TempDir()
	opts.SessionID = "s1"
	opts.Mode = "tui"
	h := testhost.Start(t, ext, opts)
	if _, err := h.Emit("session_start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	return h, opts.Cwd
}

func texts(t *testing.T, content []any) []string {
	t.Helper()
	var out []string
	for _, c := range content {
		out = append(out, c.(map[string]any)["text"].(string))
	}
	return out
}

func wantTexts(t *testing.T, res testhost.ToolCallResult, want ...string) {
	t.Helper()
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if got := texts(t, res.Content); !reflect.DeepEqual(got, want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func tool(h *testhost.Host, name string) testhost.ToolDecl {
	for _, d := range h.Tools() {
		if d.Name == name {
			return d
		}
	}
	return testhost.ToolDecl{}
}

func TestRegistersEditAndWriteWithThenRun(t *testing.T) {
	h, _ := start(t, testhost.Options{})
	for name, description := range map[string]string{"edit": editThenRun, "write": writeThenRun} {
		decl := tool(h, name)
		props, _ := decl.Parameters["properties"].(map[string]any)
		thenRun, _ := props["then_run"].(map[string]any)
		if thenRun == nil || thenRun["description"] != description || thenRun["type"] != "object" {
			t.Fatalf("%s then_run = %v", name, thenRun)
		}
		sub, _ := thenRun["properties"].(map[string]any)
		command, _ := sub["command"].(map[string]any)
		timeout, _ := sub["timeout"].(map[string]any)
		if command["type"] != "string" || command["description"] != "Bash command to run" ||
			timeout["type"] != "number" || timeout["description"] != "Timeout in seconds (optional, no default timeout)" ||
			!reflect.DeepEqual(thenRun["required"], []any{"command"}) {
			t.Fatalf("%s then_run sub-schema = %v", name, thenRun)
		}
	}
	if got, want := tool(h, "edit").Parameters["required"], []any{"path", "edits"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("edit required = %v", got)
	}
	if got, want := tool(h, "write").Parameters["required"], []any{"path", "content"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("write required = %v", got)
	}
	if !strings.HasPrefix(tool(h, "edit").Description, "Edit a single file using exact text replacement.") {
		t.Fatalf("edit description = %q", tool(h, "edit").Description)
	}
}

func TestWriteThenRunSeesTheContent(t *testing.T) {
	h, cwd := start(t, testhost.Options{})
	res := h.CallTool("write", "toolu_1", map[string]any{"path": "report.txt", "content": "hello\n", "then_run": map[string]any{"command": "cat report.txt"}})
	wantTexts(t, res, "Successfully wrote to report.txt", "[then_run:succeeded]\nhello\n")
	if b, _ := os.ReadFile(filepath.Join(cwd, "report.txt")); string(b) != "hello\n" {
		t.Fatalf("file = %q", b)
	}
	notify := h.Calls("ui.notify")
	if len(notify) != 1 || notify[0].Args["message"] != "⚡ SoL-PiG · Action Fusion\nMoney saved · 1 model round-trip avoided" {
		t.Fatalf("notify = %+v", notify)
	}
}

func TestEditThenRunSeesTheEdit(t *testing.T) {
	h, cwd := start(t, testhost.Options{})
	os.WriteFile(filepath.Join(cwd, "f.txt"), []byte("hello\n"), 0o644)
	res := h.CallTool("edit", "toolu_1", map[string]any{
		"path": "f.txt", "edits": []any{map[string]any{"oldText": "hello", "newText": "bye"}},
		"then_run": map[string]any{"command": "cat f.txt"},
	})
	wantTexts(t, res, "Successfully replaced 1 block(s) in f.txt.", "[then_run:succeeded]\nbye\n")
	if res.Details == nil {
		t.Fatal("edit details missing")
	}
}

func TestFailingCommandKeepsTheMutation(t *testing.T) {
	h, cwd := start(t, testhost.Options{})
	res := h.CallTool("write", "toolu_1", map[string]any{"path": "f.txt", "content": "x", "then_run": map[string]any{"command": "echo validation failed >&2; exit 7"}})
	wantTexts(t, res, "Successfully wrote to f.txt", "[then_run:failed]\nvalidation failed\n\n\nCommand exited with code 7")
	if !res.IsError {
		t.Fatal("want IsError")
	}
	if _, err := os.Stat(filepath.Join(cwd, "f.txt")); err != nil {
		t.Fatal(err)
	}
	if n := h.Calls("ui.notify"); len(n) != 0 {
		t.Fatalf("notify = %+v", n)
	}
}

func TestNoThenRunIsPlain(t *testing.T) {
	h, _ := start(t, testhost.Options{})
	wantTexts(t, h.CallTool("write", "toolu_1", map[string]any{"path": "f.txt", "content": "x"}), "Successfully wrote to f.txt")
	for _, c := range h.BuiltinCalls() {
		if c.Name == "bash" {
			t.Fatalf("bash called: %+v", c)
		}
	}
}

func TestMutationErrorSkipsCommand(t *testing.T) {
	h, cwd := start(t, testhost.Options{})
	os.WriteFile(filepath.Join(cwd, "f.txt"), []byte("hello\n"), 0o644)
	res := h.CallTool("edit", "toolu_1", map[string]any{
		"path": "f.txt", "edits": []any{map[string]any{"oldText": "nope", "newText": "x"}},
		"then_run": map[string]any{"command": "true"},
	})
	if !res.IsError && res.Err == nil {
		t.Fatalf("want an error, got %+v", res)
	}
	all := fmt.Sprint(res.Content, res.Err)
	if !strings.Contains(all, "[then_run:skipped] The file mutation did not complete successfully; the command was not run.") {
		t.Fatalf("result = %s", all)
	}
	for _, c := range h.BuiltinCalls() {
		if c.Name == "bash" {
			t.Fatalf("bash called: %+v", c)
		}
	}
}

func TestInvalidThenRunRunsNothing(t *testing.T) {
	h, cwd := start(t, testhost.Options{})
	res := h.CallTool("write", "toolu_1", map[string]any{"path": "f.txt", "content": "x", "then_run": "ls"})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "then_run must be an object with a command") {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(cwd, "f.txt")); err == nil {
		t.Fatal("mutation ran")
	}
}

func TestTimeoutPassesThrough(t *testing.T) {
	h, _ := start(t, testhost.Options{})
	h.CallTool("write", "toolu_1", map[string]any{"path": "f.txt", "content": "x", "then_run": map[string]any{"command": "true", "timeout": 12}})
	var found bool
	for _, c := range h.BuiltinCalls() {
		if c.Name == "bash" {
			found = true
			if c.Params["timeout"] != float64(12) || c.ToolCallID != "toolu_1:then_run" {
				t.Fatalf("bash call = %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("no bash call")
	}
}

func TestNestedCallShowsNoSavings(t *testing.T) {
	h, _ := start(t, testhost.Options{})
	res := h.CallTool("write", "toolu_1/1", map[string]any{"path": "f.txt", "content": "x", "then_run": map[string]any{"command": "true"}})
	wantTexts(t, res, "Successfully wrote to f.txt", "[then_run:succeeded]\n(no output)")
	if n := h.Calls("ui.notify"); len(n) != 0 {
		t.Fatalf("notify = %+v", n)
	}
}

func TestInvalidFileURLRunsNothing(t *testing.T) {
	h, _ := start(t, testhost.Options{})
	res := h.CallTool("write", "toolu_1", map[string]any{"path": "file:///tmp/%zz", "content": "x", "then_run": map[string]any{"command": "true"}})
	if res.Err == nil {
		t.Fatalf("want Err, got %+v", res)
	}
	if calls := h.BuiltinCalls(); len(calls) != 0 {
		t.Fatalf("builtin calls = %+v", calls)
	}
}

func TestSameFileCallsSerialize(t *testing.T) {
	h, _ := start(t, testhost.Options{})
	var wg sync.WaitGroup
	results := make([]testhost.ToolCallResult, 2)
	for i, content := range []string{"first\n", "second\n"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = h.CallTool("write", fmt.Sprintf("toolu_%d", i), map[string]any{"path": "f.txt", "content": content, "then_run": map[string]any{"command": "sleep 0.2; cat f.txt"}})
		}()
	}
	wg.Wait()
	for i, content := range []string{"first\n", "second\n"} {
		got := texts(t, results[i].Content)
		if len(got) != 2 || got[1] != "[then_run:succeeded]\n"+content {
			t.Fatalf("call %d = %q", i, got)
		}
	}
}

func TestShellOptionsForwarded(t *testing.T) {
	h, _ := start(t, testhost.Options{ShellPath: ptr("/bin/bash"), ShellCommandPrefix: ptr("export CI=1")})
	h.CallTool("write", "toolu_1", map[string]any{"path": "f.txt", "content": "x", "then_run": map[string]any{"command": "true"}})
	for _, c := range h.BuiltinCalls() {
		if c.Name == "bash" {
			if c.Options["shellPath"] != "/bin/bash" || c.Options["commandPrefix"] != "export CI=1" {
				t.Fatalf("bash options = %v", c.Options)
			}
			return
		}
	}
	t.Fatal("no bash call")
}

func TestShellSettingsErrorsAreReported(t *testing.T) {
	ext := sdk.New("fusion-test")
	var loadErr error
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		_, loadErr = actionfusion.LoadShellOptions(ctx, t.TempDir(), false)
		return nil, nil
	})
	h := testhost.Start(t, ext, testhost.Options{Cwd: t.TempDir(), SettingsErrors: []string{"bad settings"}})
	h.Emit("session_start", map[string]any{})
	if loadErr == nil || loadErr.Error() != "Action Fusion could not load Pi shell settings: bad settings" {
		t.Fatalf("err = %v", loadErr)
	}
}
