// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/index.ts at 2195680 (NVIDIA, MIT).

// Package solpig is SoL-Pi's four token-efficiency mechanisms as a PiG Go extension.
package solpig

import (
	"os"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/config"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/observationpack"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/paths"
)

// Extension is the factory PiG loads. It registers only session_start; the
// enabled mechanisms are registered from that handler, once, as SoL-Pi does.
// A configuration error stops loading with SoL-Pi's message and is not retried.
func Extension() *sdk.Extension {
	ext := sdk.New("sol-pig")
	var once sync.Once
	ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		var err error
		once.Do(func() {
			var cfg config.Config
			if cfg, err = loadConfig(ctx); err == nil {
				registerConfigured(ext, cfg)
			}
		})
		return nil, err
	})
	return ext
}

func loadConfig(ctx sdk.Context) (config.Config, error) {
	trusted, _ := ctx.IsProjectTrusted()
	home, _ := os.UserHomeDir()
	return config.Load(ctx.Cwd(), paths.ProjectConfigDirName(os.Getenv), paths.AgentDir(os.Getenv, home), trusted)
}

// registerConfigured registers the enabled mechanisms in SoL-Pi's order:
// Action Fusion, ObservationPack, Evidence-Preserving Reducer, Online Context
// Compact. OCC's context handler must run after ObservationPack's.
func registerConfigured(ext *sdk.Extension, cfg config.Config) {
	if cfg.ObservationPack {
		observationpack.Register(ext)
	}
}
