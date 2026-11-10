package main

import (
	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
	"testing"
	"time"
)

func TestCleanupPendingBeforeTransferIsCounted(t *testing.T) {
	f := setup(t)
	_, key := f.register("client_cleanup_empty")
	f.events(key, finished("empty_dir_failure", "empty_dir_run", "quark", "daily", f.now, false, false))
	number(t, f.overview(), "cleanup_pending_runs", 1)
	number(t, f.overview(), "successful_cycles", 0)
	f.events(key, event("empty_dir_clean", "account_check_cleanup_finished", f.now.Add(time.Second), gin.H{"run_id": "empty_dir_run", "result": "passed", "duration_ms": 1, "error_code": nil, "deletion_mode": "recycle_bin"}))
	number(t, f.overview(), "cleanup_pending_runs", 0)
	number(t, f.overview(), "successful_cycles", 0)
}

func TestLocalStorageFailureReportAccepted(t *testing.T) {
	f := setup(t)
	_, key := f.register("client_storage_error")
	result := f.events(key, event("storage_error", "account_check_stage_finished", f.now, gin.H{
		"run_id": "storage_run", "stage": "prepare_target", "status": "unknown", "duration_ms": 1, "error_code": "LOCAL_STORAGE_FAILED",
	}))
	if len(result["accepted_event_ids"].([]any)) != 1 {
		t.Fatalf("storage failure rejected: %+v", result)
	}
	number(t, f.overview(), "successful_cycles", 0)
}

func TestLegacyUnconfirmedCleanupPendingMigrates(t *testing.T) {
	f := setup(t)
	instance, key := f.register("client_pending_migration")
	f.events(key, finished("legacy_pending_event", "legacy_pending_run", "quark", "daily", f.now, false, false))
	f.events(key, finished("legacy_passed_event", "legacy_passed_run", "quark", "daily", f.now, true, true))
	// Reproduce the prior stored flag and absence of this migration marker.
	if err := f.a.store.db.Update(func(tx *bolt.Tx) error {
		var r Run
		recordKey := instance + "|legacy_pending_run"
		if !get(tx, "runs", recordKey, &r) {
			t.Fatal("missing run")
		}
		r.CleanupPending = false
		if err := put(tx, "runs", recordKey, r); err != nil {
			return err
		}
		return bucket(tx, "settings").Delete([]byte("migration_cleanup_pending_v1"))
	}); err != nil {
		t.Fatal(err)
	}
	number(t, f.overview(), "cleanup_pending_runs", 0)
	for i := 0; i < 2; i++ {
		if err := f.a.store.initTelemetry(); err != nil {
			t.Fatal(err)
		}
		number(t, f.overview(), "cleanup_pending_runs", 1)
		number(t, f.overview(), "successful_cycles", 1)
	}
}
