// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/online-context-compact/extension.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/savings"
)

// The texts the choreography sends to the model and the user.
const (
	BoundaryCompactionInstructions = "Preserve completed work, verification results, important decisions, and remaining work."
	PostCompactionPlanReminder     = "Online context compaction finished. The parent task is still active. " +
		"Continue the remaining work from the current plan. Preserve existing step IDs when updating progress."
	SkippedCompactionReminder = "Online context compaction was skipped. The existing context is still available. " +
		"Continue the remaining work from the current plan."
	CompactionFailedResumeReminder = "Online context compaction did not complete. The parent task is still active. " +
		"Continue the remaining work from the current plan."

	reminderCustomType = "sol-pi-online-context-compact"
	skippedEntryType   = "sol-pi-online-context-compact-skipped"
	cancelledMessage   = "Compaction cancelled"
)

// recoverableCompactionErrors are rejected by the host before it commits a replacement summary, so the current context is still usable.
var recoverableCompactionErrors = []string{
	"Nothing to compact (session too small)",
	"Already compacted",
	"Summarization failed: generation hit the token cap and the summary is incomplete",
}

// Options ports OnlineContextCompactOptions; nil means unset.
type Options struct {
	CacheWriteReadRatio *float64
	KeepRecentTokens    *int
}

// cacheDebt is what an OCC-initiated compaction charges when the host reports it.
type cacheDebt struct{ debtTokens, repaymentTokens float64 }

type online struct {
	ratio      *float64
	keepRecent *int

	// mu guards everything below; no host call that can re-enter a handler is made while it is held.
	mu                 sync.Mutex
	state              State
	restored           bool
	observed           []map[string]any // never nil: nil would mean "unobserved" to EstimateProjectedCompactionTokens
	pendingBoundary    string           // tool call id of the update_plan call that completed a step
	selected           *Decision
	activeDebt         *cacheDebt
	compactionInFlight bool
	requestPending     bool
	compactionRefused  bool
}

func logf(what string, err error) { fmt.Fprintf(os.Stderr, "[occ] %s: %v\n", what, err) }

func positiveSafeInt(v int) bool { return v >= 1 && int64(v) <= 1<<53-1 }

// Register ports createOnlineContextCompactExtension: the update_plan tool, then the twelve events in SoL-Pi's order.
func Register(ext *sdk.Extension, opts Options) error {
	if opts.KeepRecentTokens != nil && !positiveSafeInt(*opts.KeepRecentTokens) {
		return errors.New("Online Context Compact keepRecentTokens must be a positive safe integer")
	}
	if r := opts.CacheWriteReadRatio; r != nil && (math.IsInf(*r, 0) || math.IsNaN(*r) || *r < 0) {
		return errors.New("Online Context Compact cacheWriteReadRatio must be finite and non-negative")
	}
	o := &online{ratio: opts.CacheWriteReadRatio, keepRecent: opts.KeepRecentTokens, state: InitialState(), observed: []map[string]any{}}
	o.registerTool(ext)
	ext.OnEvent(sdk.EventSessionStart, o.resetHandler)
	ext.OnEvent(sdk.EventSessionBeforeTree, o.sessionBeforeTree)
	ext.OnEvent(sdk.EventSessionTree, o.resetHandler)
	ext.OnEvent(sdk.EventContext, o.context)
	ext.OnEvent(sdk.EventBeforeProviderRequest, o.beforeProviderRequest)
	ext.OnEvent(sdk.EventMessageEnd, o.messageEnd)
	ext.OnEvent(sdk.EventInput, o.input)
	ext.OnEvent(sdk.EventTurnEnd, o.turnEnd)
	ext.OnEvent(sdk.EventAgentSettled, o.agentSettled)
	ext.OnEvent(sdk.EventSessionBeforeCompact, o.sessionBeforeCompact)
	ext.OnEvent(sdk.EventSessionCompact, o.sessionCompact)
	ext.OnEvent(sdk.EventSessionShutdown, o.sessionShutdown)
	return nil
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// runAborted reports whether the active run's signal is aborted; no run means not aborted.
func runAborted(ctx sdk.Context) bool {
	signal := ctx.Signal()
	return signal != nil && signal.Err() != nil
}

// reset ports restore's clearing half: it makes no host call, so session_start and session_tree never wait on the session. The branch is read lazily by ensureRestored.
func (o *online) reset() {
	o.state = InitialState()
	o.restored = false
	// Restoring raw history is not evidence of a provider-visible projection.
	o.observed = []map[string]any{}
	o.pendingBoundary = ""
	o.selected = nil
	o.activeDebt = nil
	o.compactionInFlight = false
	o.requestPending = false
	o.compactionRefused = false
}

// ensureRestored ports ensureRestored. A branch that cannot be read leaves the initial state and the session unrestored, so the next handler retries.
func (o *online) ensureRestored(ctx sdk.Context) {
	if o.restored {
		return
	}
	branch, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		logf("read the session branch", err)
		return
	}
	o.state = RestoreState(branch)
	o.restored = true
}

