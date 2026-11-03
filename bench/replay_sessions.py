#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 SoL-PiG contributors
# SPDX-License-Identifier: MIT
"""Replay recorded Pi/PiG sessions under ObservationPack policies and price them with a prompt-cache model.

    bench/replay_sessions.py --store "PiG personal=~/.pig/agent-personal/sessions" --store "PiG work=~/.pig/agent/sessions"

For every assistant message (one provider request) the script rebuilds the model context from
the session tree (branch, compaction, context edits), estimates each block's size, scales the
estimates to the request's recorded prompt tokens, and prices the request under a prefix-cache
model: tokens before the first changed block are cache reads, the rest cache writes, and a
request whose recorded cache read is zero is cold. ObservationPack policies change which tool
results are replaced by a placeholder and when, so the same session is priced as plain Pi,
as SoL-Pi today (third send), as PR #121 (second send) and as placeholder-only.

Only aggregates are printed: no session ids, paths, project names or message content.
Prices come from each request's recorded cost when the session carries one, else from Pi's
model registry (--registry); unpriced requests count tokens but no dollars.
"""

import argparse
import glob
import json
import math
import os
import re
import statistics
import sys
from collections import Counter, defaultdict

THRESHOLD_BYTES = 10 * 1024
PLACEHOLDER_BYTES = 1_400  # header lines plus a 1 KiB excerpt
POLICIES = {  # name: (threshold bytes, full sends) or None for ObservationPack off
    "none": None,
    "third send (SoL-Pi today)": (THRESHOLD_BYTES, 2),
    "second send (PR #121)": (THRESHOLD_BYTES, 1),
    "placeholder only": (THRESHOLD_BYTES, 0),
    "second send, 4 KiB": (4 * 1024, 1),
}
DIAGNOSTIC_COMMAND = re.compile(
    r"(?:^|[;&|()\s])(?:lake\s+build|lake\s+env\s+lean|lean|coq|cargo(?:\s+(?:build|test|check))?|zig\s+build|pytest|python(?:3)?\s+-m\s+(?:pytest|unittest|py_compile)|ctest|cmake\s+--build|ninja|make|(?:npm|pnpm|yarn)\s+(?:run\s+)?test|go\s+test|bazel\s+test)(?:\s|$)",
    re.I,
)
SKIP_PROVIDERS = {"faux", "tabnine"}


def tokens(chars: int) -> int:
    return math.ceil(chars / 4)


def text_of(content) -> str:
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        return ""
    parts = []
    for block in content:
        if not isinstance(block, dict):
            continue
        if block.get("type") == "text":
            parts.append(block.get("text", ""))
        elif block.get("type") == "toolCall":
            parts.append(json.dumps(block.get("arguments", {})))
        elif block.get("type") == "tool_use":
            parts.append(json.dumps(block.get("input", {})))
    return "\n".join(parts)


def pure_text(content) -> bool:
    return isinstance(content, list) and len(content) > 0 and all(isinstance(b, dict) and b.get("type") == "text" for b in content)


class Prices:
    def __init__(self, registry_dir):
        self.table = {}
        for path in glob.glob(os.path.join(registry_dir, "*.json")) if registry_dir else []:
            provider = os.path.basename(path)[:-5]
            self._walk(json.load(open(path)), provider)

    def _walk(self, node, provider):
        if isinstance(node, dict):
            cost = node.get("cost")
            if isinstance(cost, dict) and "id" in node and "input" in cost:
                self.table[(provider, node["id"])] = cost
                self.table[(None, node["id"])] = cost
            for value in node.values():
                self._walk(value, provider)
        elif isinstance(node, list):
            for value in node:
                self._walk(value, provider)

    def lookup(self, provider, model):
        candidates = [model]
        if model and not model.startswith("claude-") and re.match(r"^(opus|sonnet|haiku|fable)-", model):
            candidates.append("claude-" + model.replace(".", "-"))
        for candidate in candidates:
            for key in ((provider, candidate), (None, candidate)):
                if key in self.table:
                    c = self.table[key]
                    return {"input": c["input"], "output": c["output"], "read": c.get("cacheRead", c["input"]), "write": c.get("cacheWrite") or c["input"]}
        return None


