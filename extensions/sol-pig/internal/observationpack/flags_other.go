// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

//go:build !unix

package observationpack

// noFollow is unavailable on this platform; symlinked objects are not refused here.
const noFollow = 0