func (o *online) save(ctx sdk.Context) {
	if err := ctx.AppendEntry(StateEntryType, o.state); err != nil {
		logf("save the state entry", err)
	}
}

func (o *online) resetHandler(_ sdk.Context, _ map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reset()
	return nil, nil
}

func (o *online) sessionBeforeTree(sdk.Context, map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.compactionInFlight {
		return map[string]any{"cancel": true}, nil
	}
	return nil, nil
}

// resolveCompactionTokenSettings ports resolveCompactionTokenSettings: the model's override, then the base setting, then Pi's default.
func resolveCompactionTokenSettings(settings map[string]any, model *sdk.ModelInfo) (keepRecentTokens, reserveTokens int) {
	compaction := asMap(settings["compaction"])
	var override map[string]any
	if model != nil {
		override = asMap(asMap(compaction["modelOverrides"])[model.Provider+"/"+model.ID])
	}
	pick := func(key string, fallback int) int {
		for _, source := range []map[string]any{override, compaction} {
			if n, ok := safeInt(source[key]); ok {
				return n
			}
		}
		return fallback
	}
	return pick("keepRecentTokens", DefaultKeepRecentTokens), pick("reserveTokens", DefaultEconomics.WindowReserveTokens)
}

func tokenEstimate(text string) int { return (len(text) + 3) / 4 }

// contextTokens ports contextTokens: the larger of the visible estimate and what the host reports.
func (o *online) contextTokens(ctx sdk.Context) int {
	estimated := 0
	for _, m := range o.observed {
		estimated += sdk.EstimateTokens(m)
	}
	prompt, err := ctx.GetSystemPrompt()
	if err != nil {
		logf("read the system prompt", err)
	}
	estimated += tokenEstimate(prompt)
	usage, err := ctx.GetContextUsage()
	if err != nil {
		logf("read the context usage", err)
	}
	if usage != nil && usage.Tokens != nil && positiveSafeInt(*usage.Tokens) {
		return max(*usage.Tokens, estimated)
	}
	return estimated
}

func (o *online) context(ctx sdk.Context, data map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensureRestored(ctx)
	messages, _ := data["messages"].([]any)
	o.observed = o.observed[:0:0]
	for _, m := range messages {
		if message := asMap(m); message != nil {
			o.observed = append(o.observed, message)
		}
	}
	o.requestPending = !runAborted(ctx)
	return nil, nil
}

// beforeProviderRequest counts a request only after a context event: cache-warm replays, aborted-run builds and repeats are not requests.
func (o *online) beforeProviderRequest(ctx sdk.Context, _ map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.requestPending {
		return nil, nil
	}
	o.requestPending = false
	o.ensureRestored(ctx)
	o.state = o.state.RecordProviderRequest(o.contextTokens(ctx))
	o.save(ctx)
	return nil, nil
}

// messageEnd: the assistant message ends the request a context event started, so a later replay cannot take its place.
func (o *online) messageEnd(_ sdk.Context, data map[string]any) (any, error) {
	if asMap(data["message"])["role"] == "assistant" {
		o.mu.Lock()
		o.requestPending = false
		o.mu.Unlock()
	}
	return nil, nil
}

func (o *online) input(ctx sdk.Context, data map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.compactionRefused = false
	o.ensureRestored(ctx)
	text, _ := data["text"].(string)
	if data["streamingBehavior"] != "steer" && !strings.HasPrefix(text, "CORRECTION:") {
		if next, changed := o.state.RecordCompletedPlanHandoff(); changed {
			o.state = next
			o.save(ctx)
		}
		return map[string]any{"action": "continue"}, nil
	}
	o.pendingBoundary = ""
	o.selected = nil
	o.activeDebt = nil
	o.state = o.state.RecordCorrection()
	o.save(ctx)
	return map[string]any{"action": "continue"}, nil
}

func stoppedEarly(message map[string]any) bool {
	return message["role"] != "assistant" || message["stopReason"] == "error" || message["stopReason"] == "aborted"
}

