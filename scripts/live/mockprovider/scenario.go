// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package main

import "regexp"

// A step is what the scripted "model" does on one agent request.
type step struct {
	kind  string         // "tool", "text" or "dynamic"
	name  string         // tool name for "tool"
	args  map[string]any // tool arguments for "tool"
	text  string         // reply for "text"
	build func(serializedRequest string) step
}

func toolCall(name string, args map[string]any) step {
	return step{kind: "tool", name: name, args: args}
}

func textReply(value string) step { return step{kind: "text", text: value} }

var observationID = regexp.MustCompile(`obs_[a-f0-9]{24}`)

// recallObservation recalls the first placeholder in the request, or asks for "missing" so the agent still gets an answer.
var recallObservation = step{kind: "dynamic", build: func(serialized string) step {
	id := "missing"
	if m := observationID.FindString(serialized); m != "" {
		id = m
	}
	return toolCall("obs_recall", map[string]any{"id": id, "offset": 0})
}}

var occBashStep = toolCall("bash", map[string]any{"command": "yes 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' | head -n 1000"})

func occPlan(collect, report string) []map[string]any {
	return []map[string]any{
		{"id": "collect", "goal": "collect build output", "status": collect},
		{"id": "report", "goal": "write the report", "status": report},
		{"id": "verify", "goal": "verify the report", "status": "pending"},
	}
}

var scenarios = map[string][]step{
	// ObservationPack: a ~27 KB bash result is sent in full twice, then placeholdered and recalled.
	"observation-pack": {
		toolCall("bash", map[string]any{"command": "yes 'abcdefghijklmnopqrstuvwxyz 0123456789 padding padding' | head -n 500"}),
		toolCall("bash", map[string]any{"command": "echo second-step"}),
		toolCall("bash", map[string]any{"command": "echo third-step"}),
		recallObservation,
		textReply("All done."),
	},
	// ActionFusion: write then edit, each with a then_run command; the edit's command fails.
	"action-fusion": {
		toolCall("write", map[string]any{"path": "report.txt", "content": "hello from the fused write\n", "then_run": map[string]any{"command": "cat report.txt"}}),
		toolCall("edit", map[string]any{"path": "report.txt", "edits": []map[string]any{{"oldText": "hello", "newText": "bye"}}, "then_run": map[string]any{"command": "exit 7"}}),
		textReply("All done."),
	},
	// Reducer: a failing make target prints 300 lines, so the bash result is reduced to a verified receipt.
	"reducer": {
		toolCall("write", map[string]any{"path": "Makefile", "content": "check:\n\t@for i in $$(seq 1 300); do echo \"test_$$i ... ok\"; done; echo \"ERROR test target failed\"; exit 1\n"}),
		toolCall("bash", map[string]any{"command": "make check"}),
		textReply("All done."),
	},
	// Online Context Compact: a plan, five ~41 KB logs, then a completed step whose boundary compacts; every later request (the resumed turn too) answers.
	"occ": {
		toolCall("update_plan", map[string]any{"steps": occPlan("in_progress", "pending")}),
		occBashStep, occBashStep, occBashStep, occBashStep, occBashStep,
		toolCall("update_plan", map[string]any{"steps": occPlan("completed", "in_progress"),
			"progress": map[string]any{"files_changed": []string{}, "verification": []string{"collected five logs"}, "decisions": []string{}}}),
		textReply("All done."),
	},
}
