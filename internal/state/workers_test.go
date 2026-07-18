package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestListWorkersDropsSilentRecords(t *testing.T) {
	store := NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	ctx := context.Background()
	if err := store.RegisterWorker(ctx, WorkerRecord{ID: "w-live"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterWorker(ctx, WorkerRecord{ID: "w-crashed"}); err != nil {
		t.Fatal(err)
	}
	// A crashed worker never deregisters — simulate its silence.
	if err := store.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		record := snapshot.Workers["w-crashed"]
		record.LastHeartbeatAt = now.Add(-WorkerRegistryExpiry - time.Minute)
		snapshot.Workers["w-crashed"] = record
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	workers, err := store.ListWorkers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].ID != "w-live" {
		t.Fatalf("workers = %#v, want only w-live", workers)
	}
	// The silent record is gone from storage, not just filtered.
	snapshot, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.Workers["w-crashed"]; ok {
		t.Fatal("silent worker record still stored after ListWorkers prune")
	}
}
