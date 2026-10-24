// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package observationpack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLedgerAppend(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "sub", "ledger.jsonl")}
	if err := l.Append(map[string]any{"event": "full", "id": "obs_1", "request": 1}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(map[string]any{"event": "placeholder", "id": "obs_1", "request": 3}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, `{"timestamp":"`) {
			t.Fatalf("line %d does not start with the timestamp: %s", i, line)
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if entry["id"] != "obs_1" || entry["event"] == nil || entry["request"] == nil {
			t.Fatalf("line %d fields = %v", i, entry)
		}
	}
}
