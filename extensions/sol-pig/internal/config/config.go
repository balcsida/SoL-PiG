// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/config.ts at 2195680 (NVIDIA, MIT).

// Package config loads sol-pi.json with SoL-Pi's schema, defaults, search
// order and validation messages.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the configuration file SoL-Pi and SoL-PiG read.
const FileName = "sol-pi.json"

const (
	defaultReducerProvider = "openai-codex"
	defaultReducerModel    = "gpt-5.6-luna"
	defaultRatio           = 12.5
)

// Config is the effective SoL-Pi configuration.
type Config struct {
	Version                   int
	ActionFusion              bool
	ObservationPack           bool
	EvidencePreservingReducer bool
	OnlineContextCompact      bool
	ReducerProvider           string
	ReducerModel              string
	CacheWriteReadRatio       float64
}

// Default is the configuration with no file present: every mechanism off.
func Default() Config {
	return Config{
		Version:             1,
		ReducerProvider:     defaultReducerProvider,
		ReducerModel:        defaultReducerModel,
		CacheWriteReadRatio: defaultRatio,
	}
}

var featureKeys = []string{"actionFusion", "observationPack", "evidencePreservingReducer", "onlineContextCompact"}

var knownKeys = map[string]bool{
	"version": true, "actionFusion": true, "observationPack": true, "evidencePreservingReducer": true,
	"onlineContextCompact": true, "evidencePreservingReducerModel": true, "evidencePreservingReducerProvider": true,
	"cacheWriteReadRatio": true,
}

// Load reads <cwd>/<projectDirName>/sol-pi.json when allowProject and it
// exists, else <agentDir>/sol-pi.json when it exists, else Default(). The
// project file replaces the global one; nothing is merged.
func Load(cwd, projectDirName, agentDir string, allowProject bool) (Config, error) {
	path := ""
	if allowProject {
		if p := filepath.Join(cwd, projectDirName, FileName); exists(p) {
			path = p
		}
	}
	if path == "" {
		if p := filepath.Join(agentDir, FileName); exists(p) {
			path = p
		}
	}
	if path == "" {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("Unable to read SoL-Pi config %s: %s", path, err)
	}
	return Parse(path, data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Parse validates one file's bytes with SoL-Pi's rules; path names the file in messages.
func Parse(path string, data []byte) (Config, error) {
	keys, record, err := decodeObject(data)
	if err != nil {
		return Config{}, fmt.Errorf("Unable to read SoL-Pi config %s: %s", path, err)
	}
	if record == nil {
		return Config{}, fmt.Errorf("SoL-Pi config must be a JSON object: %s", path)
	}
	for _, key := range keys {
		if !knownKeys[key] {
			return Config{}, fmt.Errorf("Unknown SoL-Pi config key: %s", key)
		}
	}
	var version float64
	if raw, ok := record["version"]; !ok || json.Unmarshal(raw, &version) != nil || version != 1 {
		return Config{}, fmt.Errorf("SoL-Pi config version must be 1: %s", path)
	}
	cfg := Default()
	flags := map[string]*bool{
		"actionFusion": &cfg.ActionFusion, "observationPack": &cfg.ObservationPack,
		"evidencePreservingReducer": &cfg.EvidencePreservingReducer, "onlineContextCompact": &cfg.OnlineContextCompact,
	}
	for _, key := range featureKeys {
		raw, ok := record[key]
		if !ok {
			continue
		}
		var value bool
		if json.Unmarshal(raw, &value) != nil || !isJSONBool(raw) {
			return Config{}, fmt.Errorf("SoL-Pi config %s must be boolean: %s", key, path)
		}
		*flags[key] = value
	}
	if raw, ok := record["cacheWriteReadRatio"]; ok {
		var ratio float64
		if json.Unmarshal(raw, &ratio) != nil || !isJSONNumber(raw) || math.IsInf(ratio, 0) || math.IsNaN(ratio) || ratio < 0 {
			return Config{}, fmt.Errorf("SoL-Pi config cacheWriteReadRatio must be a finite non-negative number: %s", path)
		}
		cfg.CacheWriteReadRatio = ratio
	}
	var err2 error
	if cfg.ReducerModel, err2 = stringValue(record, "evidencePreservingReducerModel", defaultReducerModel, path); err2 != nil {
		return Config{}, err2
	}
	if cfg.ReducerProvider, err2 = stringValue(record, "evidencePreservingReducerProvider", defaultReducerProvider, path); err2 != nil {
		return Config{}, err2
	}
	return cfg, nil
}

func stringValue(record map[string]json.RawMessage, key, fallback, path string) (string, error) {
	raw, ok := record[key]
	if !ok {
		return fallback, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || !isJSONString(raw) || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("SoL-Pi config %s must be a non-empty string: %s", key, path)
	}
	return strings.TrimSpace(value), nil
}

// decodeObject returns the top-level keys in file order and the raw values.
// record is nil when the document is valid JSON but not an object.
func decodeObject(data []byte) (keys []string, record map[string]json.RawMessage, err error) {
	var any json.RawMessage
	if err := json.Unmarshal(data, &any); err != nil {
		return nil, nil, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(any), []byte("{")) {
		return nil, nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(any))
	if _, err := dec.Token(); err != nil { // "{"
		return nil, nil, err
	}
	record = map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
		record[key] = value
	}
	return keys, record, nil
}

func isJSONBool(raw json.RawMessage) bool {
	s := string(bytes.TrimSpace(raw))
	return s == "true" || s == "false"
}

func isJSONNumber(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	return len(s) > 0 && s[0] != '"' && s[0] != 'n' && s[0] != 't' && s[0] != 'f' && s[0] != '[' && s[0] != '{'
}

func isJSONString(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	return len(s) > 0 && s[0] == '"'
}
