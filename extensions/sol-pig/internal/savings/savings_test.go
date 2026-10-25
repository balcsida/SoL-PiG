// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package savings

import (
	"math"
	"reflect"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/testhost"
)

func TestFormatCount(t *testing.T) {
	cases := map[float64]string{
		12345:      "12,345 context tokens avoided",
		-3:         "0 context tokens avoided",
		math.NaN(): "0 context tokens avoided",
		999.6:      "1,000 context tokens avoided",
	}
	for in, want := range cases {
		if got := FormatCount(in, "context tokens avoided"); got != want {
			t.Errorf("FormatCount(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[float64]string{
		78200:   "76.4 KiB removed from future prompts",
		1048576: "1 MiB removed from future prompts",
		512:     "512 B removed from future prompts",
	}
	for in, want := range cases {
		if got := FormatBytes(in); got != want {
			t.Errorf("FormatBytes(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBannerLines(t *testing.T) {
	got := BannerLines(ActionFusion, "1 model round-trip avoided", []string{"row"})
	want := []string{"⚡ SoL-PiG · Action Fusion", "Money saved · 1 model round-trip avoided", "row"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestIsNestedToolCallID(t *testing.T) {
	for id, want := range map[string]bool{"toolu_01AbC/1": true, "cm1/2/12": true, "toolu_01AbC": false, "call_abc|fc_def": false, "call-1:then_run": false} {
		if got := IsNestedToolCallID(id); got != want {
			t.Errorf("IsNestedToolCallID(%q) = %v, want %v", id, got, want)
		}
	}
}

func showingExtension() *sdk.Extension {
	ext := sdk.New("probe")
	ext.Tool("show", "show", sdk.Schema{"type": "object"}, func(ctx sdk.Context, p map[string]any) (any, error) {
		Show(ctx, ObservationPack, "12 context tokens avoided")
		return "ok", nil
	})
	return ext
}

func TestShow(t *testing.T) {
	old := StatusDuration
	StatusDuration = 20 * time.Millisecond
	t.Cleanup(func() { StatusDuration = old })
	h := testhost.Start(t, showingExtension(), testhost.Options{Cwd: t.TempDir(), Mode: "tui"})
	if res := h.CallTool("show", "c1", map[string]any{}); res.Err != nil {
		t.Fatal(res.Err)
	}
	notify := h.Calls("ui.notify")
	if len(notify) != 1 || notify[0].Args["message"] != "⚡ SoL-PiG · Observation Pack\nMoney saved · 12 context tokens avoided" || notify[0].Args["level"] != "info" {
		t.Fatalf("notify = %+v", notify)
	}
	status := h.Calls("ui.setStatus")
	if len(status) != 1 || status[0].Args["key"] != StatusKey || status[0].Args["text"] != "⚡ Observation Pack · 12 context tokens avoided" {
		t.Fatalf("setStatus = %+v", status)
	}
	time.Sleep(100 * time.Millisecond)
	status = h.Calls("ui.setStatus")
	if _, hasText := status[len(status)-1].Args["text"]; len(status) != 2 || status[1].Args["key"] != StatusKey || hasText {
		t.Fatalf("after the delay the status must be removed (setStatus with the key only), got %+v", status)
	}
}

// PiG 0.5.3 never sends the run mode, so the TUI reports the SDK default "print" with hasUI true.
func TestShowNotifiesWhenHostOmitsMode(t *testing.T) {
	h := testhost.Start(t, showingExtension(), testhost.Options{Cwd: t.TempDir(), Mode: "print"})
	if res := h.CallTool("show", "c1", map[string]any{}); res.Err != nil {
		t.Fatal(res.Err)
	}
	if n, s := h.Calls("ui.notify"), h.Calls("ui.setStatus"); len(n) != 1 || len(s) != 1 {
		t.Fatalf("notify = %+v, setStatus = %+v", n, s)
	}
}

func TestShowSilentWithoutUI(t *testing.T) {
	h := testhost.Start(t, showingExtension(), testhost.Options{Cwd: t.TempDir(), Mode: "print", HasUI: new(bool)})
	if res := h.CallTool("show", "c1", map[string]any{}); res.Err != nil {
		t.Fatal(res.Err)
	}
	if n, s := h.Calls("ui.notify"), h.Calls("ui.setStatus"); len(n) != 0 || len(s) != 0 {
		t.Fatalf("notify = %+v, setStatus = %+v", n, s)
	}
}

func TestShowSilentOutsideTUI(t *testing.T) {
	h := testhost.Start(t, showingExtension(), testhost.Options{Cwd: t.TempDir(), Mode: "rpc"})
	if res := h.CallTool("show", "c1", map[string]any{}); res.Err != nil {
		t.Fatal(res.Err)
	}
	if n, s := h.Calls("ui.notify"), h.Calls("ui.setStatus"); len(n) != 0 || len(s) != 0 {
		t.Fatalf("notify = %+v, setStatus = %+v", n, s)
	}
}
