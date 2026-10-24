// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/action-fusion/index.ts and src/sol-pi/shell-options.ts at 2195680 (NVIDIA, MIT).

package actionfusion

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/savings"
)

const (
	editThenRunDescription  = "Command to run next on this file after the edit succeeds — e.g. run, build, start/restart, install, or check it; optional timeout in seconds. Skipped if the edit fails; a non-zero exit is reported but keeps the edit."
	writeThenRunDescription = "Command to run next on this file after the write succeeds — e.g. run, build, start/restart, install, or check it; optional timeout in seconds. Skipped if the write fails; a non-zero exit is reported but keeps the write."
)

// ShellOptions are Pi's file-backed shell settings; nil means unset.
type ShellOptions struct{ ShellPath, CommandPrefix *string }

// LoadShellOptions inherits Pi's shell settings without trusting unapproved project settings.
func LoadShellOptions(ctx sdk.Context, agentDir string, projectTrusted bool) (ShellOptions, error) {
	settings, err := sdk.SettingsManagerCreate(ctx, ctx.Cwd(), &agentDir, &sdk.SettingsManagerCreateOptions{ProjectTrusted: sdk.Bool(projectTrusted)})
	if err != nil {
		return ShellOptions{}, err
	}
	drained, err := settings.DrainErrors()
	if err != nil {
		return ShellOptions{}, err
	}
	if len(drained) > 0 {
		msgs := make([]string, len(drained))
		for i, e := range drained {
			msgs[i] = e.Error.Error()
		}
		return ShellOptions{}, fmt.Errorf("Action Fusion could not load Pi shell settings: %s", strings.Join(msgs, "; "))
	}
	var o ShellOptions
	if o.ShellPath, err = settings.GetShellPath(); err != nil {
		return ShellOptions{}, err
	}
	if o.CommandPrefix, err = settings.GetShellCommandPrefix(); err != nil {
		return ShellOptions{}, err
	}
	return o, nil
}

// builtins holds the built-in definitions for one cwd; they capture the cwd, so they are kept per cwd.
type builtins struct{ edit, write, bash sdk.BuiltinToolDefinition }

type fusion struct {
	shell ShellOptions
	mu    sync.Mutex
	cache map[string]builtins
}

func (f *fusion) forCwd(cwd string) (builtins, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.cache[cwd]; ok {
		return b, nil
	}
	bashOptions := &sdk.BashToolOptions{}
	if f.shell.ShellPath != nil {
		bashOptions.ShellPath = *f.shell.ShellPath
	}
	if f.shell.CommandPrefix != nil {
		bashOptions.CommandPrefix = *f.shell.CommandPrefix
	}
	var b builtins
	var err error
	if b.edit, err = sdk.CreateEditToolDefinition(cwd, nil); err != nil {
		return b, err
	}
	if b.write, err = sdk.CreateWriteToolDefinition(cwd, nil); err != nil {
		return b, err
	}
	if b.bash, err = sdk.CreateBashToolDefinition(cwd, bashOptions); err != nil {
		return b, err
	}
	f.cache[cwd] = b
	return b, nil
}

func thenRunSchema(description string) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": description,
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "Bash command to run"},
			"timeout": map[string]any{"type": "number", "description": "Timeout in seconds (optional, no default timeout)"},
		},
		"required": []any{"command"},
	}
}

// withThenRun is the built-in definition with an optional then_run parameter; everything else is inherited.
func withThenRun(base sdk.BuiltinToolDefinition, description string) (sdk.ToolDefinition, error) {
	def := base.ToolDefinition
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		return def, fmt.Errorf("built-in %s tool has no parameter properties", def.Name)
	}
	props = maps.Clone(props)
	props["then_run"] = thenRunSchema(description)
	def.Parameters = maps.Clone(def.Parameters)
	def.Parameters["properties"] = props
	return def, nil
}

func (f *fusion) execute(pick func(builtins) sdk.BuiltinToolDefinition) sdk.ToolFunc {
	return func(ctx sdk.Context, params map[string]any) (any, error) {
		b, err := f.forCwd(ctx.Cwd())
		if err != nil {
			return nil, err
		}
		thenRun, rest := ThenRunFromParams(params)
		if raw, present := params["then_run"]; present && raw != nil && thenRun == nil {
			return nil, errors.New("then_run must be an object with a command")
		}
		path, _ := rest["path"].(string)
		abs, err := ResolveToolPath(ctx.Cwd(), path)
		if err != nil {
			return nil, err
		}
		id := ctx.ToolCallID()
		// The run's abort signal (nil when no run is active) so Esc stops the mutation and the command.
		signal := ctx.Signal()
		result, status, err := Run(Fused{
			AbsolutePath: abs,
			ThenRun:      thenRun,
			Mutate:       func() (sdk.AgentToolResult, error) { return pick(b).ExecuteWith(ctx, id, rest, signal, nil) },
			RunBash: func(p map[string]any) (sdk.AgentToolResult, error) {
				return b.bash.ExecuteWith(ctx, id+":then_run", p, signal, nil)
			},
		})
		if err != nil {
			return nil, err
		}
		if status == StatusSucceeded && !savings.IsNestedToolCallID(id) {
			savings.Show(ctx, savings.ActionFusion, "1 model round-trip avoided")
		}
		return result, nil
	}
}

// Register overrides the built-in edit and write tools with versions that take then_run.
func Register(ext *sdk.Extension, ctx sdk.Context, shell ShellOptions) error {
	f := &fusion{shell: shell, cache: map[string]builtins{}}
	b, err := f.forCwd(ctx.Cwd())
	if err != nil {
		return err
	}
	edit, err := withThenRun(b.edit, editThenRunDescription)
	if err != nil {
		return err
	}
	edit.Execute = f.execute(func(b builtins) sdk.BuiltinToolDefinition { return b.edit })
	write, err := withThenRun(b.write, writeThenRunDescription)
	if err != nil {
		return err
	}
	write.Execute = f.execute(func(b builtins) sdk.BuiltinToolDefinition { return b.write })
	ext.RegisterTool(edit)
	ext.RegisterTool(write)
	return nil
}
