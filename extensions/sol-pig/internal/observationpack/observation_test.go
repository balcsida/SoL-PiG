// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package observationpack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func numberedText(lines int) (string, []string) {
	parts := make([]string, lines)
	for i := range parts {
		parts[i] = fmt.Sprintf("line %05d %s\n", i+1, strings.Repeat("x", 38)) // 50 bytes each
	}
	return strings.Join(parts, ""), parts
}

func TestNewObservationThreshold(t *testing.T) {
	if _, ok := NewObservation("bash", "c1", []string{strings.Repeat("a", ThresholdBytes)}); ok {
		t.Fatal("10240 bytes must not participate")
	}
	text := strings.Repeat("a", ThresholdBytes+1)
	o, ok := NewObservation("bash", "c1", []string{text})
	if !ok || o.Bytes != ThresholdBytes+1 || o.Lines != 1 || !IsObservationID(o.ID) || o.ToolName != "bash" || o.Text != text {
		t.Fatalf("observation = %+v ok=%v", o, ok)
	}
	again, _ := NewObservation("bash", "c1", []string{text})
	other, _ := NewObservation("bash", "c2", []string{text})
	if again.ID != o.ID || other.ID == o.ID {
		t.Fatalf("ids: %s %s %s", o.ID, again.ID, other.ID)
	}
	if o.Tokens != (ThresholdBytes+1+3)/4 {
		t.Fatalf("tokens = %d", o.Tokens)
	}
}

func TestNewObservationSkipsReceipts(t *testing.T) {
	big := strings.Repeat("b", ThresholdBytes+1)
	if _, ok := NewObservation("bash", "c1", []string{"x\n" + ReceiptPrefixLine + "\n" + big}); ok {
		t.Fatal("a receipt line must skip packing")
	}
	if _, ok := NewObservation("bash", "c1", []string{"x\n" + ReceiptPrefixLine + " trailer\n" + big}); !ok {
		t.Fatal("only an exact receipt line skips packing")
	}
}

func TestPlaceholderFormat(t *testing.T) {
	text, lines := numberedText(400)
	o, ok := NewObservation("bash", "call-1", []string{text})
	if !ok || o.Bytes != 20000 || o.Lines != 400 || o.Tokens != 5000 {
		t.Fatalf("observation = %+v ok=%v", o, ok)
	}
	want := strings.Join([]string{
		"[large tool result replaced after its first 2 provider requests]",
		"id: " + o.ID,
		"tool: bash",
		"original_bytes: 20000",
		"original_lines: 400",
		"estimated_tokens: 5000",
		`retrieve: call obs_recall with {"id":"` + o.ID + `","offset":0}; continue with returned next_offset`,
		"[first complete lines, up to 512 bytes]",
		strings.Join(lines[:10], ""),
		"[middle omitted; last complete lines, up to 512 bytes]",
		strings.Join(lines[390:], ""),
		"[20000 original bytes omitted]",
	}, "\n")
	if got := Placeholder(o); got != want {
		t.Fatalf("placeholder:\n%s\nwant:\n%s", got, want)
	}
	if Placeholder(o) != want {
		t.Fatal("placeholder is not stable")
	}
}

func TestCompleteLineExcerpt(t *testing.T) {
	text, lines := func() (string, []string) {
		parts := make([]string, 20)
		for i := range parts {
			parts[i] = strings.Repeat("y", 99) + "\n"
		}
		return strings.Join(parts, ""), parts
	}()
	if got := CompleteLineExcerpt(text, 512, false); got != strings.Join(lines[:5], "") {
		t.Fatalf("head = %d bytes", len(got))
	}
	if got := CompleteLineExcerpt(text, 512, true); got != strings.Join(lines[15:], "") {
		t.Fatalf("tail = %d bytes", len(got))
	}
	if got := CompleteLineExcerpt(strings.Repeat("z", 600), 512, false); got != "" {
		t.Fatalf("single long line = %q", got)
	}
	first := strings.Repeat("a", 400) + "\n"
	second := strings.Repeat("é", 60) + "\n" // 121 bytes: crosses the budget
	if got := CompleteLineExcerpt(first+second, 512, false); got != first || !utf8.ValidString(got) {
		t.Fatalf("multi-byte crossing = %q", got)
	}
}

