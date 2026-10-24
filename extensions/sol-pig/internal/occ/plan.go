// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/online-context-compact/plan.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"encoding/json"
	"fmt"
	"slices"
)

const (
	maxPlanSteps       = 128
	maxPlanStringBytes = 16384
)

// Step is one plan step; the field order fixes the JSON key order id, goal, status.
type Step struct {
	ID     string `json:"id"`
	Goal   string `json:"goal"`
	Status string `json:"status"`
}

var planStatuses = []string{"pending", "in_progress", "completed"}

func boundedString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && s != "" && len(s) <= maxPlanStringBytes
}

// ParsePlanSteps ports parsePlanSteps; value is a JSON-decoded array.
func ParsePlanSteps(value any) ([]Step, bool) {
	items, ok := value.([]any)
	if !ok || len(items) > maxPlanSteps {
		return nil, false
	}
	steps := make([]Step, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok || len(m) != 3 {
			return nil, false
		}
		id, okID := boundedString(m["id"])
		goal, okGoal := boundedString(m["goal"])
		status, _ := m["status"].(string)
		if !okID || !okGoal || !slices.Contains(planStatuses, status) || seen[id] {
			return nil, false
		}
		seen[id] = true
		steps = append(steps, Step{id, goal, status})
	}
	return steps, true
}

// Transition ports PlanTransition; completed steps are reported by id.
type Transition struct{ CompletedIDs, Advice []string }

// AnalyzePlanTransition ports analyzePlanTransition.
func AnalyzePlanTransition(previous, next []Step) Transition {
	byID := make(map[string]Step, len(previous))
	for _, s := range previous {
		byID[s.ID] = s
	}
	t := Transition{CompletedIDs: []string{}, Advice: []string{}}
	inProgress, pending := 0, false
	for _, s := range next {
		// A newly introduced completed step establishes history, not a status transition.
		if prior, ok := byID[s.ID]; ok {
			if prior.Status != "completed" && s.Status == "completed" {
				t.CompletedIDs = append(t.CompletedIDs, s.ID)
			}
			if prior.Goal != s.Goal {
				quoted, _ := json.Marshal(s.ID)
				t.Advice = append(t.Advice, fmt.Sprintf("Plan step %s changed goal; reuse an id only for the same goal.", quoted))
			}
		}
		switch s.Status {
		case "in_progress":
			inProgress++
		case "pending":
			pending = true
		}
	}
	if inProgress > 1 {
		t.Advice = append(t.Advice, "Keep at most one plan step in_progress.")
	}
	if inProgress == 0 && pending {
		t.Advice = append(t.Advice, "Mark one pending plan step in_progress before starting it.")
	}
	return t
}

// FormatPlanSnapshot ports formatPlanSnapshot.
func FormatPlanSnapshot(steps []Step) string {
	if steps == nil {
		steps = []Step{}
	}
	raw, _ := json.Marshal(struct {
		Steps []Step `json:"steps"`
	}{steps})
	return `<sol-pi-plan task_status="active">` + string(raw) + `</sol-pi-plan>`
}
