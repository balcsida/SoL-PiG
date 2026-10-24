// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package reducer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestValidateReceiptReasons(t *testing.T) {
	body := "first line\nerror: boom\nlast line"
	a := Archive{Hash: SHA256Hex(body), Bytes: len(body), Lines: 3}
	receipt := func(status string, uncertain any, evidence any) string {
		raw, _ := json.Marshal(map[string]any{
			"schema": ReceiptSchema, "source_sha256": a.Hash, "status": status, "uncertain": uncertain, "evidence": evidence,
		})
		return string(raw)
	}
	item := func(kind, quote string) map[string]any { return map[string]any{"kind": kind, "quote": quote} }
	items := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = item("fatal", fmt.Sprintf("line%d", i))
		}
		return out
	}
	good := []any{item("fatal", "error: boom")}
	wrongSchema := strings.Replace(receipt("failure", false, good), ReceiptSchema, "other/1", 1)
	wrongHash := strings.Replace(receipt("failure", false, good), a.Hash, "deadbeef", 1)

	cases := []struct {
		name    string
		raw     string
		isError bool
		reason  string
	}{
		{"fenced json", "```json\n" + receipt("failure", false, good) + "\n```", true, "invalid-json"},
		{"empty", "", true, "invalid-json"},
		{"not an object", "[]", true, "schema-mismatch"},
		{"wrong schema", wrongSchema, true, "schema-mismatch"},
		{"wrong hash", wrongHash, true, "schema-mismatch"},
		{"wrong status", receipt("success", false, good), true, "schema-mismatch"},
		{"uncertain not boolean", receipt("failure", "no", good), true, "schema-mismatch"},
		{"evidence not array", receipt("failure", false, "x"), true, "schema-mismatch"},
		{"13 items", receipt("failure", false, items(13)), true, "schema-mismatch"},
		{"bad kind", receipt("failure", false, []any{item("note", "error: boom")}), true, "unverifiable-quote"},
		{"empty quote", receipt("failure", false, []any{item("fatal", "")}), true, "unverifiable-quote"},
		{"quote not in body", receipt("failure", false, []any{item("fatal", "absent")}), true, "unverifiable-quote"},
		{"non-object item", receipt("failure", false, []any{"error: boom"}), true, "unverifiable-quote"},
		{"no failure evidence", receipt("failure", false, []any{item("warning", "first line")}), true, "missing-failure-evidence"},
		{"no evidence at all", receipt("failure", false, []any{}), true, "missing-failure-evidence"},
	}
	for _, c := range cases {
		if _, reason := ValidateReceipt(c.raw, a, body, c.isError); reason != c.reason {
			t.Errorf("%s: reason = %q, want %q", c.name, reason, c.reason)
		}
	}

	// 12 items is the limit; a long-but-600-unit quote is accepted.
	longBody := strings.Repeat("a", 600)
	la := Archive{Hash: SHA256Hex(longBody)}
	raw, _ := json.Marshal(map[string]any{"schema": ReceiptSchema, "source_sha256": la.Hash, "status": "success", "uncertain": false, "evidence": []any{item("summary", longBody)}})
	if _, reason := ValidateReceipt(string(raw), la, longBody, false); reason != "" {
		t.Errorf("600-char quote: reason = %q", reason)
	}

	// Without failure signal in the body, an error receipt needs no failure item.
	quiet := "all quiet"
	qa := Archive{Hash: SHA256Hex(quiet)}
	raw, _ = json.Marshal(map[string]any{"schema": ReceiptSchema, "source_sha256": qa.Hash, "status": "failure", "uncertain": true, "evidence": []any{}})
	if v, reason := ValidateReceipt(string(raw), qa, quiet, true); reason != "" || !v.Uncertain || len(v.Evidence) != 0 {
		t.Errorf("quiet = %+v, %q", v, reason)
	}

	dup := receipt("failure", true, []any{item("fatal", "error: boom"), item("fatal", "error: boom"), item("warning", "error: boom"), item("summary", "last line")})
	v, reason := ValidateReceipt(dup, a, body, true)
	if reason != "" {
		t.Fatalf("valid receipt rejected: %q", reason)
	}
	want := []Evidence{
		{"fatal", 2, "error: boom", SHA256Hex("error: boom")},
		{"warning", 2, "error: boom", SHA256Hex("error: boom")},
		{"summary", 3, "last line", SHA256Hex("last line")},
	}
	if v.Status != "failure" || !v.Uncertain || len(v.Evidence) != len(want) {
		t.Fatalf("verified = %+v", v)
	}
	for i := range want {
		if v.Evidence[i] != want[i] {
			t.Errorf("evidence[%d] = %+v, want %+v", i, v.Evidence[i], want[i])
		}
	}
}

