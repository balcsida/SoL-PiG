// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/evidence-preserving-reducer/archive.ts at 2195680 (NVIDIA, MIT).

package reducer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Archive describes one raw log stored under its content hash.
type Archive struct {
	Hash  string
	Bytes int // UTF-8 bytes
	Chars int // UTF-16 code units
	Lines int // 0 for an empty body, else count of "\n" plus 1
	Path  string
}

// ArchiveBody ports archiveBody. An existing object with the same name but
// different bytes is an integrity failure, not a cache hit.
func ArchiveBody(storeRoot, body string) (Archive, error) {
	hash := SHA256Hex(body)
	dir := filepath.Join(storeRoot, "objects", hash[:2])
	path := filepath.Join(dir, hash+".txt")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Archive{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	switch {
	case err == nil:
		_, werr := file.WriteString(body)
		if cerr := file.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return Archive{}, werr
		}
	case errors.Is(err, os.ErrExist):
		existing, rerr := os.ReadFile(path)
		if rerr != nil {
			return Archive{}, rerr
		}
		if !bytes.Equal(existing, []byte(body)) {
			return Archive{}, errors.New("Reducer archive integrity failure: " + path)
		}
	default:
		return Archive{}, err
	}
	lines := 0
	if body != "" {
		lines = strings.Count(body, "\n") + 1
	}
	return Archive{Hash: hash, Bytes: len(body), Chars: UTF16Len(body), Lines: lines, Path: path}, nil
}
