// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/runtime-paths.ts at 2195680 (NVIDIA, MIT).

// Package paths resolves SoL-Pi's per-session runtime root.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// ErrUnsafeSessionID is returned for a session id that could escape the runtime root.
var ErrUnsafeSessionID = errors.New("SoL-Pi requires a safe Pi session id")

var safeSessionID = regexp.MustCompile(`^(?i)[a-z0-9][a-z0-9._-]*$`)

var (
	tempMu    sync.Mutex
	tempRoots = map[string]string{}
)

// RuntimeRoot is <sessionDir>/sol-pi/<sessionID>, or for a session without a
// directory a private temp dir created once per session id and kept after
// shutdown, as SoL-Pi does. The persistent root is not created here.
func RuntimeRoot(sessionDir, sessionID string) (string, error) {
	if !safeSessionID.MatchString(sessionID) {
		return "", ErrUnsafeSessionID
	}
	if sessionDir != "" {
		return filepath.Join(sessionDir, "sol-pi", sessionID), nil
	}
	tempMu.Lock()
	defer tempMu.Unlock()
	if root, ok := tempRoots[sessionID]; ok {
		return root, nil
	}
	root, err := os.MkdirTemp(os.TempDir(), "sol-pi-"+sessionID+"-")
	if err != nil {
		return "", err
	}
	tempRoots[sessionID] = root
	return root, nil
}
