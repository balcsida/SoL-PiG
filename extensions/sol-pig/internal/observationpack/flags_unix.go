// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

//go:build unix

package observationpack

import "syscall"

// noFollow refuses to open a symlink in place of an object, as SoL-Pi's O_NOFOLLOW does.
const noFollow = syscall.O_NOFOLLOW
