// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/evidence-preserving-reducer/provider.ts at 2195680 (NVIDIA, MIT).

package reducer

import (
	"errors"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ErrModelUnavailable is wrapped by the error CallReducer returns when the configured model does not exist.
var ErrModelUnavailable = errors.New("Reducer model is unavailable")

// ErrTimeout is returned when the call outlives Timeout or the run is aborted.
var ErrTimeout = errors.New("Reducer model call timed out")

// CallReducer asks the configured reducer model to read body. The model call
// has no cancel API, so it runs in a goroutine that is abandoned on timeout or
// abort; its buffered channel lets that goroutine finish without a reader.
func CallReducer(ctx sdk.Context, cfg Config, command string, isError bool, a Archive, body string) (ProviderResult, error) {
	registry := ctx.ModelRegistry()
	model, err := registry.Find(cfg.ReducerProvider, cfg.ReducerModel)
	if err != nil {
		return ProviderResult{}, err
	}
	if model == nil {
		return ProviderResult{}, &unavailableError{cfg.ReducerProvider + "/" + cfg.ReducerModel}
	}
	maxTokens := float64(MaxOutputTokens)
	if limit, ok := model["maxTokens"].(float64); ok && limit < maxTokens {
		maxTokens = limit
	}
	request := map[string]any{
		"systemPrompt": Instructions(),
		"messages": []any{map[string]any{
			"role":      "user",
			"content":   []any{map[string]any{"type": "text", "text": Input(command, isError, a, body)}},
			"timestamp": time.Now().UnixMilli(),
		}},
	}
	options := map[string]any{"cacheRetention": "none", "maxTokens": int(maxTokens), "sessionId": cfg.RunID, "timeoutMs": 90000}

	done := make(chan map[string]any, 1)
	go func() { done <- registry.Complete(model, request, options) }()

	var aborted <-chan struct{}
	if signal := ctx.Signal(); signal != nil {
		aborted = signal.Done()
	}
	timer := time.NewTimer(Timeout)
	defer timer.Stop()
	select {
	case reply := <-done:
		return providerResult(reply), nil
	case <-timer.C:
		return ProviderResult{}, ErrTimeout
	case <-aborted:
		return ProviderResult{}, ErrTimeout
	case <-ctx.Done():
		return ProviderResult{}, ErrTimeout
	}
}

type unavailableError struct{ model string }

func (e *unavailableError) Error() string { return ErrModelUnavailable.Error() + ": " + e.model }
func (e *unavailableError) Unwrap() error { return ErrModelUnavailable }

func providerResult(reply map[string]any) ProviderResult {
	stop, _ := reply["stopReason"].(string)
	var text strings.Builder
	blocks, _ := reply["content"].([]any)
	for _, item := range blocks {
		if block, ok := item.(map[string]any); ok && block["type"] == "text" {
			s, _ := block["text"].(string)
			text.WriteString(s)
		}
	}
	provider, _ := reply["provider"].(string)
	model, _ := reply["model"].(string)
	message, _ := reply["errorMessage"].(string)
	usage, _ := reply["usage"].(map[string]any)
	count := func(key string) int {
		n, _ := usage[key].(float64)
		return int(n)
	}
	return ProviderResult{
		OK:           stop != "error" && stop != "aborted",
		OutputText:   text.String(),
		Provider:     provider,
		Model:        model,
		StopReason:   stop,
		ErrorMessage: message,
		Usage: Usage{
			Input: count("input"), Output: count("output"), CacheRead: count("cacheRead"),
			CacheWrite: count("cacheWrite"), TotalTokens: count("totalTokens"),
		},
	}
}
