// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/online-context-compact/tools.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func stringArraySchema(maxItems int) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 1000}, "maxItems": maxItems}
}

func planParameters() sdk.Schema {
	boundedText := map[string]any{"type": "string", "minLength": 1, "maxLength": maxPlanStringBytes}
	statuses := make([]any, len(planStatuses))
	for i, status := range planStatuses {
		statuses[i] = map[string]any{"const": status, "type": "string"}
	}
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"steps": map[string]any{
				"type": "array", "minItems": 1, "maxItems": maxPlanSteps,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": boundedText, "goal": boundedText, "status": map[string]any{"anyOf": statuses},
					},
					"required":             []any{"id", "goal", "status"},
					"additionalProperties": false,
				},
			},
			"progress": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"files_changed": stringArraySchema(128), "verification": stringArraySchema(64), "decisions": stringArraySchema(64),
				},
				"required":             []any{"files_changed", "verification", "decisions"},
				"additionalProperties": false,
			},
		},
		"required":             []any{"steps"},
		"additionalProperties": false,
	}
}

func (o *online) registerTool(ext *sdk.Extension) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "update_plan",
		Label:         "Update plan",
		Description:   "Replace the complete working plan. Completing a previously registered unfinished step becomes a safe point where SoL-Pi may compact context if doing so is economical. Newly introduced completed steps are recorded as history.",
		PromptSnippet: "Keep the working plan current",
		PromptGuidelines: []string{
			"Send the complete plan on every update_plan call.",
			"Preserve step IDs and register unfinished steps before reporting their completion.",
			"Keep at most one step in_progress and mark finished steps completed.",
			"When completing a step, include concise progress evidence when available.",
		},
		Exposure:      sdk.ToolExposureModelOnly,
		ExecutionMode: "sequential",
		RenderShell:   sdk.ToolRenderShellSelf,
		Parameters:    planParameters(),
		Execute:       o.updatePlan,
	})
}

// progressSummary ports progressSummary: nothing unless progress was given and the step exists.
func progressSummary(steps []Step, params map[string]any, completedID string) *Progress {
	given, ok := params["progress"].(map[string]any)
	index := -1
	for i, s := range steps {
		if s.ID == completedID {
			index = i
			break
		}
	}
	if !ok || index < 0 {
		return nil
	}
	p := Progress{StepID: completedID, Goal: steps[index].Goal, NextWork: []string{}}
	p.FilesChanged, _ = stringArray(given["files_changed"])
	p.Verification, _ = stringArray(given["verification"])
	p.Decisions, _ = stringArray(given["decisions"])
	for _, s := range steps {
		if s.Status != "completed" {
			p.NextWork = append(p.NextWork, s.Goal)
		}
	}
	return &p
}

// updatePlan ports updatePlan.
func (o *online) updatePlan(ctx sdk.Context, params map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensureRestored(ctx)
	if runAborted(ctx) {
		return nil, errors.New("Plan update was aborted")
	}
	steps, ok := ParsePlanSteps(params["steps"])
	if !ok || len(steps) == 0 {
		return nil, errors.New("Plan must contain at least one valid step")
	}

	transition := AnalyzePlanTransition(o.state.Plan, steps)
	boundary := len(transition.CompletedIDs) > 0
	if boundary {
		o.state = o.state.RecordBoundary(steps, progressSummary(steps, params, transition.CompletedIDs[0]))
		if o.pendingBoundary == "" {
			o.pendingBoundary = ctx.ToolCallID()
		}
	} else {
		o.state.Plan = steps
	}
	o.save(ctx)

	return sdk.ToolResult{
		Content: strings.Join(append([]string{FormatPlanSnapshot(steps)}, transition.Advice...), "\n"),
		Details: map[string]any{
			"boundary":           boundary,
			"completed_step_ids": transition.CompletedIDs,
			"progress_recorded":  boundary && params["progress"] != nil,
			"task_status":        "active",
			"plan":               steps,
		},
	}, nil
}
