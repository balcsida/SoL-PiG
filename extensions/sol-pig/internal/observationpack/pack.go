// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/observation-pack/index.ts at 2195680 (NVIDIA, MIT).

package observationpack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/paths"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/savings"
)

const recallSaving = "full observation replay avoided"

type pack struct {
	mu         sync.Mutex
	sentCounts map[string]int    // root + "\x00" + observation id → provider requests sent so far
	ledgers    map[string]Ledger // by runtime root
}

// Register adds the obs_recall tool, then the context handler that replaces
// large tool results after their first two sends. It never edits stored
// history: the placeholder goes into the outgoing request's message objects in
// place, so the host keeps mid-conversation system messages where they are.
func Register(ext *sdk.Extension) {
	p := &pack{sentCounts: map[string]int{}, ledgers: map[string]Ledger{}}
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "obs_recall",
		Label:         "Recall Observation",
		Description:   "Read a stored large tool result by observation id and byte offset.",
		PromptSnippet: "Recall a paged excerpt from a previously replaced large tool result",
		RenderShell:   sdk.ToolRenderShellSelf,
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"id":     map[string]any{"type": "string", "description": "Observation id from a placeholder"},
				"offset": map[string]any{"type": "integer", "minimum": 0, "description": "Byte offset, default 0"},
			},
			"required": []any{"id"},
		},
		Execute: p.recall,
		RenderCall: func(ctx sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
			return recallCallLines(args), nil
		},
		RenderResult: func(ctx sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
			details, _ := result.Details.(map[string]any)
			return recallResultLines(options.IsPartial, details), nil
		},
	})
	ext.OnEvent(sdk.EventContext, p.context)
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

func (p *pack) ledgerFor(root string) Ledger {
	p.mu.Lock()
	defer p.mu.Unlock()
	ledger, ok := p.ledgers[root]
	if !ok {
		ledger = Ledger{Path: filepath.Join(root, "observation-pack", "ledger.jsonl")}
		p.ledgers[root] = ledger
	}
	return ledger
}

func (p *pack) recall(ctx sdk.Context, params map[string]any) (any, error) {
	id, _ := params["id"].(string)
	if !IsObservationID(id) {
		return nil, fmt.Errorf("Unknown observation id: %s", id)
	}
	var offset int64
	if v, ok := params["offset"].(float64); ok {
		offset = int64(v)
	}
	root, err := runtimeRoot(ctx)
	if err != nil {
		return nil, err
	}
	chunk, err := ReadRecallChunk(ObjectPath(root, id), offset)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("Unknown observation id: %s", id)
		}
		return nil, err
	}
	header := fmt.Sprintf("[obs_recall id=%s offset=%d next_offset=%d eof=%t]\n[chunk_bytes=%d chunk_lines=%d; use next_offset to continue]",
		id, offset, chunk.NextOffset, chunk.EOF, chunk.Bytes, chunk.Lines)
	content := header + "\n" + chunk.Text
	if len(content) > RecallMaxBytes || CountLines(content) > RecallMaxLines {
		return nil, errors.New("Recall output exceeded its hard limit")
	}
	details := map[string]any{"id": id, "offset": offset, "bytes": chunk.Bytes, "lines": chunk.Lines, "nextOffset": chunk.NextOffset, "eof": chunk.EOF}
	entry := map[string]any{"event": "recall"}
	for k, v := range details {
		entry[k] = v
	}
	if err := p.ledgerFor(root).Append(entry); err != nil {
		return nil, err
	}
	return sdk.ToolResult{Content: content, Details: details}, nil
}

func recallCallLines(args map[string]any) []string {
	offset := 0
	if v, ok := args["offset"].(float64); ok {
		offset = int(v)
	}
	return savings.BannerLines(savings.ObservationPack, recallSaving, []string{fmt.Sprintf("Recall %v from byte %d", args["id"], offset)})
}

func recallResultLines(isPartial bool, details map[string]any) []string {
	base := "Recalling the requested slice..."
	if !isPartial {
		bytes, _ := details["bytes"].(float64)
		lines, _ := details["lines"].(float64)
		base = fmt.Sprintf("Recalled %d bytes across %d lines", int(bytes), int(lines))
	}
	return savings.BannerLines(savings.ObservationPack, recallSaving, []string{base})
}

