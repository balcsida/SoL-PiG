// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/evidence-preserving-reducer/journal.ts at 2195680 (NVIDIA, MIT).

package reducer

import (
	"fmt"
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Journal records the reducer's decisions as custom session entries, which never enter the model context.
type Journal struct {
	ctx sdk.Context
	cfg Config
}

// Write appends one entry: schema, runId and kind first, then data. A failed append is reported, never raised.
func (j Journal) Write(kind string, data map[string]any) {
	entry := map[string]any{"schema": EventSchema, "runId": j.cfg.RunID, "kind": kind}
	for k, v := range data {
		entry[k] = v
	}
	if err := j.ctx.AppendEntry(EventType, entry); err != nil {
		fmt.Fprintf(os.Stderr, "[reducer] journal: %v\n", err)
	}
}