// turnEnd evaluates the boundary the tool marked: the tool result is in the turn by now.
func (o *online) turnEnd(ctx sdk.Context, data map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	results, _ := data["toolResults"].([]any)
	// Plan chatter cannot make a rejected compact feasible: it takes actual tool work or new user input to try again.
	var boundaryResult map[string]any
	for _, item := range results {
		result := asMap(item)
		if result["toolName"] != "update_plan" && result["isError"] != true {
			o.compactionRefused = false
		}
		if boundaryResult == nil && o.pendingBoundary != "" && result["toolCallId"] == o.pendingBoundary {
			boundaryResult = result
		}
	}
	boundary := o.pendingBoundary
	o.pendingBoundary = ""
	if boundary == "" || o.selected != nil || o.compactionRefused {
		return nil, nil
	}
	if stoppedEarly(asMap(data["message"])) || runAborted(ctx) || boundaryResult == nil || boundaryResult["isError"] == true {
		return nil, nil
	}

	o.ensureRestored(ctx)
	settings, err := ctx.GetSettings()
	if err != nil {
		logf("read the settings", err)
		return nil, nil
	}
	model, err := ctx.GetModelInfo()
	if err != nil {
		logf("read the model", err)
	}
	keepRecentTokens, reserveTokens := resolveCompactionTokenSettings(settings, model)
	if o.keepRecent != nil {
		keepRecentTokens = *o.keepRecent
	}
	projection, err := ctx.SessionManager().BuildSessionProjection()
	if err != nil {
		logf("build the session projection", err)
		return nil, nil
	}
	projected := make([]Projected, len(projection.Entries))
	for i, entry := range projection.Entries {
		projected[i] = Projected{SourceEntry: entry.SourceEntry, Messages: entry.Messages}
	}

	usage, err := ctx.GetContextUsage()
	if err != nil {
		logf("read the context usage", err)
	}
	writeTokens := o.contextTokens(ctx)
	archiveTokens := EstimateProjectedCompactionTokens(projected, keepRecentTokens, o.observed)
	var window *int
	switch {
	case usage != nil && positiveSafeInt(usage.ContextWindow):
		window = &usage.ContextWindow
	case model != nil && positiveSafeInt(model.ContextWindow):
		window = &model.ContextWindow
	}
	var averageIncrement *float64
	if o.state.PositiveContextDeltaCount != 0 {
		v := o.state.PositiveContextDeltaTotal / float64(o.state.PositiveContextDeltaCount)
		averageIncrement = &v
	}
	var sinceCompaction *int
	if o.state.LastCompactionRequestCount != nil {
		v := o.state.RequestCount - *o.state.LastCompactionRequestCount
		sinceCompaction = &v
	}
	economics := DefaultEconomics
	economics.WindowReserveTokens = reserveTokens
	decision := DecideCompaction(DecisionInput{
		WriteTokens: writeTokens, ArchiveTokens: archiveTokens, MemoTokens: DefaultNativeSummaryTokenEstimate, ContextTokens: writeTokens,
		CompletedBoundaryRequestCounts: o.state.CompletedBoundaryRequestCounts,
		RemainingBoundaries:            len(o.state.Plan) - countCompleted(o.state.Plan),
		AverageContextTokenIncrement:   averageIncrement,
		ContextWindowTokens:            window,
		PriorCompactionCount:           o.state.NativeCompactionCount,
		RequestsSinceLastCompaction:    sinceCompaction,
		CarriedDebtTokens:              o.state.CacheDebtTokens,
		CacheDebtRepaymentTokens:       o.state.CacheDebtRepaymentTokens,
		CacheWriteReadRatio:            o.ratio,
		Economics:                      economics,
	})
	if decision.Compact && archiveTokens == 0 {
		decision.Compact, decision.Reason = false, "native_not_compactable"
	}
	if !decision.Compact {
		return nil, nil
	}
	o.selected = &decision
	ctx.Abort()
	return nil, nil
}

func countCompleted(plan []Step) int {
	return len(slices.DeleteFunc(slices.Clone(plan), func(s Step) bool { return s.Status != "completed" }))
}

// compactionOutcome is what CompactWithOptions' callbacks report; err is nil on completion.
type compactionOutcome struct{ err error }

