// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/online-context-compact/economics.ts at 2195680 (NVIDIA, MIT).

// Package occ ports SoL-Pi's Online Context Compact: the economics, plan and
// state logic is pure; nothing here calls the host.
package occ

import "math"

type Economics struct {
	RemainingRequestScale, RemainingRequestStddevK float64
	WindowReserveTokens                            int
	FirstCompactionRequestScale                    float64
	SubsequentCompactionMargin                     float64
	// MinimumRequestsSinceCompaction is the cooldown before economics may compact again.
	MinimumRequestsSinceCompaction int
}

var DefaultEconomics = Economics{1, 0, 16384, 2, 1.5, 2}

const (
	minimumVarianceSamples = 3
	smallSampleScale       = 0.5
)

// Horizon ports RequestHorizonEstimate.
type Horizon struct {
	MeanBoundaryRequests, LowerBound float64
	UnboundedRemaining               int
	WindowRequestUpperBound          *int
	ExpectedRemaining                int
}

// EstimateRemainingRequests ports estimateRemainingRequests.
func EstimateRemainingRequests(counts []int, remainingBoundaries int, contextTokens int, window *int, avgIncrement *float64, e Economics) Horizon {
	total := 0
	for _, c := range counts {
		total += c
	}
	mean := float64(total) / math.Max(1, float64(len(counts)))
	lower := mean
	if e.RemainingRequestStddevK != 0 {
		if len(counts) < minimumVarianceSamples {
			lower *= smallSampleScale
		} else {
			variance := 0.0
			for _, c := range counts {
				variance += math.Pow(float64(c)-mean, 2)
			}
			deviation := math.Sqrt(variance / float64(len(counts)-1))
			lower = math.Max(0, mean-e.RemainingRequestStddevK*deviation)
		}
	}
	unbounded := 1 + int(math.Floor(lower*math.Max(0, float64(remainingBoundaries))*e.RemainingRequestScale))
	var upper *int
	if window != nil && avgIncrement != nil && *avgIncrement > 0 {
		u := int(math.Max(0, math.Floor(float64(*window-contextTokens) / *avgIncrement)))
		upper = &u
	}
	expected := unbounded
	if upper != nil {
		expected = min(unbounded, *upper)
	}
	return Horizon{mean, lower, unbounded, upper, expected}
}

// DecisionInput is decideCompaction's input; CompletedBoundaryRequestCounts nil means unavailable.
type DecisionInput struct {
	WriteTokens, ArchiveTokens, MemoTokens, ContextTokens int
	CompletedBoundaryRequestCounts                        []int
	RemainingBoundaries                                   int
	AverageContextTokenIncrement                          *float64
	ContextWindowTokens                                   *int
	PriorCompactionCount                                  int
	// RequestsSinceLastCompaction is nil when never compacted.
	RequestsSinceLastCompaction *int
	CarriedDebtTokens           float64
	CacheDebtRepaymentTokens    float64
	CacheWriteReadRatio         *float64
	Economics                   Economics
}

// Decision ports CompactionDecision; the leading fields echo the input.
type Decision struct {
	WriteTokens, ArchiveTokens, MemoTokens, ContextTokens int
	PriorCompactionCount                                  int
	RequestsSinceLastCompaction                           *int
	CarriedDebtTokens, CacheDebtRepaymentTokens           float64
	CacheWriteReadRatio                                   *float64

	Compact                                                                bool
	Reason                                                                 string
	SavingTokens, PostCompactionTokens                                     int
	IncrementalCacheCostRatio                                              *float64
	NewDebtTokens                                                          float64
	BreakevenRequests, CombinedBreakevenRequests, EffectiveHorizonRequests *float64
	Horizon                                                                *Horizon
}

