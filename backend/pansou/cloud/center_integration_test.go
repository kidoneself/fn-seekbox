package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercises the six real client call paths against an isolated protocol server.
// It never registers test installations or fabricated successes in production.
func TestCenterSixInterfaceFlowAndRestart(t *testing.T) {
	s, _, a := functionService(t)
	calls := map[string]int{}
	seenEvents := map[string]bool{}
	kinds := map[string]int{}
	rejectBatch := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" "+r.URL.Path]++
		private := strings.HasPrefix(r.URL.Path, "/api/client/") && r.URL.Path != "/api/client/register"
		if private && r.Header.Get("Authorization") != "Bearer isolated-report-token" {
			t.Error("missing report credential")
		}
		if !private && r.Header.Get("Authorization") != "" {
			t.Error("credential leaked to public route")
		}
		var req map[string]any
		if r.Method == "POST" {
			if json.NewDecoder(r.Body).Decode(&req) != nil {
				t.Error("invalid JSON")
			}
		}
		var data any
		switch r.URL.Path {
		case "/api/check-link":
			if r.URL.Query().Get("provider") != "quark" {
				t.Error("wrong provider")
			}
			data = map[string]any{"provider": "quark", "share_url": "https://pan.quark.cn/s/probe", "password": "", "source_id": "source", "source_version": "1"}
		case "/api/search-sites":
			data = []CenterSite{{ID: "ikantv", Name: "站点", Enabled: false, Order: 1}}
		case "/api/client/register":
			if calls[r.Method+" "+r.URL.Path] != 1 {
				t.Error("registered again after restart")
			}
			if req["platform"] != "fnos" || len(req["registration_nonce"].(string)) != 64 {
				t.Error("bad identity")
			}
			data = map[string]any{"installation_id": "isolated-instance", "report_token": "isolated-report-token"}
		case "/api/client/config":
			data = map[string]any{"accepted_schema_versions": []int{1}, "heartbeat_interval_seconds": 600, "event_batch_max_items": 100, "event_batch_max_bytes": 262144}
		case "/api/client/heartbeat":
			data = map[string]any{"accepted_seq": req["snapshot_seq"], "applied": true}
		case "/api/client/events/batch":
			if rejectBatch {
				w.WriteHeader(503)
				json.NewEncoder(w).Encode(map[string]any{"code": "SERVICE_UNAVAILABLE"})
				return
			}
			accepted, duplicate := []string{}, []string{}
			for _, raw := range req["events"].([]any) {
				e := raw.(map[string]any)
				id := e["event_id"].(string)
				if seenEvents[id] {
					duplicate = append(duplicate, id)
					continue
				}
				seenEvents[id] = true
				accepted = append(accepted, id)
				kinds[e["type"].(string)]++
				encoded, _ := json.Marshal(e)
				for _, secret := range []string{"private-cookie", "01.mp4", "pan.quark.cn", "isolated-report-token"} {
					if strings.Contains(string(encoded), secret) {
						t.Error("sensitive event data")
					}
				}
				d := e["data"].(map[string]any)
				if e["type"] == "account_check_stage_finished" {
					if d["run_id"] == nil || d["stage"] == nil || d["status"] != "passed" {
						t.Error("bad stage event")
					}
					if _, ok := d["error_code"]; !ok {
						t.Error("missing nullable error_code")
					}
				}
			}
			data = map[string]any{"accepted_event_ids": accepted, "duplicate_event_ids": duplicate, "rejected_events": []any{}}
		default:
			t.Error("unexpected endpoint", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	s.centerBase = server.URL
	enabled := true
	if _, err := s.SetCenterConfig("owner", &enabled, &enabled); err != nil {
		t.Fatal(err)
	}
	s.syncCenter("owner")
	run := executeTestFunction(t, s, a)
	if run.Status != "passed" {
		t.Fatal(run.Error)
	}
	rejectBatch = true
	s.store.update("owner", func(st *userState) error { st.Center.LastAttemptAt = 0; return nil })
	s.syncCenter("owner")
	st, _ := s.store.read("owner")
	if len(st.Center.Events) != 9 {
		t.Fatalf("lost complete check events: %d", len(st.Center.Events))
	}
	originalIDs := map[string]bool{}
	for _, e := range st.Center.Events {
		originalIDs[e.ID] = true
	}
	dir := filepath.Dir(s.store.db.Path())
	if err := s.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	s.store = reopened
	st, _ = s.store.read("owner")
	for _, e := range st.Center.Events {
		if !originalIDs[e.ID] {
			t.Fatal("event ID changed across restart")
		}
	}
	rejectBatch = false
	s.store.update("owner", func(st *userState) error { st.Center.LastAttemptAt = 0; st.Center.LastReportAt = 0; return nil })
	s.syncCenter("owner")
	st, _ = s.store.read("owner")
	if len(st.Center.Events) != 0 || st.Center.LastError != "" || kinds["account_check_started"] != 1 || kinds["account_check_stage_finished"] != 7 || kinds["account_check_finished"] != 1 {
		t.Fatalf("incomplete flow: kinds=%v error=%s", kinds, st.Center.LastError)
	}
	for _, route := range []string{"GET /api/check-link", "GET /api/search-sites", "POST /api/client/register", "GET /api/client/config", "POST /api/client/heartbeat", "POST /api/client/events/batch"} {
		if calls[route] == 0 {
			t.Error("unused interface", route)
		}
	}
}

func TestCenterRateLimitAndHeartbeatReceipt(t *testing.T) {
	s, _, _ := functionService(t)
	if err := s.saveCenterSecret("owner", Credentials{"report_token": "test-token"}); err != nil {
		t.Fatal(err)
	}
	mode := "limited"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if mode == "limited" {
			w.Header().Set("Retry-After", "300")
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]any{"code": "RATE_LIMITED"})
			return
		}
		var req struct {
			Seq uint64 `json:"snapshot_seq"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var data any = map[string]any{}
		if mode == "newer" {
			data = map[string]any{"accepted_seq": uint64(50), "applied": false}
		}
		if mode == "accepted" {
			if req.Seq != 51 {
				t.Error("did not recover sequence", req.Seq)
			}
			data = map[string]any{"accepted_seq": req.Seq, "applied": true}
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	s.centerBase = server.URL
	s.store.update("owner", func(st *userState) error {
		st.Center.Config.ReportingEnabled = true
		st.Center.PolicyUpdatedAt = now()
		st.Center.SitesUpdatedAt = now()
		st.Center.Policy = centerPolicy{Schemas: []int{1}, HeartbeatSeconds: 600}
		return nil
	})
	s.syncCenter("owner")
	st, _ := s.store.read("owner")
	if st.Center.NextAttemptAt < now()+290000 || st.Center.LastReportAt != 0 {
		t.Fatal("limit not preserved")
	}
	s.syncCenter("owner")
	if calls != 1 {
		t.Fatal("retried within backoff")
	}
	for _, next := range []string{"invalid", "newer", "accepted"} {
		mode = next
		s.store.update("owner", func(st *userState) error { st.Center.NextAttemptAt = 0; st.Center.LastAttemptAt = 0; return nil })
		s.syncCenter("owner")
		st, _ = s.store.read("owner")
		if next != "accepted" && st.Center.LastReportAt != 0 {
			t.Fatal("unacknowledged heartbeat reported as success")
		}
	}
	if st.Center.LastReportAt == 0 || st.Center.LastError != "" {
		t.Fatal("valid heartbeat not accepted")
	}
	at := time.Now().UTC().Truncate(time.Second)
	if centerRetryAfter(at.Add(2*time.Minute).Format(http.TimeFormat), at) != 2*time.Minute {
		t.Fatal("HTTP-date retry unsupported")
	}
}

func TestCenterSitesKeepsDisabledCatalogOnOutage(t *testing.T) {
	s, _, _ := functionService(t)
	offline := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if offline {
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]any{"code": "SERVICE_UNAVAILABLE"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": []CenterSite{{ID: "ikantv", Enabled: false}}})
	}))
	defer server.Close()
	s.centerBase = server.URL
	sites, stale, err := s.CenterSites(context.Background(), "owner")
	if err != nil || stale || len(sites) != 1 || sites[0].Enabled {
		t.Fatal("catalog not applied")
	}
	offline = true
	s.CenterSites(context.Background(), "owner")
	if calls != 1 {
		t.Fatal("cache ignored")
	}
	s.store.update("owner", func(st *userState) error { st.Center.SitesUpdatedAt = now() - 120000; return nil })
	sites, stale, err = s.CenterSites(context.Background(), "owner")
	if err != nil || !stale || len(sites) != 1 || sites[0].Enabled {
		t.Fatal("outage discarded last valid configuration")
	}
}
