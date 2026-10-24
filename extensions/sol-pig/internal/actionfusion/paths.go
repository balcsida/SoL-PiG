// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/action-fusion/file-queue.ts at 2195680 (NVIDIA, MIT).

// Package actionfusion fuses a file mutation with its follow-up command.
package actionfusion

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
)

var (
	unicodeSpaces     = regexp.MustCompile("[\u00A0\u2000-\u200A\u202F\u205F\u3000]")
	windowsShellDrive = regexp.MustCompile(`(?i)^/(?:mnt/|cygdrive/)?([a-z])(?:/(.*))?$`)
)

// NormalizeToolPath replaces the Unicode spaces U+00A0, U+2000–U+200A, U+202F, U+205F, U+3000 with " " and strips one leading "@".
func NormalizeToolPath(p string) string {
	return strings.TrimPrefix(unicodeSpaces.ReplaceAllString(p, " "), "@")
}

// NormalizeWindowsShellPath maps /c/x, /mnt/c/x, /cygdrive/c/x to C:\x when goos == "windows"; untouched otherwise, or when the path starts with "//" or contains "\".
// Git Bash, MSYS, Cygwin and WSL hand over such paths, while Pi's own mutation tools address the native drive path.
func NormalizeWindowsShellPath(p, goos string) string {
	if goos != "windows" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, `\`) {
		return p
	}
	m := windowsShellDrive.FindStringSubmatch(p)
	if m == nil {
		return p
	}
	return strings.ToUpper(m[1]) + `:\` + strings.ReplaceAll(m[2], "/", `\`)
}

// ResolveToolPath: normalize; "file://" → url.Parse + path (invalid encoding → error); "~" → home; "~/" (and "~\" on windows) → home + rest; else filepath.Join(cwd, p) when relative.
func ResolveToolPath(cwd, p string) (string, error) {
	p = NormalizeWindowsShellPath(NormalizeToolPath(p), runtime.GOOS)
	if strings.HasPrefix(p, "file://") {
		var err error
		if p, err = fileURLPath(p); err != nil {
			return "", err
		}
	}
	if p == "~" || strings.HasPrefix(p, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(p, `~\`)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Join(cwd, p), nil
}

// fileURLPath decodes a file URL like Node's fileURLToPath, which rejects an encoded separator.
func fileURLPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") {
		return "", errors.New("file URL path must not include encoded / characters")
	}
	path := u.Path
	if runtime.GOOS == "windows" {
		path = filepath.FromSlash(strings.TrimPrefix(path, "/"))
	}
	return path, nil
}

// CanonicalQueueKey: EvalSymlinks of the deepest existing ancestor plus the missing tail.
func CanonicalQueueKey(path string) (string, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := resolved
	var missing []string
	for {
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(append([]string{real}, missing...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return resolved, nil
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		current = parent
	}
}
