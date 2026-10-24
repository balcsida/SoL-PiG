// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package reducer

import "testing"

func TestDiagnosticCommand(t *testing.T) {
	for _, command := range []string{"pytest -q", "lake build", "cargo test --all", "make check", "go test ./..."} {
		if !DiagnosticCommand.MatchString(command) {
			t.Errorf("%q should match", command)
		}
	}
	for _, command := range []string{"rg test src", "makefile", "remake all"} {
		if DiagnosticCommand.MatchString(command) {
			t.Errorf("%q should not match", command)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	cfg := LoadConfig("/run/dir", "", "")
	if len(cfg.RunID) != 16 || cfg.RunID != SHA256Hex("/run/dir")[:16] {
		t.Fatalf("RunID = %q", cfg.RunID)
	}
	if cfg.StoreRoot != "/run/dir/evidence-preserving-reducer" {
		t.Fatalf("StoreRoot = %q", cfg.StoreRoot)
	}
	if cfg.ReducerProvider != "openai-codex" || cfg.ReducerModel != "gpt-5.6-luna" {
		t.Fatalf("defaults = %q %q", cfg.ReducerProvider, cfg.ReducerModel)
	}
	cfg = LoadConfig("/run/dir", "p", "m")
	if cfg.ReducerProvider != "p" || cfg.ReducerModel != "m" {
		t.Fatalf("overrides = %q %q", cfg.ReducerProvider, cfg.ReducerModel)
	}
}

func TestUTF16Len(t *testing.T) {
	if got := UTF16Len("aé😀"); got != 4 {
		t.Fatalf("UTF16Len = %d, want 4", got)
	}
	if got := UTF16Len("a😀"); got != 3 {
		t.Fatalf("UTF16Len(a😀) = %d, want 3", got)
	}
	if got := UTF16Len("a\xffb"); got != 3 {
		t.Fatalf("UTF16Len(invalid byte) = %d, want 3", got)
	}
}