func TestReceiptTextFormat(t *testing.T) {
	a := Archive{Hash: "abc", Bytes: 100, Lines: 9, Path: "/store/objects/ab/abc.txt"}
	p := ProviderResult{Provider: "prov", Model: "mod", Usage: Usage{TotalTokens: 321}}
	head := "sol_pi_evidence_receipt_v1\nstatus=failure\nuncertain=false\ncommand_sha256=" + SHA256Hex("make") +
		"\nsource_sha256=abc\nsource_bytes=100\nsource_lines=9\nsource_artifact=/store/objects/ab/abc.txt\nreducer_provider=prov\nreducer_model=mod\nreducer_total_tokens=321\nverified_evidence:\n"
	tail := "authority=Sol retains diagnosis, repair, rerun, and pass/fail adjudication\nreadback=use bash with an explicit byte or line range on source_artifact when exact context is needed"

	v := Verified{Status: "failure", Evidence: []Evidence{
		{"fatal", 2, "a\"b\n", "h1"},
		{"summary", 5, "ok", "h2"},
	}}
	want := head + "- kind=fatal line=2 quote_sha256=h1 quote=\"a\\\"b\\n\"\n- kind=summary line=5 quote_sha256=h2 quote=\"ok\"\n" + tail
	if got := ReceiptText("make", a, v, p); got != want {
		t.Fatalf("ReceiptText =\n%s\nwant\n%s", got, want)
	}
	v = Verified{Status: "failure", Uncertain: false}
	if got := ReceiptText("make", a, v, p); got != head+"- none\n"+tail {
		t.Fatalf("empty ReceiptText =\n%s", got)
	}
	v.Uncertain = true
	if got := ReceiptText("make", a, v, p); !strings.Contains(got, "\nuncertain=true\n") {
		t.Fatalf("uncertain not rendered: %s", got)
	}
}

func TestJSQuote(t *testing.T) {
	cases := map[string]string{
		"":             `""`,
		`a"b\c`:        `"a\"b\\c"`,
		"a\nb\tc\r":    `"a\nb\tc\r"`,
		"\b\f":         `"\b\f"`,
		"\x01\x1f":     `"\u0001\u001f"`,
		"é😀<>&":        "\"é😀<>&\"",
		"\u2028\u2029": "\"\u2028\u2029\"",
		"/\x7f":        "\"/\x7f\"",
	}
	for in, want := range cases {
		if got := jsQuote(in); got != want {
			t.Errorf("jsQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestValidateReceiptQuoteLengthAndShape(t *testing.T) {
	build := func(a Archive, status string, evidence any) string {
		raw, _ := json.Marshal(map[string]any{"schema": ReceiptSchema, "source_sha256": a.Hash, "status": status, "uncertain": false, "evidence": evidence})
		return string(raw)
	}
	quoteOf := func(q string) []any { return []any{map[string]any{"kind": "summary", "quote": q}} }

	// The body contains every quote, so only the length rule can reject it.
	for _, c := range []struct {
		name, quote, reason string
	}{
		{"601 ASCII", strings.Repeat("a", 601), "unverifiable-quote"},
		{"600 ASCII", strings.Repeat("a", 600), ""},
		{"301 emoji (602 units)", strings.Repeat("😀", 301), "unverifiable-quote"},
		{"300 emoji (600 units)", strings.Repeat("😀", 300), ""},
	} {
		body := c.quote + "\ntail"
		a := Archive{Hash: SHA256Hex(body)}
		if _, reason := ValidateReceipt(build(a, "success", quoteOf(c.quote)), a, body, false); reason != c.reason {
			t.Errorf("%s: reason = %q, want %q", c.name, reason, c.reason)
		}
	}

	body := "x\nrepeat\ny\nrepeat\n"
	a := Archive{Hash: SHA256Hex(body)}
	if v, reason := ValidateReceipt(build(a, "success", quoteOf("repeat")), a, body, false); reason != "" || v.Evidence[0].Line != 2 {
		t.Errorf("first occurrence: %+v, %q", v, reason)
	}
	if _, reason := ValidateReceipt(build(a, "failure", []any{}), a, body, false); reason != "schema-mismatch" {
		t.Errorf("failure status without is_error: %q", reason)
	}
	if _, reason := ValidateReceipt(build(a, "success", nil), a, body, false); reason != "schema-mismatch" {
		t.Errorf("evidence null: %q", reason)
	}
	if _, reason := ValidateReceipt("null", a, body, false); reason != "schema-mismatch" {
		t.Errorf("JSON null: %q", reason)
	}

	var lines []string
	var twelve []any
	for i := 0; i < 12; i++ {
		lines = append(lines, fmt.Sprintf("entry%02d", i))
		twelve = append(twelve, map[string]any{"kind": "summary", "quote": lines[i]})
	}
	body = strings.Join(lines, "\n")
	a = Archive{Hash: SHA256Hex(body)}
	if v, reason := ValidateReceipt(build(a, "success", twelve), a, body, false); reason != "" || len(v.Evidence) != 12 {
		t.Errorf("12 items: %d, %q", len(v.Evidence), reason)
	}
}
