// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/observation-pack/ledger.ts at 2195680 (NVIDIA, MIT).

package observationpack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Ledger is the append-only JSONL record of what the mechanism did per provider request.
type Ledger struct{ Path string }

// Append writes one line: {"timestamp": <now, ISO 8601 UTC>, ...entry}.
func (l Ledger) Append(entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line := `{"timestamp":"` + time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00") + `"`
	if len(body) > 2 {
		line += "," + string(body[1:])
	} else {
		line += "}"
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(line + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
