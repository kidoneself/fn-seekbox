package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func pendingReleaseEvents(t *testing.T, s *Service) int {
	t.Helper()
	count := 0
	if err := s.store.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(updateEventsBucket); b != nil {
			count = b.Stats().KeyN
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestUpdateReportDurableRetryAcknowledgmentAndStableOwner(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{store: st, ctx: context.Background(), wake: make(chan struct{}, 1), centerClient: centerHTTPClient()}
	defer func() { s.store.db.Close() }()
	event := ReleaseEvent{ReleaseID: "release-1", EventID: "event-1", Version: "1.6.1", FromVersion: "1.6.0", Status: "success", OccurredAt: time.Now().UTC()}
	if err := s.EnqueueReleaseEvent(event); err != nil {
		t.Fatal(err)
	}
	s.syncReleaseReports() // No registered user: retain the durable event.
	if pendingReleaseEvents(t, s) != 1 {
		t.Fatal("event lost before registration")
	}
	for _, owner := range []string{"alice", "bob"} {
		if err := s.store.update(owner, func(st *userState) error { st.Center.Config.ReportingEnabled = true; return nil }); err != nil {
			t.Fatal(err)
		}
		if err := s.saveCenterSecret(owner, Credentials{"report_token": owner + "-token"}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	ack := false
	accepted := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/client/releases/report" || r.Header.Get("Authorization") != "Bearer alice-token" {
			t.Error("unstable reporting identity")
		}
		var got ReleaseEvent
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		accepted[got.EventID] = true
		if ack {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"event_id": got.EventID, "accepted": true}})
		} else {
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]any{"code": "TEMPORARY"})
		}
	}))
	defer server.Close()
	s.centerBase = server.URL
	s.syncReleaseReports()
	if pendingReleaseEvents(t, s) != 1 || calls != 1 {
		t.Fatal("retry event not retained")
	}
	// Reopening the database simulates a restart between submission and retry.
	if err := s.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.store = reopened
	if pendingReleaseEvents(t, s) != 1 {
		t.Fatal("pending receipt did not survive restart")
	}
	// Choosing a new, earlier owner after a failed request must not change identity.
	s.store.update("aaron", func(st *userState) error { st.Center.Config.ReportingEnabled = true; return nil })
	s.saveCenterSecret("aaron", Credentials{"report_token": "aaron-token"})
	ack = true
	s.store.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(updateMetadataBucket).Delete([]byte("report_retry_at")) })
	s.syncReleaseReports()
	if pendingReleaseEvents(t, s) != 0 || calls != 2 || len(accepted) != 1 {
		t.Fatal("receipt did not confirm exactly once")
	}
	if err := s.EnqueueReleaseEvent(event); err != nil {
		t.Fatal(err)
	} // helper retry remains server-idempotent
	s.syncReleaseReports()
	if len(accepted) != 1 || pendingReleaseEvents(t, s) != 0 {
		t.Fatal("duplicate counted twice")
	}
}