// DecideCompaction ports decideCompaction.
func DecideCompaction(in DecisionInput) Decision {
	e := in.Economics
	var horizon *Horizon
	if in.CompletedBoundaryRequestCounts != nil {
		h := EstimateRemainingRequests(in.CompletedBoundaryRequestCounts, in.RemainingBoundaries, in.ContextTokens, in.ContextWindowTokens, in.AverageContextTokenIncrement, e)
		horizon = &h
	}
	saving := in.ArchiveTokens - in.MemoTokens
	post := max(0, in.WriteTokens-saving)
	var incremental *float64
	if in.CacheWriteReadRatio != nil {
		v := math.Max(0, *in.CacheWriteReadRatio-1)
		incremental = &v
	}
	newDebt := 0.0
	if incremental != nil {
		newDebt = float64(post) * *incremental
	}
	var breakeven, combined *float64
	if saving > 0 && incremental != nil {
		v := newDebt / float64(saving)
		breakeven = &v
		if repayment := in.CacheDebtRepaymentTokens + float64(saving); repayment > 0 {
			c := (in.CarriedDebtTokens + newDebt) / repayment
			combined = &c
		}
	}
	first := in.PriorCompactionCount == 0
	var effective *float64
	if horizon != nil {
		v := float64(horizon.ExpectedRemaining)
		if first {
			v *= e.FirstCompactionRequestScale
			if horizon.WindowRequestUpperBound != nil {
				v = math.Min(v, float64(*horizon.WindowRequestUpperBound))
			}
		}
		effective = &v
	}
	windowProtection := in.ContextWindowTokens != nil && in.ContextTokens >= *in.ContextWindowTokens-e.WindowReserveTokens
	expected := 0.0
	if horizon != nil {
		expected = float64(horizon.ExpectedRemaining)
	}
	baseEconomic := horizon != nil && horizon.ExpectedRemaining > 0 && breakeven != nil && *breakeven <= expected
	firstEconomic := first && effective != nil && *effective > 0 && breakeven != nil && *breakeven <= *effective
	marginOpen := !first && horizon != nil && breakeven != nil && *breakeven*e.SubsequentCompactionMargin <= expected
	carriedOpen := !first && horizon != nil && combined != nil && *combined <= expected
	economic := baseEconomic && marginOpen && carriedOpen
	if first {
		economic = firstEconomic
	}
	compressible := saving > 0
	cooldown := in.RequestsSinceLastCompaction != nil && *in.RequestsSinceLastCompaction < e.MinimumRequestsSinceCompaction
	// Window protection stays absolute: near the context limit, compact even in cooldown.
	compact := compressible && (windowProtection || (economic && !cooldown))

	var reason string
	switch {
	case !compressible:
		reason = "non_positive_saving"
	case windowProtection:
		reason = "window_protection"
	case economic && cooldown:
		reason = "deferred_post_compaction_cooldown"
	case economic:
		reason = "economic"
	case horizon == nil:
		reason = "horizon_unavailable"
	case breakeven == nil:
		reason = "cache_ratio_unavailable"
	case !first && baseEconomic && !marginOpen:
		reason = "deferred_subsequent_margin"
	case !first && baseEconomic && !carriedOpen:
		reason = "deferred_carried_debt"
	default:
		reason = "deferred_economic"
	}

	return Decision{
		WriteTokens: in.WriteTokens, ArchiveTokens: in.ArchiveTokens, MemoTokens: in.MemoTokens, ContextTokens: in.ContextTokens,
		PriorCompactionCount: in.PriorCompactionCount, RequestsSinceLastCompaction: in.RequestsSinceLastCompaction,
		CarriedDebtTokens: in.CarriedDebtTokens, CacheDebtRepaymentTokens: in.CacheDebtRepaymentTokens,
		CacheWriteReadRatio: in.CacheWriteReadRatio,
		Compact:             compact, Reason: reason, SavingTokens: saving, PostCompactionTokens: post,
		IncrementalCacheCostRatio: incremental, NewDebtTokens: newDebt,
		BreakevenRequests: breakeven, CombinedBreakevenRequests: combined, EffectiveHorizonRequests: effective,
		Horizon: horizon,
	}
}
