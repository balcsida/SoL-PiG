// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/action-fusion/file-queue.ts at 2195680 (NVIDIA, MIT).

package actionfusion

import "sync"

type queueEntry struct {
	mu      sync.Mutex
	waiters int
}

var (
	queueMu      sync.Mutex
	queueEntries = map[string]*queueEntry{}
)

// WithFileQueue runs work while holding the per-key lock; keys are released when unused.
func WithFileQueue(key string, work func() error) error {
	queueMu.Lock()
	e := queueEntries[key]
	if e == nil {
		e = &queueEntry{}
		queueEntries[key] = e
	}
	e.waiters++
	queueMu.Unlock()

	e.mu.Lock()
	defer func() {
		e.mu.Unlock()
		queueMu.Lock()
		if e.waiters--; e.waiters == 0 {
			delete(queueEntries, key)
		}
		queueMu.Unlock()
	}()
	return work()
}
