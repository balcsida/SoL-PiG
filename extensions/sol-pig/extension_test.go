// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package solpig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/testhost"
)

func TestExtensionIdentity(t *testing.T) {
	if got := Extension().Name(); got != "sol-pig" {
		t.Fatalf("Name() = %q, want %q", got, "sol-pig")
	}
}

// agentDir points the extension at a private agent directory holding body as sol-pi.json ("" for none),
// through the host facts PiG passes every extension process (PIG_EXT_HOST_INFO).
func agentDir(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	info, _ := json.Marshal(map[string]string{"agentDir": dir, "configDirName": ".pig"})
	t.Setenv("PIG_EXT_HOST_INFO", string(info))
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "sol-pi.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func toolNames(h *testhost.Host) []string {
	var names []string
	for _, tool := range h.Tools() {
		names = append(names, tool.Name)
	}
	return names
}

func TestRegisterFrameHasOnlySessionStart(t *testing.T) {
	agentDir(t, "")
	h := testhost.Start(t, Extension(), testhost.Options{Cwd: t.TempDir()})
	registered := h.RegisteredHandlers()
	if len(registered) != 1 || len(registered["session_start"]) != 1 || len(h.Tools()) != 0 {
		t.Fatalf("register frame: handlers=%v tools=%v", registered, h.Tools())
	}
}

func TestSessionStartRegistersEnabledMechanisms(t *testing.T) {
	agentDir(t, `{"version":1,"observationPack":true}`)
	h := testhost.Start(t, Extension(), testhost.Options{Cwd: t.TempDir()})
	for range 2 {
		if _, err := h.Emit("session_start", map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	if names := toolNames(h); len(names) != 1 || names[0] != "obs_recall" {
		t.Fatalf("tools = %v", names)
	}
	if len(h.Handlers("context")) != 1 {
		t.Fatalf("context handlers = %v", h.Handlers("context"))
	}
}

func TestSessionStartAllDisabled(t *testing.T) {
	agentDir(t, `{"version":1}`)
	h := testhost.Start(t, Extension(), testhost.Options{Cwd: t.TempDir()})
	if _, err := h.Emit("session_start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(h.Tools()) != 0 || len(h.Handlers("context")) != 0 {
		t.Fatalf("all-disabled registered tools=%v context=%v", h.Tools(), h.Handlers("context"))
	}
}

func TestSessionStartConfigError(t *testing.T) {
	agentDir(t, `{"version":1,"bogus":true}`)
	h := testhost.Start(t, Extension(), testhost.Options{Cwd: t.TempDir()})
	_, err := h.Emit("session_start", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "Unknown SoL-Pi config key: bogus") {
		t.Fatalf("err = %v", err)
	}
	if len(h.Tools()) != 0 || len(h.Handlers("context")) != 0 {
		t.Fatal("a config error must register nothing")
	}
}

func TestProjectConfigNeedsTrust(t *testing.T) {
	agentDir(t, "")
	cwd := t.TempDir()
	os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755)
	os.WriteFile(filepath.Join(cwd, ".pig", "sol-pi.json"), []byte(`{"version":1,"observationPack":true}`), 0o644)

	untrusted := testhost.Start(t, Extension(), testhost.Options{Cwd: cwd, ProjectTrusted: false})
	if _, err := untrusted.Emit("session_start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(untrusted.Tools()) != 0 {
		t.Fatalf("untrusted project config applied: %v", untrusted.Tools())
	}
	trusted := testhost.Start(t, Extension(), testhost.Options{Cwd: cwd, ProjectTrusted: true})
	if _, err := trusted.Emit("session_start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if names := toolNames(trusted); len(names) != 1 || names[0] != "obs_recall" {
		t.Fatalf("trusted project config not applied: %v", names)
	}
}

func TestSessionStartRegistersActionFusion(t *testing.T) {
	agentDir(t, `{"version":1,"actionFusion":true}`)
	h := testhost.Start(t, Extension(), testhost.Options{Cwd: t.TempDir()})
	if _, err := h.Emit("session_start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if names := strings.Join(toolNames(h), ","); names != "edit,write" {
		t.Fatalf("tools = %v", names)
	}
}

func TestActionFusionRegistersBeforeObservationPack(t *testing.T) {
	agentDir(t, `{"version":1,"actionFusion":true,"observationPack":true}`)
	h := testhost.Start(t, Extension(), testhost.Options{Cwd: t.TempDir()})
	if _, err := h.Emit("session_start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if names := strings.Join(toolNames(h), ","); names != "edit,write,obs_recall" {
		t.Fatalf("tools = %v", names)
	}
}

func TestShellSettingsErrorFailsSessionStart(t *testing.T) {
	agentDir(t, `{"version":1,"actionFusion":true,"observationPack":true}`)
	h := testhost.Start(t, Extension(), testhost.Options{Cwd: t.TempDir(), SettingsErrors: []string{"bad settings"}})
	_, err := h.Emit("session_start", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "Action Fusion could not load Pi shell settings: bad settings") || len(h.Tools()) != 0 {
		t.Fatalf("err = %v, tools = %v", err, toolNames(h))
	}
}