def request_prices(usage, prices, provider, model):
    """USD per token for this request: recorded cost first, Pi's registry second."""
    cost = usage.get("cost") if isinstance(usage.get("cost"), dict) else None
    if cost and cost.get("total"):
        per = lambda key, tok: (cost.get(key, 0) / tok) if tok else None
        p = {"input": per("input", usage.get("input", 0)), "output": per("output", usage.get("output", 0)),
             "read": per("cacheRead", usage.get("cacheRead", 0)), "write": per("cacheWrite", usage.get("cacheWrite", 0))}
        table = prices.lookup(provider, model) or {}
        for key in p:  # a zero count leaves that rate unknown; fill it from the registry
            if p[key] is None:
                p[key] = (table.get(key, 0) / 1e6) if table else 0
        return p, "recorded"
    table = prices.lookup(provider, model)
    if table:
        return {k: v / 1e6 for k, v in table.items()}, "registry"
    return None, "unpriced"


class Session:
    def __init__(self, path):
        self.entries = []
        self.by_id = {}
        self.arm = {"op": False, "af": False, "occ": False}
        with open(path, "rb") as f:
            for raw in f:
                try:
                    e = json.loads(raw)
                except Exception:
                    continue
                if e.get("type") == "session":
                    continue
                if "id" in e:
                    self.by_id[e["id"]] = e
                self.entries.append(e)
                m = e.get("message") if isinstance(e.get("message"), dict) else None
                if m and m.get("role") == "system":
                    names = {t.get("name") for t in m.get("toolsAdded", []) if isinstance(t, dict)}
                    self.arm["op"] |= "obs_recall" in names
                    self.arm["occ"] |= "update_plan" in names
                    self.arm["af"] |= any("then_run" in json.dumps(t) for t in m.get("toolsAdded", []) if isinstance(t, dict) and t.get("name") in ("edit", "write"))

    def chain(self, entry):
        out = []
        seen = set()
        cur = entry.get("parentId")
        while cur and cur not in seen:
            seen.add(cur)
            parent = self.by_id.get(cur)
            if not parent:
                break
            out.append(parent)
            cur = parent.get("parentId")
        out.reverse()
        return out

    def context_blocks(self, entry):
        """Model context for the request this assistant entry answered, as (id, kind, chars, meta) blocks."""
        chain = self.chain(entry)
        compaction = None
        edits = {}
        for e in chain:
            if e.get("type") == "compaction":
                compaction = e
            elif e.get("type") == "context_edit":
                edits[e.get("targetId")] = e
        if compaction:
            c = chain.index(compaction)
            kept = next((i for i, e in enumerate(chain) if e.get("id") == compaction.get("firstKeptEntryId")), c)
            chain = [compaction] + chain[kept:c] + chain[c + 1:]
        blocks = []
        for e in chain:
            t = e.get("type")
            if t == "compaction":
                blocks.append((e["id"], "summary", len(e.get("summary") or ""), {}))
            elif t in ("branch_summary", "custom_message"):
                blocks.append((e["id"], t, len(text_of(e.get("content")) or e.get("summary") or ""), {}))
            elif t == "message":
                m = e["message"]
                role = m.get("role")
                if role == "bashExecution" and m.get("excludeFromContext"):
                    continue
                edit = edits.get(e["id"])
                if edit is not None:
                    if edit.get("replacement") is None:
                        continue
                    content = (edit["replacement"] or {}).get("content")
                else:
                    content = m.get("content")
                if role == "system":
                    chars = len(json.dumps(m.get("sections", {}))) + len(json.dumps(m.get("toolsAdded", []))) + len(json.dumps(m.get("toolsRemoved", [])))
                    blocks.append((e["id"], "system", chars, {}))
                elif role == "toolResult":
                    text = text_of(content)
                    size = len(text.encode("utf-8", "replace"))
                    eligible = pure_text(content) and not m.get("isError") and "sol_pi_evidence_receipt_v1" not in text
                    blocks.append((e["id"], "toolResult", len(text), {"bytes": size, "eligible": eligible, "tool": m.get("toolName")}))
                elif role == "assistant":
                    blocks.append((e["id"], "assistant", len(text_of(content)), {}))
                elif role == "bashExecution":
                    blocks.append((e["id"], "bash", len(m.get("command", "")) + len(m.get("output", "")), {}))
                else:
                    blocks.append((e["id"], role or "message", len(text_of(content)), {}))
        return blocks


