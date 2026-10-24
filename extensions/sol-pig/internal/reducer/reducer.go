// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/evidence-preserving-reducer/index.ts at 2195680 (NVIDIA, MIT).

package reducer

import (
	"errors"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/paths"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/savings"
)

// Options selects the reducer model; empty fields take the defaults.
type Options struct{ Provider, Model string }

// Register adds the tool_result handler that turns long diagnostic output into verified receipts.
func Register(ext *sdk.Extension, opts Options) {
	var mu sync.Mutex
	configs := map[string]Config{} // by runtime root
	ext.OnEvent(sdk.EventToolResult, func(ctx sdk.Context, event map[string]any) (any, error) {
		if _, nested := event["parentToolCallId"]; nested {
			return nil, nil
		}
		root, err := runtimeRoot(ctx)
		if err != nil {
			return nil, nil // no safe session id: stay silent
		}
		mu.Lock()
		cfg, ok := configs[root]
		if !ok {
			cfg = LoadConfig(root, opts.Provider, opts.Model)
			configs[root] = cfg
		}
		mu.Unlock()
		return reduce(ctx, cfg, event)
	})
}

func runtimeRoot(ctx sdk.Context) (string, error) {
	sessions := ctx.SessionManager()
	dir, err := sessions.GetSessionDir()
	if err != nil {
		return "", err
	}
	id, err := sessions.GetSessionID()
	if err != nil {
		return "", err
	}
	return paths.RuntimeRoot(dir, id)
}

// reduce is reduceToolResult. It returns nil, nil, leaving the result
// untouched, on every path except a verified receipt that is smaller than the log.
func reduce(ctx sdk.Context, cfg Config, event map[string]any) (any, error) {
	if _, nested := event["parentToolCallId"]; nested {
		return nil, nil
	}
	reducible, ok := ReducibleToolResult(event)
	if !ok || !DiagnosticCommand.MatchString(reducible.Command) {
		return nil, nil
	}
	body, command := reducible.Body, reducible.Command
	if len(body) < MinBytes {
		return nil, nil
	}
	journal := Journal{ctx, cfg}
	toolCallID, _ := event["toolCallId"].(string)
	isError, _ := event["isError"].(bool)
	// The receipt status follows isError; when the result's own text disagrees, leave the output alone.
	if reducible.ExitFailed != nil && *reducible.ExitFailed != isError {
		journal.Write("fallback", map[string]any{"toolCallId": toolCallID, "reason": "exit-state-mismatch", "isError": isError, "exitFailed": *reducible.ExitFailed})
		return nil, nil
	}
	if chars := UTF16Len(body); chars > MaxChars {
		journal.Write("fallback", map[string]any{"reason": "source-over-max-chars", "sourceChars": chars, "maxChars": MaxChars})
		return nil, nil
	}
	if LikelySecret.MatchString(body) {
		journal.Write("fallback", map[string]any{"reason": "likely-secret"})
		return nil, nil
	}

	a, err := ArchiveBody(cfg.StoreRoot, body)
	if err != nil {
		// SoL-Pi lets this throw; the design fails open instead.
		journal.Write("fallback", map[string]any{"toolCallId": toolCallID, "reason": "archive-error", "errorMessage": err.Error()})
		return nil, nil
	}
	commandSHA := SHA256Hex(command)
	journal.Write("candidate", map[string]any{
		"toolCallId": toolCallID, "commandSha256": commandSHA, "isError": isError,
		"sourceSha256": a.Hash, "sourceBytes": a.Bytes, "sourceLines": a.Lines, "sourcePath": a.Path,
	})

	provider, err := CallReducer(ctx, cfg, command, isError, a, body)
	if err != nil {
		reason := "model-call-exception"
		switch {
		case errors.Is(err, ErrTimeout):
			reason = "model-call-timeout"
		case errors.Is(err, ErrModelUnavailable):
			reason = "reducer-model-unavailable"
		}
		journal.Write("fallback", map[string]any{"toolCallId": toolCallID, "sourceSha256": a.Hash, "reason": reason})
		return nil, nil
	}
	usage := map[string]any{
		"input": provider.Usage.Input, "output": provider.Usage.Output, "cacheRead": provider.Usage.CacheRead,
		"cacheWrite": provider.Usage.CacheWrite, "totalTokens": provider.Usage.TotalTokens,
	}
	response := map[string]any{
		"toolCallId": toolCallID, "sourceSha256": a.Hash, "provider": provider.Provider, "model": provider.Model, "usage": usage,
	}
	failure := map[string]any{"toolCallId": toolCallID, "sourceSha256": a.Hash, "reason": "model-response-error"}
	for key, value := range map[string]string{"stopReason": provider.StopReason, "errorMessage": provider.ErrorMessage} {
		if value != "" { // TS leaves these undefined, so they are absent on disk
			response[key], failure[key] = value, value
		}
	}
	journal.Write("provider_response", response)
	if !provider.OK {
		journal.Write("fallback", failure)
		return nil, nil
	}

	verified, reason := ValidateReceipt(provider.OutputText, a, body, isError)
	if reason != "" {
		journal.Write("fallback", map[string]any{"toolCallId": toolCallID, "sourceSha256": a.Hash, "reason": reason, "usage": usage})
		return nil, nil
	}
	receipt := ReceiptText(command, a, verified, provider)
	receiptBytes := len(receipt)
	if receiptBytes >= a.Bytes {
		journal.Write("fallback", map[string]any{
			"toolCallId": toolCallID, "sourceSha256": a.Hash, "reason": "receipt-not-smaller",
			"receiptBytes": receiptBytes, "sourceBytes": a.Bytes, "usage": usage,
		})
		return nil, nil
	}
	receiptSHA := SHA256Hex(receipt)
	journal.Write("applied", map[string]any{
		"toolCallId": toolCallID, "commandSha256": commandSHA, "sourceSha256": a.Hash, "sourceBytes": a.Bytes,
		"receiptSha256": receiptSHA, "receiptBytes": receiptBytes, "evidenceCount": len(verified.Evidence),
		"uncertain": verified.Uncertain, "usage": usage,
	})
	savings.Show(ctx, savings.LunaDelegating, savings.FormatBytes(float64(a.Bytes-receiptBytes)))

	details := map[string]any{}
	if original, ok := event["details"].(map[string]any); ok {
		for k, v := range original {
			details[k] = v
		}
	}
	details["evidencePreservingReducer"] = map[string]any{
		"schema": ReceiptSchema, "sourceSha256": a.Hash, "sourceBytes": a.Bytes, "receiptSha256": receiptSHA,
		"receiptBytes": receiptBytes, "evidenceCount": len(verified.Evidence), "uncertain": verified.Uncertain,
	}
	return map[string]any{"content": reducible.Project(receipt), "isError": isError, "details": details}, nil
}
