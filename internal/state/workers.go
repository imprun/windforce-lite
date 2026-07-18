package state

import "time"

// WorkerRecord is the worker registry entry (ADR 0009 §6): the observable
// truth of which capabilities are alive right now. Slots is the worker's
// quantitative concurrency cap; labels stay qualitative.
type WorkerRecord struct {
	ID              string    `json:"id"`
	Group           string    `json:"group,omitempty"`
	Tags            []string  `json:"tags,omitempty"`
	Labels          []string  `json:"labels,omitempty"`
	Slots           int       `json:"slots"`
	StartedAt       time.Time `json:"startedAt"`
	LastHeartbeatAt time.Time `json:"lastHeartbeatAt"`
}

// WorkerLiveTTL is how recent a heartbeat must be for a worker to count as
// live in observability surfaces.
const WorkerLiveTTL = 90 * time.Second

// WorkerRegistryExpiry is how long a silent record survives before the
// registry drops it — crashed workers (no graceful deregister) must not
// accumulate forever. Live workers heartbeat every ~15s, far inside this.
const WorkerRegistryExpiry = 15 * time.Minute

// Live reports whether the record's heartbeat is fresh at the given time.
func (w WorkerRecord) Live(now time.Time) bool {
	return now.Sub(w.LastHeartbeatAt) <= WorkerLiveTTL
}
