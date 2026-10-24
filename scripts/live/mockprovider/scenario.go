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

var scenarios = map[string][]step{
	// ObservationPack: a ~27 KB bash result is sent in full twice, then placeholdered and recalled.
	"observation-pack": {
		toolCall("bash", map[string]any{"command": "yes 'abcdefghijklmnopqrstuvwxyz 0123456789 padding padding' | head -n 500"}),
		toolCall("bash", map[string]any{"command": "echo second-step"}),
		toolCall("bash", map[string]any{"command": "echo third-step"}),
		recallObservation,
		textReply("All done."),
	},
}
