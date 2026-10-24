// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

// Package solpig is SoL-Pi's four token-efficiency mechanisms as a PiG Go extension.
package solpig

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// Extension is the factory PiG loads. It registers only session_start; the
// enabled mechanisms are registered from that handler, as SoL-Pi does.
func Extension() *sdk.Extension {
	ext := sdk.New("sol-pig")
	ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) { return nil, nil })
	return ext
}
