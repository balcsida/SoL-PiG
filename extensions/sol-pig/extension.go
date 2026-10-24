// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/index.ts at 2195680 (NVIDIA, MIT).

// Package solpig is SoL-Pi's four token-efficiency mechanisms as a PiG Go extension.
package solpig

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/config"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/observationpack"
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

// loadConfig reads sol-pi.json from the directories the host itself resolves:
// Pi's getAgentDir() and CONFIG_DIR_NAME, as PiG hands them to every extension.
func loadConfig(ctx sdk.Context) (config.Config, error) {
	agentDir, err := sdk.AgentDir()
	if err != nil {
		return config.Config{}, err
	}
	dirName, err := sdk.ConfigDirName()
	if err == nil && dirName == "" {
		err = sdk.ErrHostInfoUnavailable
	}
	if err != nil {
		return config.Config{}, err
	}
	trusted, _ := ctx.IsProjectTrusted()
	return config.Load(ctx.Cwd(), dirName, agentDir, trusted)
}

// registerConfigured registers the enabled mechanisms in SoL-Pi's order:
// Action Fusion, ObservationPack, Evidence-Preserving Reducer, Online Context
// Compact. OCC's context handler must run after ObservationPack's.
func registerConfigured(ext *sdk.Extension, cfg config.Config) {
	if cfg.ObservationPack {
		observationpack.Register(ext)
	}
}
