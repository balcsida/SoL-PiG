// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/evidence-preserving-reducer/config.ts at 2195680 (NVIDIA, MIT).

// Package reducer replaces long diagnostic tool output with verified evidence receipts.
package reducer

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"time"
	"unicode/utf16"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/config"
)

const (
	EventType     = "sol-pi-evidence-preserving-reducer-v1"
	EventSchema   = "sol-pi-evidence-preserving-reducer/1"
	ReceiptSchema = "sol-pi-evidence-receipt/1"
	ReceiptPrefix = "sol_pi_evidence_receipt_v1"

	MaxEvidenceItems = 12
	MaxQuoteChars    = 600 // UTF-16 code units
	MinBytes         = 4096
	MaxChars         = 600000 // UTF-16 code units
	MaxOutputTokens  = 2048
)

// Timeout bounds one reducer model call; tests shorten it.
var Timeout = 90 * time.Second

var (
	DiagnosticCommand = regexp.MustCompile(`(?i)(?:^|[;&|()\s])(?:lake\s+build|lake\s+env\s+lean|lean|coq|cargo(?:\s+(?:build|test|check))?|zig\s+build|pytest|python(?:3)?\s+-m\s+(?:pytest|unittest|py_compile)|ctest|cmake\s+--build|ninja|make|npm\s+test|pnpm\s+test|yarn\s+test|go\s+test|bazel\s+test)(?:\s|$)`)
	FailureSignal     = regexp.MustCompile(`(?i)error|failed|failure|fatal|exception|panic|timeout|unsolved|type mismatch|assert`)
	LikelySecret      = regexp.MustCompile(`(?i)(?:api[_-]?key|authorization|bearer|access[_-]?token|secret)[^\n]{0,32}[=:][^\n]+`)
	exitStatusLine    = regexp.MustCompile(`(?:^|\n)Command exited with code (-?\d+)\s*$`)
	tempLogName       = regexp.MustCompile(`^pi-bash-[^/\\]+\.log$`)
)

// Config is the effective reducer configuration for one run.
type Config struct {
	RunID           string
	StoreRoot       string
	ReducerProvider string
	ReducerModel    string
}

// LoadConfig ports loadReducerConfig; empty provider or model fall back to the defaults.
func LoadConfig(runtimeDir, provider, model string) Config {
	defaults := config.Default()
	if provider == "" {
		provider = defaults.ReducerProvider
	}
	if model == "" {
		model = defaults.ReducerModel
	}
	return Config{
		RunID:           SHA256Hex(runtimeDir)[:16],
		StoreRoot:       filepath.Join(runtimeDir, "evidence-preserving-reducer"),
		ReducerProvider: provider,
		ReducerModel:    model,
	}
}

// SHA256Hex is the hex SHA-256 of the UTF-8 bytes of s.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// UTF16Len is JavaScript's String.length.
func UTF16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
