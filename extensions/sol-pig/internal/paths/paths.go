// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/runtime-paths.ts at 2195680 (NVIDIA, MIT).
// The directory rules replicate PiG internal/codingagent/paths.go at v0.5.3.

// Package paths resolves PiG's agent and project config directories and
// SoL-Pi's per-session runtime root.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ErrUnsafeSessionID is returned for a session id that could escape the runtime root.
var ErrUnsafeSessionID = errors.New("SoL-Pi requires a safe Pi session id")

var safeSessionID = regexp.MustCompile(`^(?i)[a-z0-9][a-z0-9._-]*$`)

func sharedMode(getenv func(string) string) bool { return getenv("PIG_USE_PI_DIRS") == "1" }

// AgentDir is PiG's agent directory: PIG_CODING_AGENT_DIR (or PI_CODING_AGENT_DIR
// in shared mode) when set, else ~/.pi/agent in shared mode, else <config root>/agent
// where the config root is $PIG_HOME, else $XDG_CONFIG_HOME/pig, else ~/.pig.
func AgentDir(getenv func(string) string, home string) string {
	if sharedMode(getenv) {
		if v := getenv("PI_CODING_AGENT_DIR"); v != "" {
			return expandHome(v, home)
		}
		return filepath.Join(home, ".pi", "agent")
	}
	if v := getenv("PIG_CODING_AGENT_DIR"); v != "" {
		return expandHome(v, home)
	}
	return filepath.Join(configRoot(getenv, home), "agent")
}

func configRoot(getenv func(string) string, home string) string {
	if v := getenv("PIG_HOME"); v != "" {
		return expandHome(v, home)
	}
	if v := getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandHome(v, home), "pig")
	}
	return filepath.Join(home, ".pig")
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:])
	}
	return p
}

// ProjectConfigDirName is ".pig", or ".pi" when PiG shares Pi's directories.
func ProjectConfigDirName(getenv func(string) string) string {
	if sharedMode(getenv) {
		return ".pi"
	}
	return ".pig"
}

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