def simulate(session, prices):
    """Per-request accounting under every policy. Returns per-session totals and counts."""
    actual_policy = (THRESHOLD_BYTES, 2) if session.arm["op"] else None
    policies = dict(POLICIES, actual=actual_policy)
    totals = {name: {"usd": 0.0, "read": 0, "write": 0, "replacements": 0, "avoided": 0} for name in policies}
    recorded = {"usd": 0.0, "read": 0, "write": 0, "input": 0, "output": 0}
    prev = {name: None for name in policies}  # previous request's variant blocks: list of (id, replaced)
    last_ratio = {name: 1.0 for name in policies}
    counts = Counter()
    pricing = Counter()
    requests = []
    assistants = [e for e in session.entries if e.get("type") == "message" and e["message"].get("role") == "assistant" and isinstance(e["message"].get("usage"), dict)]
    for entry in assistants:
        m = entry["message"]
        if m.get("provider") in SKIP_PROVIDERS:
            continue
        u = m["usage"]
        T = (u.get("input") or 0) + (u.get("cacheRead") or 0) + (u.get("cacheWrite") or 0)
        if T <= 0:
            continue
        p, how = request_prices(u, prices, m.get("provider"), m.get("model"))
        pricing[how] += 1
        blocks = session.context_blocks(entry)
        est = [tokens(chars) for _, _, chars, _ in blocks]
        scale = T / sum(est) if sum(est) > 0 else 1.0
        sizes = [max(1.0, e * scale) if e else 0.0 for e in est]
        warm = (u.get("cacheRead") or 0) > 0
        counts["requests"] += 1
        counts["cold"] += 0 if warm else 1
        recorded["read"] += u.get("cacheRead") or 0
        recorded["write"] += u.get("cacheWrite") or 0
        recorded["input"] += u.get("input") or 0
        recorded["output"] += u.get("output") or 0
        if p:
            recorded["usd"] += (u.get("input") or 0) * p["input"] + (u.get("cacheRead") or 0) * p["read"] + (u.get("cacheWrite") or 0) * p["write"] + (u.get("output") or 0) * p["output"]
        # sends before this request for each eligible result: assistant blocks after it in the context
        assistants_after = []
        n_after = 0
        for _, kind, _, _ in reversed(blocks):
            assistants_after.append(n_after)
            if kind == "assistant":
                n_after += 1
        assistants_after.reverse()
        # the actual cut: the first block the recorded cache read did not cover
        cut_other = None
        if warm:
            cum = 0.0
            for j, s in enumerate(sizes):
                cum += s
                if cum > (u.get("cacheRead") or 0) + 1:
                    cut_other = j
                    break
        for name, policy in policies.items():
            variant = []
            for j, (bid, kind, _, meta) in enumerate(blocks):
                replaced = bool(policy and kind == "toolResult" and meta.get("eligible") and meta["bytes"] > policy[0] and assistants_after[j] >= policy[1])
                variant.append((bid, replaced))
            vsizes = [tokens(PLACEHOLDER_BYTES) * scale if r else s for (_, r), s in zip(variant, sizes)]
            first_change = len(variant)
            previous = prev[name] or []
            for j, v in enumerate(variant):
                if j >= len(previous) or previous[j] != v:
                    first_change = j
                    break
            new_replacements = sum(1 for j, (_, r) in enumerate(variant) if r and (j >= len(previous) or previous[j][1] is False))
            # a recorded cache miss that coincides with the recorded policy's own replacement is ObservationPack, not another change
            cut = cut_other
            if cut is not None and name != "actual" and actual_policy and session.arm["op"]:
                actual_variant = [(bid, bool(kind == "toolResult" and meta.get("eligible") and meta["bytes"] > actual_policy[0] and assistants_after[j] >= actual_policy[1])) for j, (bid, kind, _, meta) in enumerate(blocks)]
                prev_actual = prev["actual"] or []
                actual_first = next((j for j, v in enumerate(actual_variant) if j >= len(prev_actual) or prev_actual[j] != v), len(actual_variant))
                if abs(actual_first - cut) <= 1 and any(r for _, r in actual_variant[actual_first:actual_first + 1]):
                    cut = None
            j_star = 0 if not warm else min([x for x in (first_change, cut) if x is not None] or [len(variant)])
            read = sum(vsizes[:j_star])
            write = sum(vsizes[j_star:])
            totals[name]["read"] += read
            totals[name]["write"] += write
            totals[name]["replacements"] += new_replacements
            totals[name]["avoided"] += sum(s - vs for s, vs in zip(sizes, vsizes))
            if p:
                totals[name]["usd"] += read * p["read"] + write * p["write"] + (u.get("output") or 0) * p["output"]
            prev[name] = variant
            last_ratio[name] = (sum(vsizes) / T) if T else 1.0
        # opportunities for the other mechanisms
        for block in m.get("content") or []:
            if isinstance(block, dict) and block.get("type") in ("toolCall", "tool_use"):
                counts[f"call:{block.get('name')}"] += 1
        requests.append((entry, p, T))
    # cache-warming requests: a full prompt read at the prompt's current size
    for e in session.entries:
        if e.get("type") == "usage" and isinstance(e.get("usage"), dict):
            u = e["usage"]
            p, _ = request_prices(u, prices, e.get("provider"), e.get("model"))
            read = u.get("cacheRead") or 0
            counts["warm_requests"] += 1
            recorded["read"] += read
            if p:
                recorded["usd"] += read * p["read"]
                for name in totals:
                    totals[name]["usd"] += read * last_ratio[name] * p["read"]
                    totals[name]["read"] += read * last_ratio[name]
    # Action Fusion opportunities: an edit/write call whose next assistant turn starts with a bash call
    af_pairs = 0
    af_usd = 0.0
    for i in range(len(requests) - 1):
        entry, _, _ = requests[i]
        calls = [b.get("name") for b in entry["message"].get("content") or [] if isinstance(b, dict) and b.get("type") in ("toolCall", "tool_use")]
        nxt, p_next, T_next = requests[i + 1]
        next_calls = [b.get("name") for b in nxt["message"].get("content") or [] if isinstance(b, dict) and b.get("type") in ("toolCall", "tool_use")]
        if calls and calls[-1] in ("edit", "write") and next_calls and next_calls[0] == "bash":
            af_pairs += 1
            if p_next:
                af_usd += T_next * p_next["read"] + (nxt["message"]["usage"].get("output") or 0) * p_next["output"]
    # Evidence-Preserving Reducer candidates: diagnostic bash output of 4 KiB or more
    epr = 0
    call_cmd = {}
    for e in session.entries:
        m = e.get("message") if isinstance(e.get("message"), dict) else None
        if not m:
            continue
        if m.get("role") == "assistant":
            for b in m.get("content") or []:
                if isinstance(b, dict) and b.get("type") in ("toolCall", "tool_use") and b.get("name") == "bash":
                    call_cmd[b.get("id")] = (b.get("arguments") or b.get("input") or {}).get("command", "")
        elif m.get("role") == "toolResult" and m.get("toolName") == "bash":
            cmd = call_cmd.get(m.get("toolCallId"), "")
            if DIAGNOSTIC_COMMAND.search(cmd) and len(text_of(m.get("content")).encode("utf-8", "replace")) >= 4096:
                epr += 1
    turns = len(assistants)
    return {"totals": totals, "recorded": recorded, "counts": counts, "pricing": pricing, "turns": turns, "af_pairs": af_pairs, "af_usd": af_usd, "epr": epr,
            "model": Counter(e["message"].get("model") for e in assistants).most_common(1)[0][0] if assistants else None, "arm": session.arm}


