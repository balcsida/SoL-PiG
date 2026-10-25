// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/tui.ts at 2195680 (NVIDIA, MIT).

// Package savings shows what a mechanism saved, as SoL-Pi's TUI does: a
// banner in tool renderers, a notification and a status line in the TUI.
package savings

import (
	"math"
	"regexp"
	"strconv"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Mechanism names a SoL mechanism as the banners show it.
type Mechanism string

// The four mechanisms, with SoL-Pi's labels.
const (
	ActionFusion         Mechanism = "Action Fusion"
	ObservationPack      Mechanism = "Observation Pack"
	LunaDelegating       Mechanism = "Luna Delegating"
	OnlineContextCompact Mechanism = "Online Context Compact"
)

// StatusKey is the TUI status slot the savings line uses.
const StatusKey = "sol-pi-savings"

// StatusDuration is how long the status line stays; tests shorten it.
var StatusDuration = 4 * time.Second

var nestedToolCallID = regexp.MustCompile(`/\d+$`)

// FormatCount renders a token-style count: "12,345 context tokens avoided".
func FormatCount(v float64, unit string) string {
	return groupThousands(clampRound(v)) + " " + unit
}

// FormatBytes renders a byte saving: "76.4 KiB removed from future prompts".
func FormatBytes(v float64) string {
	bytes := clampRound(v)
	switch {
	case bytes >= 1024*1024:
		return compactDecimal(float64(bytes)/(1024*1024)) + " MiB removed from future prompts"
	case bytes >= 1024:
		return compactDecimal(float64(bytes)/1024) + " KiB removed from future prompts"
	}
	return groupThousands(bytes) + " B removed from future prompts"
}

// BannerLines is the two-line savings banner followed by the base rendering.
func BannerLines(m Mechanism, saving string, base []string) []string {
	lines := []string{"⚡ SoL-PiG · " + string(m), "Money saved · " + saving}
	return append(lines, base...)
}

// IsNestedToolCallID reports a call made through another tool (id ends in "/<n>").
func IsNestedToolCallID(id string) bool { return nestedToolCallID.MatchString(id) }

var (
	statusMu  sync.Mutex
	statusGen uint64
)

// Show notifies the user and sets the status line in the TUI; other modes stay silent.
// The status clears after StatusDuration unless a newer saving replaced it.
// SoL-Pi tests mode === "tui", but PiG 0.5.3 never sends the run mode (Mode() is "print"
// even in the TUI), so the gate is the host's hasUI, with RPC silent as in SoL-Pi.
func Show(ctx sdk.Context, m Mechanism, saving string) {
	if !ctx.HasUI() || ctx.Mode() == "rpc" {
		return
	}
	ctx.Notify("⚡ SoL-PiG · "+string(m)+"\nMoney saved · "+saving, "info")
	ctx.SetStatus(StatusKey, "⚡ "+string(m)+" · "+saving)

	statusMu.Lock()
	statusGen++
	gen := statusGen
	statusMu.Unlock()
	time.AfterFunc(StatusDuration, func() {
		statusMu.Lock()
		current := statusGen == gen
		statusMu.Unlock()
		if current {
			ctx.ClearStatus(StatusKey) // Pi's setStatus(key, undefined): remove the entry
		}
	})
}

func clampRound(v float64) int64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return int64(math.Max(0, math.Round(v)))
}

// compactDecimal is JS toFixed(1) (half up) with a trailing ".0" dropped.
func compactDecimal(x float64) string {
	s := strconv.FormatFloat(math.Floor(x*10+0.5)/10, 'f', 1, 64)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		return s[:len(s)-2]
	}
	return s
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