func TestEnsureStored(t *testing.T) {
	text := strings.Repeat("c", ThresholdBytes+1)
	o, _ := NewObservation("bash", "c1", []string{text})

	t.Run("writes and reuses", func(t *testing.T) {
		root := t.TempDir()
		if err := EnsureStored(root, o); err != nil {
			t.Fatal(err)
		}
		path := ObjectPath(root, o.ID)
		if path != filepath.Join(root, "observation-pack", "objects", o.ID+".txt") {
			t.Fatalf("path = %s", path)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("object: %v %v", info, err)
		}
		if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
			t.Fatalf("objects dir mode = %v", dir.Mode().Perm())
		}
		if body, _ := os.ReadFile(path); string(body) != text {
			t.Fatal("object body differs")
		}
		if err := EnsureStored(root, o); err != nil {
			t.Fatalf("second store: %v", err)
		}
	})
	t.Run("hash mismatch", func(t *testing.T) {
		root := t.TempDir()
		path := ObjectPath(root, o.ID)
		os.MkdirAll(filepath.Dir(path), 0o700)
		os.WriteFile(path, []byte(strings.Repeat("d", ThresholdBytes+1)), 0o600)
		if err := EnsureStored(root, o); err == nil || !strings.Contains(err.Error(), "Content-addressed observation hash mismatch for "+o.ID) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("size mismatch", func(t *testing.T) {
		root := t.TempDir()
		path := ObjectPath(root, o.ID)
		os.MkdirAll(filepath.Dir(path), 0o700)
		os.WriteFile(path, []byte("short"), 0o600)
		if err := EnsureStored(root, o); err == nil || !strings.Contains(err.Error(), "Content-addressed observation size mismatch for "+o.ID) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("symlinked object", func(t *testing.T) {
		root := t.TempDir()
		path := ObjectPath(root, o.ID)
		os.MkdirAll(filepath.Dir(path), 0o700)
		target := filepath.Join(t.TempDir(), "target.txt")
		os.WriteFile(target, []byte("keep"), 0o600)
		os.Symlink(target, path)
		if err := EnsureStored(root, o); err == nil {
			t.Fatal("symlinked object accepted")
		}
		if body, _ := os.ReadFile(target); string(body) != "keep" {
			t.Fatal("wrote through the symlink")
		}
	})
	t.Run("symlinked directory", func(t *testing.T) {
		root := t.TempDir()
		elsewhere := t.TempDir()
		os.MkdirAll(filepath.Join(root, "observation-pack"), 0o700)
		os.Symlink(elsewhere, filepath.Join(root, "observation-pack", "objects"))
		err := EnsureStored(root, o)
		if err == nil || !strings.Contains(err.Error(), "Observation directory is not a regular directory for "+o.ID) {
			t.Fatalf("err = %v", err)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Fatal("wrote through the symlinked directory")
		}
	})
}

func TestReadRecallChunkPaging(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < 40*1024; i++ {
		fmt.Fprintf(&b, "ünïcödé lïne %d — ✓ %s\n", i, strings.Repeat("é", i%7))
	}
	original := b.String()
	path := filepath.Join(t.TempDir(), "obs.txt")
	os.WriteFile(path, []byte(original), 0o600)

	var got strings.Builder
	var offset int64
	for {
		chunk, err := ReadRecallChunk(path, offset)
		if err != nil {
			t.Fatal(err)
		}
		if chunk.Bytes > RecallMaxBytes-RecallHeaderReserve || chunk.Lines > RecallMaxLines-RecallHeaderLines || !utf8.ValidString(chunk.Text) || chunk.Bytes != len(chunk.Text) {
			t.Fatalf("chunk out of bounds: bytes=%d lines=%d valid=%v", chunk.Bytes, chunk.Lines, utf8.ValidString(chunk.Text))
		}
		got.WriteString(chunk.Text)
		if chunk.NextOffset != offset+int64(chunk.Bytes) {
			t.Fatalf("nextOffset %d != %d+%d", chunk.NextOffset, offset, chunk.Bytes)
		}
		offset = chunk.NextOffset
		if chunk.EOF {
			break
		}
	}
	if got.String() != original {
		t.Fatal("paged recall does not reproduce the original bytes")
	}
	mid := int64(strings.Index(original, "ü")) + 1 // inside a 2-byte character
	chunk, err := ReadRecallChunk(path, mid)
	if err != nil || chunk.NextOffset <= mid || chunk.Bytes == 0 {
		t.Fatalf("mid-character offset: %+v %v", chunk, err)
	}
	size := int64(len(original))
	if _, err := ReadRecallChunk(path, size+1); err == nil || err.Error() != fmt.Sprintf("Offset %d exceeds observation size %d", size+1, size) {
		t.Fatalf("err = %v", err)
	}
	if last, err := ReadRecallChunk(path, size); err != nil || !last.EOF || last.Bytes != 0 {
		t.Fatalf("offset at size: %+v %v", last, err)
	}
}