// agentSettled starts the selected compaction once the run is idle, waits for it, and resumes the task. PiG defers the turn the reminder
// requests until every settled handler returns, so nothing here waits for a later agent_settled.
func (o *online) agentSettled(ctx sdk.Context, _ map[string]any) (any, error) {
	o.mu.Lock()
	pending := o.selected
	o.selected = nil
	idle, err := ctx.IsIdle()
	if err != nil {
		logf("read the idle state", err)
	}
	if err != nil || !idle {
		o.selected = pending
		o.mu.Unlock()
		return nil, nil
	}
	if pending == nil {
		o.mu.Unlock()
		return nil, nil
	}
	ratio := 0.0
	if pending.IncrementalCacheCostRatio != nil {
		ratio = *pending.IncrementalCacheCostRatio
	}
	o.activeDebt = &cacheDebt{
		debtTokens:      float64(pending.PostCompactionTokens) * ratio,
		repaymentTokens: math.Max(0, float64(pending.ArchiveTokens-pending.MemoTokens)),
	}
	o.compactionInFlight = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.compactionInFlight = false
		o.activeDebt = nil
		o.mu.Unlock()
	}()

	done := make(chan compactionOutcome, 1)
	finish := func(outcome compactionOutcome) {
		select {
		case done <- outcome:
		default: // the first outcome settles the compaction
		}
	}
	ctx.CompactWithOptions(sdk.CompactOptions{
		CustomInstructions: BoundaryCompactionInstructions,
		OnComplete: func(result map[string]any) {
			defer finish(compactionOutcome{}) // a panic below must not leave agent_settled waiting
			summary, _ := result["summary"].(string)
			if removed := pending.ArchiveTokens - tokenEstimate(summary); removed > 0 {
				savings.Show(ctx, savings.OnlineContextCompact, savings.FormatCount(float64(removed), "context tokens removed"))
			}
		},
		OnError: func(err error) {
			o.mu.Lock()
			o.activeDebt = nil
			o.mu.Unlock()
			finish(compactionOutcome{err: err})
		},
	})
	var outcome compactionOutcome
	select {
	case outcome = <-done:
	case <-ctx.Done():
		logf("compaction callback", errors.New("context done"))
		return nil, nil
	}

	o.mu.Lock()
	o.compactionInFlight = false
	o.mu.Unlock()
	var failure error
	if outcome.err != nil && !strings.Contains(outcome.err.Error(), cancelledMessage) {
		failure = outcome.err
	}
	recoverable := failure != nil && slices.ContainsFunc(recoverableCompactionErrors, func(s string) bool { return strings.Contains(failure.Error(), s) })
	if recoverable {
		o.mu.Lock()
		o.compactionRefused = true
		o.mu.Unlock()
		if err := ctx.AppendEntry(skippedEntryType, map[string]any{"reason": failure.Error()}); err != nil {
			logf("record the skipped compaction", err)
		}
		if ctx.Mode() == "tui" {
			ctx.Notify("Online compaction skipped: "+failure.Error(), "warning")
		}
	}

	// The run was stopped for this compaction, so resume it unless the user cancelled.
	// Known native refusals are recorded as skips; other failures still propagate after resuming.
	reminder := ""
	switch {
	case outcome.err == nil:
		reminder = PostCompactionPlanReminder
	case recoverable:
		reminder = SkippedCompactionReminder
	case failure != nil:
		reminder = CompactionFailedResumeReminder
	}
	var sendErr error
	if reminder != "" {
		if sendErr = ctx.SendMessage(reminderCustomType, reminder, false, sdk.SendMessageOptions{TriggerTurn: sdk.Bool(true)}); sendErr != nil {
			logf("send the resume reminder", sendErr)
		}
	}
	if failure != nil && !recoverable {
		return nil, failure
	}
	return nil, sendErr
}

// sessionBeforeCompact: the host may run its own threshold compaction for a run this extension already stopped for a selected
// boundary; that would consume the selection and leave the boundary without a continuation, so agent_settled compacts instead.
func (o *online) sessionBeforeCompact(_ sdk.Context, data map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.selected != nil && data["reason"] == "threshold" {
		return map[string]any{"cancel": true}, nil
	}
	return nil, nil
}

// sessionCompact runs for any native compaction: it counts, and charges debt only for OCC's own.
func (o *online) sessionCompact(ctx sdk.Context, data map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensureRestored(ctx)
	o.compactionRefused = false
	debt := cacheDebt{}
	if fromExtension, _ := data["fromExtension"].(bool); !fromExtension && o.activeDebt != nil {
		debt = *o.activeDebt
	}
	o.state = o.state.RecordCompaction(debt.debtTokens, debt.repaymentTokens)
	o.save(ctx)
	o.pendingBoundary = ""
	o.selected = nil
	o.activeDebt = nil
	o.requestPending = false
	o.observed = []map[string]any{}
	return nil, nil
}

func (o *online) sessionShutdown(sdk.Context, map[string]any) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pendingBoundary = ""
	o.selected = nil
	o.activeDebt = nil
	o.compactionInFlight = false
	o.requestPending = false
	return nil, nil
}