// context runs once per provider request with the conversation the host is
// about to send. Returning nil keeps the in-place edits.
func (p *pack) context(ctx sdk.Context, data map[string]any) (any, error) {
	root, err := runtimeRoot(ctx)
	if err != nil {
		return nil, err
	}
	messages, _ := data["messages"].([]any)
	// How many provider requests each message has already been part of,
	// counted by the assistant messages that follow it.
	prior := make([]int, len(messages))
	assistants := 0
	for i := len(messages) - 1; i >= 0; i-- {
		prior[i] = assistants
		if m, ok := messages[i].(map[string]any); ok && m["role"] == "assistant" {
			assistants++
		}
	}
	requestIndex := assistants + 1
	for i, entry := range messages {
		message, texts, ok := pureTextResult(entry)
		if !ok {
			continue
		}
		if err := p.packMessage(ctx, root, message, texts, prior[i], requestIndex); err != nil {
			// Fail open: a packing failure must never cost the agent its observation.
			fmt.Fprintf(os.Stderr, "[observationpack] fail-open for tool result: %s\n", err)
		}
	}
	return nil, nil
}

// pureTextResult accepts a non-error toolResult whose content is a non-empty
// list of text blocks, and returns the block texts.
func pureTextResult(entry any) (map[string]any, []string, bool) {
	message, ok := entry.(map[string]any)
	if !ok || message["role"] != "toolResult" {
		return nil, nil, false
	}
	if isError, _ := message["isError"].(bool); isError {
		return nil, nil, false
	}
	blocks, ok := message["content"].([]any)
	if !ok || len(blocks) == 0 {
		return nil, nil, false
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		b, ok := block.(map[string]any)
		if !ok || b["type"] != "text" {
			return nil, nil, false
		}
		text, ok := b["text"].(string)
		if !ok {
			return nil, nil, false
		}
		texts = append(texts, text)
	}
	return message, texts, true
}

func (p *pack) packMessage(ctx sdk.Context, root string, message map[string]any, texts []string, priorAssistants, requestIndex int) error {
	toolName, _ := message["toolName"].(string)
	toolCallID, _ := message["toolCallId"].(string)
	o, ok := NewObservation(toolName, toolCallID, texts)
	if !ok {
		return nil
	}
	if err := EnsureStored(root, o); err != nil {
		return err
	}
	key := root + "\x00" + o.ID
	p.mu.Lock()
	previousSends, known := p.sentCounts[key]
	p.mu.Unlock()
	if !known {
		previousSends = priorAssistants
	}
	ledger := p.ledgerFor(root)
	if previousSends < FullSends {
		if err := ledger.Append(map[string]any{
			"event": "full", "id": o.ID, "request": requestIndex, "tool": o.ToolName,
			"originalBytes": o.Bytes, "originalLines": o.Lines, "originalTokens": o.Tokens, "contentHash": o.ContentHash,
		}); err != nil {
			return err
		}
		p.setSent(key, previousSends+1)
		return nil
	}
	placeholder := Placeholder(o)
	placeholderTokens := EstimateTokens(placeholder)
	removedTokens := max(0, o.Tokens-placeholderTokens)
	if err := ledger.Append(map[string]any{
		"event": "placeholder", "id": o.ID, "request": requestIndex, "sendNumber": previousSends + 1, "tool": o.ToolName,
		"originalBytes": o.Bytes, "originalLines": o.Lines, "originalTokens": o.Tokens,
		"placeholderBytes": len(placeholder), "placeholderTokens": placeholderTokens, "removedTokens": removedTokens,
	}); err != nil {
		return err
	}
	if previousSends == FullSends {
		savings.Show(ctx, savings.ObservationPack, savings.FormatCount(float64(removedTokens), "context tokens avoided"))
	}
	// Same message object, new content: the host keeps system-message positions.
	message["content"] = []any{map[string]any{"type": "text", "text": placeholder}}
	p.setSent(key, previousSends+1)
	return nil
}

func (p *pack) setSent(key string, count int) {
	p.mu.Lock()
	p.sentCounts[key] = count
	p.mu.Unlock()
}