def bucket(turns):
    return "<50 turns" if turns < 50 else "50-149 turns" if turns < 150 else "150+ turns"


def fmt_usd(x):
    return f"${x:,.2f}"


def pct(a, b):
    return "n/a" if not b else f"{(a / b - 1) * 100:+.1f}%"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--store", action="append", required=True, help='"label=path" of a sessions directory; repeatable')
    ap.add_argument("--registry", default=os.path.expanduser("~/github.com/SoL-Pi/node_modules/@earendil-works/pi-ai/dist/providers/data"), help="Pi model registry data directory for prices")
    ap.add_argument("--min-turns", type=int, default=2)
    ap.add_argument("--out", help="write the Markdown report here as well")
    args = ap.parse_args()
    prices = Prices(args.registry if os.path.isdir(args.registry) else None)

    lines = []
    say = lines.append
    say("# Recorded sessions replayed under ObservationPack policies\n")
    say("Each assistant message is one provider request, priced under a prefix-cache model calibrated to the recorded prompt tokens: reads before the first changed block, writes after it, cold when the recorded cache read was zero. `actual` is the recorded policy (ObservationPack on at the third send, or off), so its gap to the recorded cost is the model's error. Dollar figures use the recorded per-request prices where the session has them, else Pi's registry prices.\n")
    grand = defaultdict(lambda: defaultdict(float))
    for spec in args.store:
        label, _, path = spec.partition("=")
        path = os.path.expanduser(path)
        files = [p for p in glob.glob(os.path.join(path, "**", "*.jsonl"), recursive=True) if "/sol-pi/" not in p and "/sol-pig/" not in p]
        results = []
        for p in files:
            try:
                s = Session(p)
                r = simulate(s, prices)
            except Exception:
                continue
            if r["turns"] >= args.min_turns and r["counts"]["requests"] > 0:
                results.append(r)
        if not results:
            say(f"## {label}\n\nno sessions with {args.min_turns}+ turns\n")
            continue
        say(f"## {label}\n")
        n_req = sum(r["counts"]["requests"] for r in results)
        priced = sum(r["pricing"]["recorded"] + r["pricing"]["registry"] for r in results)
        say(f"{len(results)} sessions, {n_req:,} requests ({sum(r['pricing']['recorded'] for r in results):,} priced from recorded cost, {sum(r['pricing']['registry'] for r in results):,} from the registry, {n_req - priced:,} unpriced), "
            f"{sum(r['counts']['warm_requests'] for r in results):,} cache-warming requests, {sum(r['counts']['cold'] for r in results):,} cold requests. "
            f"SoL on (ObservationPack tools present) in {sum(1 for r in results if r['arm']['op'])} sessions.\n")
        rec = sum(r["recorded"]["usd"] for r in results)
        act = sum(r["totals"]["actual"]["usd"] for r in results)
        errs = [abs(r["totals"]["actual"]["usd"] / r["recorded"]["usd"] - 1) for r in results if r["recorded"]["usd"] > 0.01]
        say(f"Calibration: recorded {fmt_usd(rec)}, modelled under the recorded policy {fmt_usd(act)} ({pct(act, rec)}); median per-session error {statistics.median(errs) * 100:.1f}% over {len(errs)} sessions.\n")
        for title, subset in (("All sessions", results), ("Sessions with 100+ turns", [r for r in results if r["turns"] >= 100])):
            if not subset:
                continue
            say(f"### {title}: {len(subset)} sessions, {sum(r['counts']['requests'] for r in subset):,} requests, recorded {fmt_usd(sum(r['recorded']['usd'] for r in subset))}\n")
            say("| Policy | Modelled cost | vs none | Cache read (M tok) | Cache write (M tok) | Replacements | Tokens kept out of prompts (M) |")
            say("|---|--:|--:|--:|--:|--:|--:|")
            none = sum(r["totals"]["none"]["usd"] for r in subset)
            for name in POLICIES:
                t = {k: sum(r["totals"][name][k] for r in subset) for k in ("usd", "read", "write", "replacements", "avoided")}
                say(f"| {name} | {fmt_usd(t['usd'])} | {pct(t['usd'], none)} | {t['read'] / 1e6:,.1f} | {t['write'] / 1e6:,.1f} | {t['replacements']:,} | {t['avoided'] / 1e6:,.1f} |")
                grand[(title, name)]["usd"] += t["usd"]
            grand[(title, "recorded")]["usd"] += sum(r["recorded"]["usd"] for r in subset)
            say("")
        say("### By session length\n")
        say("| Turns | Sessions | Recorded | none | third send | second send | placeholder only |")
        say("|---|--:|--:|--:|--:|--:|--:|")
        for b in ("<50 turns", "50-149 turns", "150+ turns"):
            sub = [r for r in results if bucket(r["turns"]) == b]
            if not sub:
                continue
            none = sum(r["totals"]["none"]["usd"] for r in sub)
            cells = [pct(sum(r["totals"][n]["usd"] for r in sub), none) for n in ("third send (SoL-Pi today)", "second send (PR #121)", "placeholder only")]
            say(f"| {b} | {len(sub)} | {fmt_usd(sum(r['recorded']['usd'] for r in sub))} | {fmt_usd(none)} | {' | '.join(cells)} |")
        say("")
        say("### By model (top 6 by recorded cost)\n")
        say("| Model | Sessions | Recorded | none | third send | second send |")
        say("|---|--:|--:|--:|--:|--:|")
        by_model = defaultdict(list)
        for r in results:
            by_model[r["model"]].append(r)
        for model, sub in sorted(by_model.items(), key=lambda kv: -sum(r["recorded"]["usd"] for r in kv[1]))[:6]:
            none = sum(r["totals"]["none"]["usd"] for r in sub)
            say(f"| {model} | {len(sub)} | {fmt_usd(sum(r['recorded']['usd'] for r in sub))} | {fmt_usd(none)} | {pct(sum(r['totals']['third send (SoL-Pi today)']['usd'] for r in sub), none)} | {pct(sum(r['totals']['second send (PR #121)']['usd'] for r in sub), none)} |")
        say("")
        calls = Counter()
        for r in results:
            for k, v in r["counts"].items():
                if k.startswith("call:"):
                    calls[k[5:]] += v
        top = ", ".join(f"{k} {v:,}" for k, v in calls.most_common(8))
        say(f"Other mechanisms: Action Fusion could have fused {sum(r['af_pairs'] for r in results):,} edit/write-then-bash pairs, worth about {fmt_usd(sum(r['af_usd'] for r in results))} of avoided requests; the reducer had {sum(r['epr'] for r in results):,} diagnostic logs of 4 KiB or more. Tool calls: {top}.\n")
    report = "\n".join(lines)
    print(report)
    if args.out:
        with open(args.out, "w") as f:
            f.write(report)


if __name__ == "__main__":
    main()
