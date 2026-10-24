package actionfusion

import (
	"reflect"
	"testing"
)

func TestPrepareEditArguments(t *testing.T) {
	one := map[string]any{"oldText": "a", "newText": "b"}
	thenRun := map[string]any{"command": "true"}
	tests := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{"legacy top-level pair", map[string]any{"path": "f", "oldText": "a", "newText": "b", "then_run": thenRun},
			map[string]any{"path": "f", "edits": []any{one}, "then_run": thenRun}},
		{"legacy pair appended to edits", map[string]any{"path": "f", "edits": []any{map[string]any{"oldText": "x", "newText": "y"}}, "oldText": "a", "newText": "b"},
			map[string]any{"path": "f", "edits": []any{map[string]any{"oldText": "x", "newText": "y"}, one}}},
		{"edits as a JSON array string", map[string]any{"path": "f", "edits": `[{"oldText":"a","newText":"b"}]`},
			map[string]any{"path": "f", "edits": []any{one}}},
		{"edits as a JSON object string", map[string]any{"path": "f", "edits": `{"oldText":"a","newText":"b"}`},
			map[string]any{"path": "f", "edits": []any{one}}},
		{"edits as a single object", map[string]any{"path": "f", "edits": one},
			map[string]any{"path": "f", "edits": []any{one}}},
		{"valid edits array passes through", map[string]any{"path": "f", "edits": []any{one}, "then_run": thenRun},
			map[string]any{"path": "f", "edits": []any{one}, "then_run": thenRun}},
		{"non-JSON edits string with a pair", map[string]any{"path": "f", "edits": "not json", "oldText": "a", "newText": "b"},
			map[string]any{"path": "f", "edits": []any{one}}},
		{"single-object edits with a pair", map[string]any{"path": "f", "edits": map[string]any{"oldText": "x", "newText": "y"}, "oldText": "a", "newText": "b"},
			map[string]any{"path": "f", "edits": []any{map[string]any{"oldText": "x", "newText": "y"}, one}}},
		{"unusable shapes pass through for schema validation", map[string]any{"path": "f", "edits": "not json", "oldText": 1},
			map[string]any{"path": "f", "edits": "not json", "oldText": 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PrepareEditArguments(tc.in)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
