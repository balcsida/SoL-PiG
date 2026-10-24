// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	const path = "/cfg/sol-pi.json"
	want := func(mutate func(*Config)) Config { c := Default(); mutate(&c); return c }
	cases := []struct {
		name  string
		input string
		want  Config
		err   string
	}{
		{name: "defaults", input: `{"version":1}`, want: Default()},
		{name: "ratio zero", input: `{"version":1,"cacheWriteReadRatio":0}`, want: want(func(c *Config) { c.CacheWriteReadRatio = 0 })},
		{name: "ratio 3.25", input: `{"version":1,"cacheWriteReadRatio":3.25}`, want: want(func(c *Config) { c.CacheWriteReadRatio = 3.25 })},
		{name: "route trimmed", input: `{"version":1,"evidencePreservingReducerProvider":" openai "}`, want: want(func(c *Config) { c.ReducerProvider = "openai" })},
		{name: "all enabled", input: `{"version":1,"actionFusion":true,"observationPack":true,"evidencePreservingReducer":true,"onlineContextCompact":true}`, want: want(func(c *Config) {
			c.ActionFusion, c.ObservationPack, c.EvidencePreservingReducer, c.OnlineContextCompact = true, true, true, true
		})},
		{name: "unknown key", input: `{"version":1,"provider":"x"}`, err: "Unknown SoL-Pi config key: provider"},
		{name: "version 2", input: `{"version":2}`, err: "SoL-Pi config version must be 1: " + path},
		{name: "actionFusion string", input: `{"version":1,"actionFusion":"yes"}`, err: "SoL-Pi config actionFusion must be boolean: " + path},
		{name: "ratio null", input: `{"version":1,"cacheWriteReadRatio":null}`, err: "SoL-Pi config cacheWriteReadRatio must be a finite non-negative number: " + path},
		{name: "ratio string", input: `{"version":1,"cacheWriteReadRatio":"12.5"}`, err: "SoL-Pi config cacheWriteReadRatio must be a finite non-negative number: " + path},
		{name: "ratio negative", input: `{"version":1,"cacheWriteReadRatio":-1}`, err: "SoL-Pi config cacheWriteReadRatio must be a finite non-negative number: " + path},
		{name: "empty route", input: `{"version":1,"evidencePreservingReducerModel":"  "}`, err: "SoL-Pi config evidencePreservingReducerModel must be a non-empty string: " + path},
		{name: "null route", input: `{"version":1,"evidencePreservingReducerModel":null}`, err: "SoL-Pi config evidencePreservingReducerModel must be a non-empty string: " + path},
		{name: "array", input: `[1]`, err: "SoL-Pi config must be a JSON object: " + path},
		{name: "malformed", input: `{`, err: "Unable to read SoL-Pi config " + path + ": "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(path, []byte(tc.input))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want containing %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	write := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("no files", func(t *testing.T) {
		got, err := Load(t.TempDir(), ".pig", t.TempDir(), true)
		if err != nil || got != Default() {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("only global", func(t *testing.T) {
		agent := t.TempDir()
		write(t, filepath.Join(agent, FileName), `{"version":1,"actionFusion":true}`)
		got, err := Load(t.TempDir(), ".pig", agent, true)
		if err != nil || !got.ActionFusion {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("project replaces global when trusted", func(t *testing.T) {
		agent, cwd := t.TempDir(), t.TempDir()
		write(t, filepath.Join(agent, FileName), `{"version":1,"actionFusion":true}`)
		write(t, filepath.Join(cwd, ".pig", FileName), `{"version":1,"observationPack":true}`)
		got, err := Load(cwd, ".pig", agent, true)
		if err != nil || got.ActionFusion || !got.ObservationPack {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("project ignored when untrusted", func(t *testing.T) {
		agent, cwd := t.TempDir(), t.TempDir()
		write(t, filepath.Join(agent, FileName), `{"version":1,"actionFusion":true}`)
		write(t, filepath.Join(cwd, ".pig", FileName), `{"version":1,"observationPack":true}`)
		got, err := Load(cwd, ".pig", agent, false)
		if err != nil || !got.ActionFusion || got.ObservationPack {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("config path is a directory", func(t *testing.T) {
		agent := t.TempDir()
		path := filepath.Join(agent, FileName)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Load(t.TempDir(), ".pig", agent, true)
		if err == nil || !strings.HasPrefix(err.Error(), "Unable to read SoL-Pi config "+path+": ") {
			t.Fatalf("err = %v", err)
		}
	})
}
