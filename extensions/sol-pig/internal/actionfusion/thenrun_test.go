package actionfusion

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func textResult(s string) sdk.AgentToolResult {
	return sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: s}}}
}

func fusedFor(t *testing.T, mutation sdk.AgentToolResult, bash func(map[string]any) (sdk.AgentToolResult, error)) Fused {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Fused{
		AbsolutePath: path,
		ThenRun:      map[string]any{"command": "echo hi"},
		Mutate:       func() (sdk.AgentToolResult, error) { return mutation, nil },
		RunBash:      bash,
	}
}

func lastText(r sdk.AgentToolResult) string { return r.Content[len(r.Content)-1].Text }

func TestRunWithoutThenRunLeavesResultUnchanged(t *testing.T) {
	f := fusedFor(t, textResult("edited"), func(map[string]any) (sdk.AgentToolResult, error) {
		t.Fatal("bash must not run")
		return sdk.AgentToolResult{}, nil
	})
	f.ThenRun = nil
	got, st, err := Run(f)
	if err != nil || st != StatusNone || len(got.Content) != 1 || got.Content[0].Text != "edited" {
		t.Fatalf("got %+v %q %v", got, st, err)
	}
}

func TestRunMutateError(t *testing.T) {
	f := fusedFor(t, sdk.AgentToolResult{}, nil)
	f.Mutate = func() (sdk.AgentToolResult, error) { return sdk.AgentToolResult{}, errors.New("boom") }
	_, _, err := Run(f)
	want := "boom\n\n[then_run:skipped] The file mutation did not complete successfully; the command was not run."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	f.ThenRun = nil
	if _, _, err := Run(f); err == nil || err.Error() != "boom" {
		t.Fatalf("got %v", err)
	}
}

func TestRunMutationIsErrorSkipsCommand(t *testing.T) {
	m := textResult("bad")
	m.IsError = true
	f := fusedFor(t, m, func(map[string]any) (sdk.AgentToolResult, error) {
		t.Fatal("bash must not run")
		return sdk.AgentToolResult{}, nil
	})
	got, st, err := Run(f)
	if err != nil || st != StatusSkipped || !got.IsError || lastText(got) != "[then_run:skipped] The file mutation did not complete successfully; the command was not run." {
		t.Fatalf("got %+v %q %v", got, st, err)
	}
}

func TestRunSucceededKeepsMutationFieldsAndPassesTimeout(t *testing.T) {
	m := textResult("edited")
	m.Details = "diff"
	m.StructuredContent = "sc"
	var seen map[string]any
	f := fusedFor(t, m, func(p map[string]any) (sdk.AgentToolResult, error) {
		seen = p
		r := textResult("out1")
		r.Content = append(r.Content, sdk.ToolResultContent{Type: "text", Text: "out2"})
		r.Details = "bashdetails"
		r.StructuredContent = "bashsc"
		return r, nil
	})
	f.ThenRun = map[string]any{"command": "c", "timeout": 12}
	got, st, err := Run(f)
	if err != nil || st != StatusSucceeded || got.IsError {
		t.Fatalf("got %+v %q %v", got, st, err)
	}
	if !reflect.DeepEqual(seen, map[string]any{"command": "c", "timeout": 12}) {
		t.Fatalf("params %v", seen)
	}
	if lastText(got) != "[then_run:succeeded]\nout1\nout2" || got.Details != "diff" || got.StructuredContent != "sc" || len(got.Content) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestRunEmptyOutputIsMarkerOnly(t *testing.T) {
	f := fusedFor(t, textResult("edited"), func(map[string]any) (sdk.AgentToolResult, error) { return sdk.AgentToolResult{}, nil })
	got, _, _ := Run(f)
	if lastText(got) != "[then_run:succeeded]" {
		t.Fatalf("got %q", lastText(got))
	}
	f.RunBash = func(map[string]any) (sdk.AgentToolResult, error) {
		r := sdk.AgentToolResult{IsError: true}
		return r, nil
	}
	got, st, _ := Run(f)
	if lastText(got) != "[then_run:failed]" || st != StatusFailed || !got.IsError {
		t.Fatalf("got %+v %q", got, st)
	}
}

func TestRunFailedResultKeepsOutputAndDetails(t *testing.T) {
	m := textResult("edited")
	m.Details = "diff"
	f := fusedFor(t, m, func(map[string]any) (sdk.AgentToolResult, error) {
		r := textResult("nope")
		r.IsError = true
		return r, nil
	})
	got, st, _ := Run(f)
	if lastText(got) != "[then_run:failed]\nnope" || st != StatusFailed || !got.IsError || got.Details != "diff" {
		t.Fatalf("got %+v %q", got, st)
	}
}

func TestRunBashErrorThrownShape(t *testing.T) {
	f := fusedFor(t, textResult("edited"), func(p map[string]any) (sdk.AgentToolResult, error) {
		if p["timeout"] != 0 {
			t.Fatalf("timeout %v", p["timeout"])
		}
		return sdk.AgentToolResult{}, errors.New("timed out")
	})
	f.ThenRun = map[string]any{"command": "c", "timeout": 0}
	_, _, err := Run(f)
	if err == nil || err.Error() != "edited\n\n[then_run:failed]\n\ntimed out" {
		t.Fatalf("got %v", err)
	}
	f.Mutate = func() (sdk.AgentToolResult, error) { return sdk.AgentToolResult{}, nil }
	if _, _, err := Run(f); err == nil || err.Error() != "[then_run:failed]\n\ntimed out" {
		t.Fatalf("got %v", err)
	}
}

func TestRunSkipsCommandWhenFileChangesBetweenHashes(t *testing.T) {
	f := fusedFor(t, textResult("edited"), func(map[string]any) (sdk.AgentToolResult, error) {
		t.Fatal("bash must not run")
		return sdk.AgentToolResult{}, nil
	})
	calls := 0
	betweenHashes = func() { calls++; os.WriteFile(f.AbsolutePath, []byte("changed"), 0o644) }
	defer func() { betweenHashes = func() {} }()
	_, _, err := Run(f)
	want := "[then_run:skipped] target content changed after the fused mutation; the command was not run."
	if err == nil || err.Error() != want || calls != 1 {
		t.Fatalf("got %v calls=%d", err, calls)
	}
}

func TestAssertUnchangedBeforeCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	os.WriteFile(path, []byte("a"), 0o644)
	if err := AssertUnchangedBeforeCommand(path); err != nil {
		t.Fatal(err)
	}
	err := AssertUnchangedBeforeCommand(path + ".missing")
	if err == nil || !strings.HasPrefix(err.Error(), "[then_run:skipped] ") || !strings.HasSuffix(err.Error(), "; the command was not run.") {
		t.Fatalf("got %v", err)
	}
}

func TestThenRunFromParams(t *testing.T) {
	tr, rest := ThenRunFromParams(map[string]any{"path": "p", "then_run": map[string]any{"command": "c"}})
	if tr["command"] != "c" || !reflect.DeepEqual(rest, map[string]any{"path": "p"}) {
		t.Fatalf("got %v %v", tr, rest)
	}
	for _, in := range []map[string]any{{"path": "p"}, {"path": "p", "then_run": nil}} {
		tr, rest := ThenRunFromParams(in)
		if tr != nil || !reflect.DeepEqual(rest, map[string]any{"path": "p"}) {
			t.Fatalf("got %v %v", tr, rest)
		}
	}
}
