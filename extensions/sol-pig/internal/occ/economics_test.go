// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi tests/online-context-compact-economics.test.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"math"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func baseInput() DecisionInput {
	return DecisionInput{
		WriteTokens:                    80000,
		ArchiveTokens:                  60000,
		MemoTokens:                     1000,
		ContextTokens:                  80000,
		CompletedBoundaryRequestCounts: []int{4, 6, 5},
		RemainingBoundaries:            4,
		AverageContextTokenIncrement:   ptr(2000.0),
		ContextWindowTokens:            ptr(200000),
		CacheWriteReadRatio:            ptr(1.0),
		Economics:                      DefaultEconomics,
	}
}

func windowPressure(in DecisionInput) DecisionInput {
	in.ContextTokens = 195000
	in.CacheWriteReadRatio = ptr(100.0)
	in.Economics.WindowReserveTokens = 10000
	return in
}

func wantDecision(t *testing.T, d Decision, compact bool, reason string) {
	t.Helper()
	if d.Compact != compact || d.Reason != reason {
		t.Fatalf("got compact=%v reason=%q, want compact=%v reason=%q", d.Compact, d.Reason, compact, reason)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDefaultEconomics(t *testing.T) {
	want := Economics{1, 0, 16384, 2, 1.5, 2}
	if DefaultEconomics != want {
		t.Fatalf("got %+v", DefaultEconomics)
	}
}

func TestEstimateRemainingRequestsFromCompletedBoundaries(t *testing.T) {
	h := EstimateRemainingRequests([]int{4, 6, 5}, 3, 100000, ptr(200000), ptr(5000.0), DefaultEconomics)
	if h.MeanBoundaryRequests != 5 || h.ExpectedRemaining != 16 || h.WindowRequestUpperBound == nil || *h.WindowRequestUpperBound != 20 {
		t.Fatalf("got %+v", h)
	}
}

func TestEstimateRemainingRequestsVarianceRules(t *testing.T) {
	e := DefaultEconomics
	e.RemainingRequestStddevK = 1
	// fewer than three samples: lower bound is half the mean
	h := EstimateRemainingRequests([]int{4, 6}, 2, 0, nil, nil, e)
	if h.LowerBound != 2.5 || h.UnboundedRemaining != 1+5 || h.ExpectedRemaining != 6 || h.WindowRequestUpperBound != nil {
		t.Fatalf("small sample: %+v", h)
	}
	// three samples: mean 5, sample deviation 1
	h = EstimateRemainingRequests([]int{4, 6, 5}, 2, 0, nil, nil, e)
	if h.LowerBound != 4 || h.UnboundedRemaining != 9 {
		t.Fatalf("variance: %+v", h)
	}
	// large deviation clamps at zero
	h = EstimateRemainingRequests([]int{1, 1, 100}, 5, 0, nil, nil, e)
	if h.LowerBound != 0 || h.UnboundedRemaining != 1 {
		t.Fatalf("clamp: %+v", h)
	}
	// non-positive increment means no window bound; past the window clamps to zero
	if h := EstimateRemainingRequests([]int{5}, 1, 0, ptr(100), ptr(0.0), DefaultEconomics); h.WindowRequestUpperBound != nil {
		t.Fatalf("zero increment: %+v", h)
	}
	if h := EstimateRemainingRequests([]int{5}, 1, 300, ptr(100), ptr(10.0), DefaultEconomics); h.WindowRequestUpperBound == nil || *h.WindowRequestUpperBound != 0 || h.ExpectedRemaining != 0 {
		t.Fatalf("past window: %+v", h)
	}
}

func TestDecideRejectsNonPositiveSaving(t *testing.T) {
	in := baseInput()
	in.ArchiveTokens, in.MemoTokens = 500, 1000
	wantDecision(t, DecideCompaction(in), false, "non_positive_saving")
}

func TestDecideCompactsWhenBreakevenFitsHorizon(t *testing.T) {
	wantDecision(t, DecideCompaction(baseInput()), true, "economic")
}

func TestDecideWindowProtectionBeatsEconomicGate(t *testing.T) {
	wantDecision(t, DecideCompaction(windowPressure(baseInput())), true, "window_protection")
}

func TestDecideDefersWithoutCacheRatio(t *testing.T) {
	in := baseInput()
	in.CacheWriteReadRatio = nil
	wantDecision(t, DecideCompaction(in), false, "cache_ratio_unavailable")
}

func TestDecideHorizonUnavailable(t *testing.T) {
	in := baseInput()
	in.CompletedBoundaryRequestCounts = nil
	d := DecideCompaction(in)
	wantDecision(t, d, false, "horizon_unavailable")
	if d.Horizon != nil || d.EffectiveHorizonRequests != nil {
		t.Fatalf("horizon should be absent: %+v", d)
	}
}

func TestDecideChargesCarriedDebtOnlyAfterFirstCompaction(t *testing.T) {
	in := baseInput()
	in.PriorCompactionCount = 1
	in.CacheWriteReadRatio = ptr(2.0)
	in.CarriedDebtTokens = 2000000
	d := DecideCompaction(in)
	wantDecision(t, d, false, "deferred_carried_debt")
	if *d.CombinedBreakevenRequests <= *d.BreakevenRequests {
		t.Fatalf("combined %v should exceed %v", *d.CombinedBreakevenRequests, *d.BreakevenRequests)
	}
}

func TestDecideDefersSubsequentMargin(t *testing.T) {
	in := baseInput()
	in.PriorCompactionCount = 1
	in.CacheWriteReadRatio = ptr(2.0)
	in.CompletedBoundaryRequestCounts = []int{1, 1, 1}
	in.RemainingBoundaries = 21 // horizon 22
	in.ArchiveTokens = 1100
	in.MemoTokens = 1000
	in.WriteTokens = 1100
	d := DecideCompaction(in)
	// saving 100, post 1000, breakeven 10; 10*1.5 = 15 <= 22
	wantDecision(t, d, true, "economic")
	in.RemainingBoundaries = 12 // horizon 13: breakeven 10 <= 13 but 15 > 13
	wantDecision(t, DecideCompaction(in), false, "deferred_subsequent_margin")
}

func TestDecideDeferredEconomic(t *testing.T) {
	in := baseInput()
	in.CacheWriteReadRatio = ptr(1000.0)
	wantDecision(t, DecideCompaction(in), false, "deferred_economic")
}

func TestDecidePricesCarriedDebtAgainstAllSavings(t *testing.T) {
	in := baseInput()
	in.WriteTokens, in.ArchiveTokens, in.MemoTokens = 500, 200, 100
	in.PriorCompactionCount = 1
	in.CacheWriteReadRatio = ptr(2.0)
	in.CarriedDebtTokens = 1000
	in.CacheDebtRepaymentTokens = 200
	d := DecideCompaction(in)
	if d.PostCompactionTokens != 400 || *d.BreakevenRequests != 4 || !near(*d.CombinedBreakevenRequests, 14.0/3) {
		t.Fatalf("got %+v", d)
	}
}

func TestDecideChargesDebtOnlyForPostCompactionContext(t *testing.T) {
	in := baseInput()
	in.CacheWriteReadRatio = ptr(2.0)
	d := DecideCompaction(in)
	if d.PostCompactionTokens != 21000 || !near(*d.BreakevenRequests, 21000.0/59000) {
		t.Fatalf("got %+v", d)
	}
}

func TestDecideCooldown(t *testing.T) {
	in := baseInput()
	in.PriorCompactionCount = 1
	in.RequestsSinceLastCompaction = ptr(1)
	d := DecideCompaction(in)
	wantDecision(t, d, false, "deferred_post_compaction_cooldown")
	if d.RequestsSinceLastCompaction == nil || *d.RequestsSinceLastCompaction != 1 {
		t.Fatalf("echo lost: %+v", d)
	}
	in.RequestsSinceLastCompaction = ptr(2)
	wantDecision(t, DecideCompaction(in), true, "economic")
}

func TestDecideWindowProtectionBypassesCooldown(t *testing.T) {
	in := windowPressure(baseInput())
	in.PriorCompactionCount = 1
	in.RequestsSinceLastCompaction = ptr(1)
	wantDecision(t, DecideCompaction(in), true, "window_protection")
}

func TestDecideNoCooldownBeforeFirstCompaction(t *testing.T) {
	d := DecideCompaction(baseInput())
	wantDecision(t, d, true, "economic")
	if d.RequestsSinceLastCompaction != nil {
		t.Fatalf("got %v", *d.RequestsSinceLastCompaction)
	}
}
