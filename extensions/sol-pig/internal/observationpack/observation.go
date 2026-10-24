// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/observation-pack/observation.ts at 2195680 (NVIDIA, MIT).

// Package observationpack archives large tool results and replaces them in
// outgoing requests with a stable placeholder after their first two sends,
// with exact paged recall through obs_recall.
package observationpack

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

// Thresholds and limits, as SoL-Pi defines them.
const (
	ThresholdBytes          = 10 * 1024 // only tool results above this participate
	FullSends               = 2         // provider requests that still carry the full payload
	PlaceholderExcerptBytes = 1024      // split evenly between head and tail, whole lines only
	RecallMaxBytes          = 16 * 1024
	RecallMaxLines          = 400
	RecallHeaderReserve     = 512
	RecallHeaderLines       = 2
	ReceiptPrefixLine       = "sol_pi_evidence_receipt_v1" // reducer receipts are never packed
	charsPerToken           = 4
)

var observationID = regexp.MustCompile(`^obs_[a-f0-9]{24}$`)

// Observation is one archived tool result.
type Observation struct {
	ID, ToolName, ToolCallID, Text, ContentHash string
	Bytes, Lines, Tokens                        int
}

// IsObservationID reports whether id has the obs_<24 hex> shape.
func IsObservationID(id string) bool { return observationID.MatchString(id) }

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// NewObservation joins the text blocks with "\n". ok is false when a line
// equals ReceiptPrefixLine or the text is not above ThresholdBytes.
func NewObservation(toolName, toolCallID string, textBlocks []string) (Observation, bool) {
	text := strings.Join(textBlocks, "\n")
	for _, line := range strings.Split(text, "\n") {
		if line == ReceiptPrefixLine {
			return Observation{}, false
		}
	}
	if len(text) <= ThresholdBytes {
		return Observation{}, false
	}
	contentHash := sha256Hex([]byte(text))
	id := "obs_" + sha256Hex([]byte(toolName + "\x00" + toolCallID + "\x00" + contentHash))[:24]
	return Observation{
		ID: id, ToolName: toolName, ToolCallID: toolCallID, Text: text, ContentHash: contentHash,
		Bytes: len(text), Lines: CountLines(text), Tokens: EstimateTokens(text),
	}, true
}

// CountLines is 0 for "", else the newline count plus one when the text does not end with one.
func CountLines(text string) int {
	if text == "" {
		return 0
	}
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}

// EstimateTokens is ceil(length/4) with length in UTF-16 code units, SoL-Pi's JS semantics.
func EstimateTokens(text string) int {
	units := 0
	for _, r := range text {
		units += utf16.RuneLen(r) // 2 for a supplementary-plane rune; invalid bytes decode as U+FFFD, 1 unit
	}
	return (units + charsPerToken - 1) / charsPerToken
}

// ObjectPath is where an observation's bytes live under the runtime root.
func ObjectPath(root, id string) string {
	return filepath.Join(root, "observation-pack", "objects", id+".txt")
}

// EnsureStored writes the observation to its content-addressed path, refusing
// symlinks and verifying an existing object byte for byte before reusing it.
func EnsureStored(root string, o Observation) error {
	if root == "" {
		return errors.New("SoL-Pi runtime directory is unavailable")
	}
	path := ObjectPath(root, o.ID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("Observation directory is not a regular directory for %s", o.ID)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
	if err == nil {
		_, werr := f.WriteString(o.Text)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return werr
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	existing, err := os.OpenFile(path, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return err
	}
	defer existing.Close()
	stat, err := existing.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() {
		return fmt.Errorf("Content-addressed observation is not a regular file for %s", o.ID)
	}
	if stat.Size() != int64(o.Bytes) {
		return fmt.Errorf("Content-addressed observation size mismatch for %s", o.ID)
	}
	content, err := io.ReadAll(existing)
	if err != nil {
		return err
	}
	if sha256Hex(content) != o.ContentHash {
		return fmt.Errorf("Content-addressed observation hash mismatch for %s", o.ID)
	}
	return nil
}

// CompleteLineExcerpt takes whole lines (newline kept) from the start, or from
// the end, while their UTF-8 bytes fit the budget; a line that would exceed it stops the scan.
func CompleteLineExcerpt(text string, budget int, fromEnd bool) string {
	lines := strings.SplitAfter(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var selected []string
	used := 0
	if fromEnd {
		for i := len(lines) - 1; i >= 0; i-- {
			if used+len(lines[i]) > budget {
				break
			}
			selected = append([]string{lines[i]}, selected...)
			used += len(lines[i])
		}
	} else {
		for _, line := range lines {
			if used+len(line) > budget {
				break
			}
			selected = append(selected, line)
			used += len(line)
		}
	}
	return strings.Join(selected, "")
}

// Placeholder is the stable text that replaces a packed tool result.
func Placeholder(o Observation) string {
	headBudget := PlaceholderExcerptBytes / 2
	tailBudget := PlaceholderExcerptBytes - headBudget
	return strings.Join([]string{
		fmt.Sprintf("[large tool result replaced after its first %d provider requests]", FullSends),
		"id: " + o.ID,
		"tool: " + o.ToolName,
		fmt.Sprintf("original_bytes: %d", o.Bytes),
		fmt.Sprintf("original_lines: %d", o.Lines),
		fmt.Sprintf("estimated_tokens: %d", o.Tokens),
		fmt.Sprintf(`retrieve: call obs_recall with {"id":%q,"offset":0}; continue with returned next_offset`, o.ID),
		fmt.Sprintf("[first complete lines, up to %d bytes]", headBudget),
		CompleteLineExcerpt(o.Text, headBudget, false),
		fmt.Sprintf("[middle omitted; last complete lines, up to %d bytes]", tailBudget),
		CompleteLineExcerpt(o.Text, tailBudget, true),
		fmt.Sprintf("[%d original bytes omitted]", o.Bytes),
	}, "\n")
}

// Chunk is one page of an archived observation.
type Chunk struct {
	Text         string
	Bytes, Lines int
	NextOffset   int64
	EOF          bool
}

// ReadRecallChunk reads up to 15,872 bytes and 398 lines from offset, ending
// on a UTF-8 character boundary. The start offset is used as given.
func ReadRecallChunk(path string, offset int64) (Chunk, error) {
	const maxBytes = RecallMaxBytes - RecallHeaderReserve
	const maxLines = RecallMaxLines - RecallHeaderLines
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return Chunk{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return Chunk{}, err
	}
	if !stat.Mode().IsRegular() {
		return Chunk{}, errors.New("Stored observation is not a regular file")
	}
	size := stat.Size()
	if offset > size {
		return Chunk{}, fmt.Errorf("Offset %d exceeds observation size %d", offset, size)
	}
	available := max(int64(0), size-offset)
	buf := make([]byte, min(available, int64(maxBytes+4)))
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return Chunk{}, err
	}
	end := min(n, maxBytes)
	newlines := 0
	for i := 0; i < end; i++ {
		if buf[i] != '\n' {
			continue
		}
		newlines++
		if newlines == maxLines {
			end = i + 1
			break
		}
	}
	for end > 0 && end < n && buf[end]&0xC0 == 0x80 {
		end--
	}
	chunk := buf[:end]
	next := offset + int64(len(chunk))
	return Chunk{Text: string(chunk), Bytes: len(chunk), Lines: CountLines(string(chunk)), NextOffset: next, EOF: next >= size}, nil
}
