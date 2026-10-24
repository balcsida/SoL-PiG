// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package solpig

import "testing"

func TestExtensionIdentity(t *testing.T) {
	if got := Extension().Name(); got != "sol-pig" {
		t.Fatalf("Name() = %q, want %q", got, "sol-pig")
	}
}
