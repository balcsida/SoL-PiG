// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/action-fusion/then-run.ts at 2195680 (NVIDIA, MIT).

package actionfusion

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	ThenRunSucceeded = "[then_run:succeeded]"
	ThenRunFailed    = "[then_run:failed]"
	ThenRunSkipped   = "[then_run:skipped]"
	skippedReason    = "The file mutation did not complete successfully; the command was not run."
)

// Status is what happened to the follow-up command of a returned fused result.
type Status string

const (
	StatusNone      Status = "none"
	StatusSkipped   Status = "skipped"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Fused is one mutation tool call with an optional follow-up command.
type Fused struct {
	AbsolutePath string
	ThenRun      map[string]any // nil when the call had no then_run
	Mutate       func() (sdk.AgentToolResult, error)
	RunBash      func(params map[string]any) (sdk.AgentToolResult, error) // the host's bash built-in with id "<toolCallId>:then_run"
}

// betweenHashes lets tests change the file between the two reads of AssertUnchangedBeforeCommand.
var betweenHashes = func() {}

func resultText(r sdk.AgentToolResult) string {
	var texts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	b, err := os.ReadFile(path)
	return sha256.Sum256(b), err
}

// AssertUnchangedBeforeCommand fails when the file's content differs between two reads.
func AssertUnchangedBeforeCommand(path string) error {
	err := func() error {
		first, err := fileSHA256(path)
		if err != nil {
			return err
		}
		runtime.Gosched()
		betweenHashes()
		second, err := fileSHA256(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(first[:], second[:]) {
			return errors.New("target content changed after the fused mutation")
		}
		return nil
	}()
	if err != nil {
		return fmt.Errorf("%s %s; the command was not run.", ThenRunSkipped, err.Error())
	}
	return nil
}

// appendText adds the follow-up report to the mutation result; the bash result's details are not carried over.
func appendText(r sdk.AgentToolResult, text string, isError bool) sdk.AgentToolResult {
	r.Content = append(append([]sdk.ToolResultContent(nil), r.Content...), sdk.ToolResultContent{Type: "text", Text: text})
	if isError {
		r.IsError = true
	}
	return r
}

func withOutput(marker, output string) string {
	if output == "" {
		return marker
	}
	return marker + "\n" + output
}

// Run executes Mutate, then the command, inside one queue slot for AbsolutePath.
func Run(f Fused) (sdk.AgentToolResult, Status, error) {
	key, err := CanonicalQueueKey(f.AbsolutePath)
	if err != nil {
		key = f.AbsolutePath
	}
	var (
		result sdk.AgentToolResult
		status Status
	)
	err = WithFileQueue(key, func() error {
		var err error
		result, status, err = runLocked(f)
		return err
	})
	return result, status, err
}

func runLocked(f Fused) (sdk.AgentToolResult, Status, error) {
	mutation, err := f.Mutate()
	if err != nil {
		if f.ThenRun != nil {
			return sdk.AgentToolResult{}, StatusNone, fmt.Errorf("%s\n\n%s %s", err.Error(), ThenRunSkipped, skippedReason)
		}
		return sdk.AgentToolResult{}, StatusNone, err
	}
	if f.ThenRun == nil {
		return mutation, StatusNone, nil
	}
	if mutation.IsError {
		return appendText(mutation, ThenRunSkipped+" "+skippedReason, true), StatusSkipped, nil
	}
	if err := AssertUnchangedBeforeCommand(f.AbsolutePath); err != nil {
		return sdk.AgentToolResult{}, StatusNone, err
	}
	bash, err := f.RunBash(f.ThenRun)
	if err != nil {
		var parts []string
		for _, p := range []string{resultText(mutation), ThenRunFailed, err.Error()} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		return sdk.AgentToolResult{}, StatusNone, errors.New(strings.Join(parts, "\n\n"))
	}
	output := resultText(bash)
	if bash.IsError {
		return appendText(mutation, withOutput(ThenRunFailed, output), true), StatusFailed, nil
	}
	return appendText(mutation, withOutput(ThenRunSucceeded, output), false), StatusSucceeded, nil
}

// ThenRunFromParams splits then_run off the tool params; thenRun is nil when absent or null.
func ThenRunFromParams(params map[string]any) (thenRun map[string]any, rest map[string]any) {
	rest = make(map[string]any, len(params))
	for k, v := range params {
		if k != "then_run" {
			rest[k] = v
		}
	}
	thenRun, _ = params["then_run"].(map[string]any)
	return thenRun, rest
}
