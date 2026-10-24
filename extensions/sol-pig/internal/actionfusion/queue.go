// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/action-fusion/file-queue.ts at 2195680 (NVIDIA, MIT).

package actionfusion

import "sync"

// queueEntry exists only while its key is held; waiters are woken in arrival order.
type queueEntry struct {
	waiters []chan struct{}
}

var (
	queueMu      sync.Mutex
	queueEntries = map[string]*queueEntry{}
)

// WithFileQueue runs work once every earlier call on key has finished (FIFO); keys are released when unused.
func WithFileQueue(key string, work func() error) error {
	queueMu.Lock()
	if e := queueEntries[key]; e != nil {
		turn := make(chan struct{})
		e.waiters = append(e.waiters, turn)
		queueMu.Unlock()
		<-turn // ownership is handed over by the previous holder
	} else {
		queueEntries[key] = &queueEntry{}
		queueMu.Unlock()
	}
	defer release(key)
	return work()
}

func release(key string) {
	queueMu.Lock()
	defer queueMu.Unlock()
	e := queueEntries[key]
	if len(e.waiters) == 0 {
		delete(queueEntries, key)
		return
	}
	close(e.waiters[0])
	e.waiters = e.waiters[1:]
}
