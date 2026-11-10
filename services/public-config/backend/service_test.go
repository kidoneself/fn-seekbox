package main

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	t            *testing.T
	a            *app
	handler      http.Handler
	now          time.Time
	cookie       *http.Cookie
	dbPath, seed string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir, tempErr := os.MkdirTemp(os.Getenv("SEEKBOX_TEST_TMPDIR"), "seekbox-test-")
	if tempErr != nil {
		t.Fatal(tempErr)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	seed := filepath.Join(dir, "seed.json")
	if e := os.WriteFile(seed, []byte(`{"check_links":[],"search_sites":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := openStore(filepath.Join(dir, "test.db"), seed)
	if e != nil {
		t.Fatal(e)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("test-password-123"), bcrypt.MinCost)
	f := &fixture{t: t, now: time.Date(2026, 11, 5, 12, 0, 0, 0, beijing), dbPath: filepath.Join(dir, "test.db"), seed: seed}
	f.a = newApp(s, "https://hermes.smartloan.cn", "", hash)
	f.a.now = func() time.Time { return f.now }
	f.handler = f.a.router()
	t.Cleanup(func() { f.a.store.db.Close() })
	w, _ := f.req("POST", adminPrefix+"/login", gin.H{"password": "test-password-123"}, "", false, 200)
	f.cookie = w.Result().Cookies()[0]
	return f
}
func (f *fixture) req(method, path string, body any, token string, session bool, status int) (*httptest.ResponseRecorder, map[string]any) {
	f.t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://hermes.smartloan.cn")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if session {
		r.AddCookie(f.cookie)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != status {
		f.t.Fatalf("%s %s status=%d expected=%d: %s", method, path, w.Code, status, w.Body.String())
	}
	var out map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		f.t.Fatal(e)
	}
	if out["request_id"] == "" {
		f.t.Fatal("missing request ID")
	}
	return w, out
}
func data(m map[string]any) map[string]any { return m["data"].(map[string]any) }
func (f *fixture) register(client string) (string, string) {
	_, r := f.req("POST", "/api/client/register", gin.H{"schema_version": 1, "client_instance_id": client, "registration_nonce": strings.Repeat("a", 43), "app_version": "1.5.13", "platform": "fnos", "arch": "amd64"}, "", false, 200)
	return data(r)["installation_id"].(string), data(r)["report_token"].(string)
}
func (f *fixture) events(token string, events ...Event) map[string]any {
	_, r := f.req("POST", "/api/client/events/batch", gin.H{"schema_version": 1, "batch_id": "batch_a", "events": events}, token, false, 200)
	return data(r)
}
func event(id, kind string, at time.Time, d gin.H) Event {
	raw, _ := json.Marshal(d)
	return Event{id, kind, at, "1.5.13", raw}
}
func finished(id, run, provider, trigger string, at time.Time, clean, transferred bool) Event {
	var schedule any = midnight(at)
	if trigger == "manual" {
		schedule = nil
	}
	ss := []gin.H{}
	for _, s := range stages {
		status := "passed"
		if !clean && oneOf(s, "delete", "verify_cleanup") {
			status = "failed"
		}
		if !transferred && oneOf(s, "transfer", "verify_transfer") {
			status = "failed"
		}
		ss = append(ss, gin.H{"stage": s, "status": status, "duration_ms": 1, "error_code": nil})
	}
	result := "passed"
	if !clean || !transferred {
		result = "failed"
	}
	return event(id, "account_check_finished", at, gin.H{"run_id": run, "account_ref": "acct_" + provider, "provider": provider, "trigger": trigger, "scheduled_for": schedule, "attempt": 1, "source_id": "probe_" + provider + "_01", "source_version": "1", "auth_status": "valid", "result": result, "failed_stage": nil, "error_code": nil, "duration_ms": 7, "cleanup_pending": !clean, "deletion_mode": "recycle_bin", "stages": ss})
}
func (f *fixture) overview() map[string]any {
	_, r := f.req("GET", adminPrefix+"/analytics/overview", nil, "", true, 200)
	return data(r)
}
func number(t *testing.T, m map[string]any, k string, n float64) {
	t.Helper()
	if m[k] != n {
		t.Fatalf("%s=%v want %v", k, m[k], n)
	}
}
func TestPublicConfigurationImmediateAndErrors(t *testing.T) {
	f := setup(t)
	for _, p := range providers {
		_, r := f.req("GET", "/api/check-link?provider="+p, nil, "", false, 404)
		if r["code"] != "SOURCE_NOT_CONFIGURED" {
			t.Fatal(r)
		}
		host := map[string]string{"baidu": "pan.baidu.com", "quark": "pan.quark.cn", "xunlei": "pan.xunlei.com"}[p]
		body := gin.H{"share_url": "https://" + host + "/s/LOCAL_TEST_FIXTURE", "password": "", "file_path": "功能检查.txt", "enabled": true}
		_, put := f.req("PUT", adminPrefix+"/check-links/"+p, body, "", true, 200)
		if data(put)["source_version"] != "2" {
			t.Fatal(put)
		}
		w, r := f.req("GET", "/api/check-link?provider="+p, nil, "", false, 200)
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || data(r)["password"] != "" {
			t.Fatal(r)
		}
		body["file_path"] = "新测试.txt"
		f.req("PUT", adminPrefix+"/check-links/"+p, body, "", true, 200)
		_, r = f.req("GET", "/api/check-link?provider="+p, nil, "", false, 200)
		if data(r)["file_path"] != "新测试.txt" || data(r)["source_version"] != "3" {
			t.Fatal(r)
		}
		body["expires_at"] = f.now.Add(-time.Minute)
		f.req("PUT", adminPrefix+"/check-links/"+p, body, "", true, 200)
		_, r = f.req("GET", "/api/check-link?provider="+p, nil, "", false, 503)
		if r["code"] != "SOURCE_EXPIRED" {
			t.Fatal(r)
		}
		body["enabled"] = false
		f.req("PUT", adminPrefix+"/check-links/"+p, body, "", true, 200)
		_, r = f.req("GET", "/api/check-link?provider="+p, nil, "", false, 503)
		if r["code"] != "SOURCE_DISABLED" {
			t.Fatal(r)
		}
	}
	for _, q := range []string{"", "?provider=bad", "?provider=baidu&provider=quark", "?provider=baidu&extra=1"} {
		f.req("GET", "/api/check-link"+q, nil, "", false, 400)
	}
	f.req("GET", adminPrefix+"/config", nil, "", false, 401)
	f.req("POST", "/api/search-sites", gin.H{}, "", false, 405)
	f.req("GET", "/seekbox-config/", nil, "", false, 404)
	f.req("POST", adminPrefix+"/sites", gin.H{"id": "real_id", "name": "Test fixture", "url": "https://example.org", "enabled": true, "order": 1}, "", true, 201)
	f.req("PUT", adminPrefix+"/sites/real_id", gin.H{"name": "Renamed fixture", "url": "https://example.net", "enabled": false, "order": 2}, "", true, 200)
	_, sites := f.req("GET", "/api/search-sites", nil, "", false, 200)
	item := sites["data"].([]any)[0].(map[string]any)
	if item["id"] != "real_id" || item["enabled"] != false {
		t.Fatal(item)
	}
	f.req("DELETE", adminPrefix+"/sites/real_id", gin.H{}, "", true, 200)
	f.req("POST", adminPrefix+"/sites", gin.H{"id": "real_id", "name": "x", "url": "https://example.org", "enabled": true, "order": 1}, "", true, 409)
}
func TestAPIKeyDynamicAuthorizationAndNoFallback(t *testing.T) {
	f := setup(t)
	body := gin.H{"name": "Test caller", "permissions": []string{}, "enabled": true, "expires_at": nil}
	_, r := f.req("POST", adminPrefix+"/api-keys", body, "", true, 200)
	key := data(r)["api_key"].(string)
	id := data(r)["key"].(map[string]any)["id"].(string)
	f.req("GET", adminPrefix+"/config", nil, key, true, 403)
	f.req("GET", adminPrefix+"/config", nil, "invalid", true, 401)
	body["permissions"] = []string{"GET " + adminPrefix + "/config"}
	f.req("PUT", adminPrefix+"/api-keys/"+id, body, "", true, 200)
	f.req("GET", adminPrefix+"/config", nil, key, false, 200)
	f.req("PUT", adminPrefix+"/check-links/baidu", gin.H{}, key, true, 403)
	body["permissions"] = []string{}
	f.req("PUT", adminPrefix+"/api-keys/"+id, body, "", true, 200)
	f.req("GET", adminPrefix+"/config", nil, key, true, 403)
	body["permissions"] = []string{"GET " + adminPrefix + "/config", "GET " + adminPrefix + "/client-settings"}
	f.req("PUT", adminPrefix+"/api-keys/"+id, body, "", true, 200)
	f.req("GET", adminPrefix+"/client-settings", nil, key, false, 200)
	f.req("PATCH", adminPrefix+"/client-settings", gin.H{}, key, true, 403)
	_, r = f.req("POST", adminPrefix+"/api-keys/"+id+"/rotate", gin.H{}, "", true, 200)
	next := data(r)["api_key"].(string)
	f.req("GET", adminPrefix+"/config", nil, key, true, 401)
	f.req("GET", adminPrefix+"/config", nil, next, false, 200)
	body["enabled"] = false
	f.req("PUT", adminPrefix+"/api-keys/"+id, body, "", true, 200)
	f.req("GET", adminPrefix+"/config", nil, next, true, 401)
	body["enabled"] = true
	body["expires_at"] = f.now.Add(-time.Minute)
	f.req("PUT", adminPrefix+"/api-keys/"+id, body, "", true, 200)
	f.req("GET", adminPrefix+"/config", nil, next, true, 401)
}
func TestRegistrationRetryIsolationAndRevocation(t *testing.T) {
	f := setup(t)
	id, key := f.register("client_a")
	id2, key2 := f.register("client_a")
	if id != id2 || key != key2 {
		t.Fatal("retry changed credentials")
	}
	_, other := f.register("client_b")
	if key == other {
		t.Fatal("credentials reused")
	}
	f.req("POST", "/api/client/register", gin.H{"schema_version": 1, "client_instance_id": "client_a", "registration_nonce": strings.Repeat("b", 43), "app_version": "1.5.13", "platform": "fnos", "arch": "amd64"}, "", false, 409)
	f.req("GET", adminPrefix+"/analytics/overview", nil, key, true, 401)
	f.now = f.now.Add(11 * time.Minute)
	f.req("POST", "/api/client/register", gin.H{"schema_version": 1, "client_instance_id": "client_a", "registration_nonce": strings.Repeat("a", 43), "app_version": "1.5.13", "platform": "fnos", "arch": "amd64"}, "", false, 409)
	f.req("POST", adminPrefix+"/instances/"+id+"/revoke", gin.H{}, "", true, 200)
	f.req("GET", "/api/client/config", nil, key, true, 401)
	f.req("GET", "/api/client/config", nil, other, false, 200)
}
func TestHeartbeatSequencePartialSnapshotsAndOffline(t *testing.T) {
	f := setup(t)
	id, key := f.register("client_a")
	body := gin.H{"schema_version": 1, "snapshot_seq": 2, "reported_at": f.now, "app_version": "1.5.13", "platform": "fnos", "arch": "amd64", "accounts_complete": true, "accounts": []gin.H{{"account_ref": "acct_a", "provider": "baidu", "auth_status": "valid", "check_enabled": true}}, "subscriptions": gin.H{"total": 0, "enabled": 0}, "active_jobs": 0}
	f.req("POST", "/api/client/heartbeat", body, key, false, 200)
	body["snapshot_seq"] = 1
	body["accounts"] = []gin.H{}
	_, r := f.req("POST", "/api/client/heartbeat", body, key, false, 200)
	if data(r)["applied"] != false {
		t.Fatal(r)
	}
	body["snapshot_seq"] = 2
	f.req("POST", "/api/client/heartbeat", body, key, false, 409)
	body["snapshot_seq"] = 3
	body["accounts_complete"] = false
	f.req("POST", "/api/client/heartbeat", body, key, false, 200)
	f.a.store.db.View(func(tx *bolt.Tx) error {
		var a Account
		get(tx, "accounts", id+"|acct_a", &a)
		if a.Removed {
			t.Fatal("partial removed account")
		}
		return nil
	})
	f.now = f.now.Add(time.Hour)
	body["snapshot_seq"] = 4
	body["accounts_complete"] = true
	f.req("POST", "/api/client/heartbeat", body, key, false, 200)
	number(t, f.overview(), "online_instances", 0)
	f.a.store.db.View(func(tx *bolt.Tx) error {
		var a Account
		get(tx, "accounts", id+"|acct_a", &a)
		if !a.Removed {
			t.Fatal("full snapshot failed to remove")
		}
		return nil
	})
}
func TestCycleIdempotencyPartialBatchAndPersistence(t *testing.T) {
	f := setup(t)
	_, key := f.register("client_a")
	es := []Event{}
	for _, p := range providers {
		es = append(es, finished("event_"+p, "run_"+p, p, "daily", f.now, true, true))
	}
	r := f.events(key, es...)
	if len(r["accepted_event_ids"].([]any)) != 3 {
		t.Fatal(r)
	}
	r = f.events(key, es...)
	if len(r["duplicate_event_ids"].([]any)) != 3 {
		t.Fatal(r)
	}
	es[0].ID = "new_id"
	f.events(key, es[0])
	o := f.overview()
	number(t, o, "successful_cycles", 3)
	for _, p := range providers {
		number(t, o["successful_cycles_by_provider"].(map[string]any), p, 1)
	}
	conflict := es[0]
	conflict.AppVersion = "changed"
	r = f.events(key, conflict)
	if r["rejected_events"].([]any)[0].(map[string]any)["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatal(r)
	}
	bad := finished("bad", "bad_run", "baidu", "daily", f.now, false, false)
	bad.Type = "search_finished"
	r = f.events(key, bad, finished("manual_event", "manual_run", "quark", "manual", f.now, true, true))
	if len(r["rejected_events"].([]any)) != 1 || len(r["accepted_event_ids"].([]any)) != 1 {
		t.Fatal(r)
	}
	number(t, f.overview(), "manual_successful_cycles", 1)
	number(t, f.overview(), "successful_cycles", 3)
	f.events(key, finished("failed_transfer", "failed_transfer", "baidu", "daily", f.now, true, false))
	number(t, f.overview(), "successful_cycles", 3)
	f.a.store.db.Close()
	s, e := openStore(f.dbPath, f.seed)
	if e != nil {
		t.Fatal(e)
	}
	f.a.store = s
	f.events(key, es[0])
	number(t, f.overview(), "successful_cycles", 3)
	f.now = f.now.AddDate(0, 0, 100)
	w, _ := f.req("POST", adminPrefix+"/login", gin.H{"password": "test-password-123"}, "", false, 200)
	f.cookie = w.Result().Cookies()[0]
	f.a.maintain()
	number(t, f.overview(), "cumulative_successful_cycles", 3)
	r = f.events(key, es[0])
	if r["rejected_events"].([]any)[0].(map[string]any)["code"] != "EVENT_TOO_OLD" {
		t.Fatal(r)
	}
}
func TestLateCleanupOutOfOrderAndCrossDayCorrection(t *testing.T) {
	f := setup(t)
	id, key := f.register("client_a")
	yesterday := f.now.AddDate(0, 0, -1)
	f.events(key, finished("initial", "recover", "baidu", "daily", yesterday, false, true))
	number(t, f.overview(), "cumulative_successful_cycles", 0)
	clean := event("cleanup", "account_check_cleanup_finished", f.now, gin.H{"run_id": "recover", "result": "passed", "duration_ms": 1, "error_code": nil, "deletion_mode": "recycle_bin"})
	f.events(key, clean)
	number(t, f.overview(), "successful_cycles", 1)
	clean.ID = "cleanup_again"
	clean.OccurredAt = f.now.Add(time.Minute)
	f.events(key, clean)
	number(t, f.overview(), "successful_cycles", 1)
	f.a.store.db.View(func(tx *bolt.Tx) error {
		var run Run
		get(tx, "runs", id+"|recover", &run)
		if run.OriginalResult != "failed" || run.CleanupPending || !run.CycleCompletedAt.Equal(f.now) {
			t.Fatal(run)
		}
		return nil
	})
	clean.ID = "orphan_clean"
	clean.Data = []byte(`{"run_id":"orphan","result":"passed","duration_ms":1,"error_code":null,"deletion_mode":"recycle_bin"}`)
	f.events(key, clean)
	number(t, f.overview(), "successful_cycles", 1)
	f.events(key, finished("orphan_terminal", "orphan", "quark", "daily", yesterday, false, true))
	number(t, f.overview(), "successful_cycles", 2)
	at := midnight(f.now).Add(time.Minute)
	f.events(key, finished("cross", "cross", "xunlei", "daily", at, true, true))
	number(t, f.overview(), "successful_cycles", 3)
	f.events(key, event("precise_cleanup", "account_check_stage_finished", midnight(f.now).Add(-time.Minute), gin.H{"run_id": "cross", "stage": "verify_cleanup", "status": "passed", "duration_ms": 1, "error_code": nil}))
	number(t, f.overview(), "successful_cycles", 2)
	number(t, f.overview(), "cumulative_successful_cycles", 3)
}
func TestRetentionContinuousRunningAndExpectedSlots(t *testing.T) {
	f := setup(t)
	id, key := f.register("client_a")
	f.a.store.db.Update(func(tx *bolt.Tx) error {
		var i Instance
		get(tx, "instances", id, &i)
		i.RegisteredAt = f.now.AddDate(0, 0, -8)
		put(tx, "instances", id, i)
		for n := 1; n <= 7; n++ {
			markRunning(tx, id, f.now.AddDate(0, 0, -n))
		}
		put(tx, "accounts", id+"|account", Account{AccountSnapshot: AccountSnapshot{Ref: "account", Provider: "baidu", CheckEnabled: true}, InstallationID: id, FirstSeen: f.now.AddDate(0, 0, -2), History: []AccountChange{{At: f.now.AddDate(0, 0, -2), Enabled: true}}})
		return nil
	})
	f.a.maintain()
	o := f.overview()
	number(t, o, "continuous_running_instances_7d", 1)
	number(t, o, "expected_daily_slots", 1)
	number(t, o, "unreported_daily_slots", 1)
	q := url.Values{"metric": {"retention_d7"}, "from": {midnight(f.now).AddDate(0, 0, -8).Format(time.RFC3339)}, "to": {midnight(f.now).AddDate(0, 0, -7).Format(time.RFC3339)}}
	_, r := f.req("GET", adminPrefix+"/analytics/trends?"+q.Encode(), nil, "", true, 200)
	item := data(r)["items"].([]any)[0].(map[string]any)
	if item["matured"] != true || item["value"] != float64(1) {
		t.Fatal(item)
	}
	_, r = f.req("GET", adminPrefix+"/analytics/trends?metric=retention_d7", nil, "", true, 200)
	for _, row := range data(r)["items"].([]any) {
		x := row.(map[string]any)
		if x["matured"] != false || x["value"] != nil {
			t.Fatal(x)
		}
	}
	f.req("GET", adminPrefix+"/analytics/trends?metric=search_count", nil, "", true, 400)
	f.req("GET", adminPrefix+"/analytics/trends?metric=retention_d7&provider=baidu", nil, "", true, 400)
	f.req("GET", adminPrefix+"/analytics/overview?timezone=UTC", nil, "", true, 400)
	failEvent := finished("failed_running", "failed_running", "baidu", "daily", f.now, false, false)
	f.events(key, failEvent)
	number(t, f.overview(), "successful_cycles", 0)
	number(t, f.overview(), "running_instances_7d", 1)
}

func TestBatchLimitsUnknownFieldsIsolationAndPagination(t *testing.T) {
	f := setup(t)
	id, key := f.register("guard_a")
	other, otherKey := f.register("guard_b")
	e := finished("same_event", "same_run", "baidu", "daily", f.now, true, true)
	f.events(key, e)
	f.events(otherKey, e)
	number(t, f.overview(), "successful_cycles", 2)
	_, r := f.req("GET", adminPrefix+"/analytics/checks?limit=1", nil, "", true, 200)
	if len(data(r)["items"].([]any)) != 1 || data(r)["next_cursor"] == nil {
		t.Fatal(r)
	}
	cursor := data(r)["next_cursor"].(string)
	_, r = f.req("GET", adminPrefix+"/analytics/checks?limit=1&cursor="+url.QueryEscape(cursor), nil, "", true, 200)
	if len(data(r)["items"].([]any)) != 1 || data(r)["next_cursor"] != nil {
		t.Fatal(r)
	}
	f.req("GET", adminPrefix+"/analytics/checks?provider=baidu&cursor="+url.QueryEscape(cursor), nil, "", true, 400)
	_, r = f.req("GET", adminPrefix+"/analytics/checks?installation_id="+id, nil, "", true, 200)
	if len(data(r)["items"].([]any)) != 1 {
		t.Fatal(r)
	}
	_, r = f.req("GET", adminPrefix+"/analytics/checks/same_run?installation_id="+other, nil, "", true, 200)
	if data(r)["run"].(map[string]any)["installation_id"] != other {
		t.Fatal(r)
	}
	f.req("POST", "/api/client/events/batch", gin.H{"schema_version": 1, "batch_id": "b", "installation_id": other, "events": []Event{e}}, key, false, 400)
	f.req("POST", "/api/client/events/batch", gin.H{"schema_version": 1, "batch_id": "b", "events": []Event{e, e}}, key, false, 400)
	bad := e
	bad.ID = "unknown_sensitive"
	var payload gin.H
	json.Unmarshal(bad.Data, &payload)
	payload["cookie"] = "TEST_ONLY_REJECT_ME"
	bad.Data, _ = json.Marshal(payload)
	r = f.events(key, bad)
	if len(r["rejected_events"].([]any)) != 1 {
		t.Fatal(r)
	}
	delete(payload, "cookie")
	payload["cleanup_pending"] = nil
	bad.ID = "null_pending"
	bad.Data, _ = json.Marshal(payload)
	r = f.events(key, bad)
	if len(r["rejected_events"].([]any)) != 1 {
		t.Fatal("null accepted", r)
	}
	future := e
	future.ID = "future"
	future.OccurredAt = f.now.Add(6 * time.Minute)
	r = f.events(key, future)
	if r["rejected_events"].([]any)[0].(map[string]any)["code"] != "CLOCK_SKEW" {
		t.Fatal(r)
	}
	_, r = f.req("GET", adminPrefix+"/analytics/events", nil, "", true, 200)
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "TEST_ONLY_REJECT_ME") || strings.Contains(string(raw), key) {
		t.Fatal("sensitive rejected data stored")
	}
	f.req("GET", adminPrefix+"/analytics/instances?from=2026-11-01T00:00:00Z", nil, "", true, 400)
	f.req("GET", adminPrefix+"/analytics/trends?metric=running_instances&granularity=hour", nil, "", true, 400)
}
func TestTerminalPendingAndOldFailureCannotUndoCompletion(t *testing.T) {
	f := setup(t)
	id, key := f.register("pending_a")
	e := finished("pending", "pending_run", "baidu", "daily", f.now, true, true)
	var payload gin.H
	json.Unmarshal(e.Data, &payload)
	payload["result"] = "failed"
	payload["cleanup_pending"] = true
	e.Data, _ = json.Marshal(payload)
	f.events(key, e)
	number(t, f.overview(), "successful_cycles", 0)
	f.events(key, event("real_cleanup", "account_check_cleanup_finished", f.now.Add(time.Minute), gin.H{"run_id": "pending_run", "result": "passed", "duration_ms": 1, "error_code": nil, "deletion_mode": "recycle_bin"}))
	number(t, f.overview(), "successful_cycles", 1)
	f.events(key, event("old_failure", "account_check_stage_finished", f.now.Add(-time.Hour), gin.H{"run_id": "pending_run", "stage": "verify_cleanup", "status": "failed", "duration_ms": 1, "error_code": "CLEANUP_UNCONFIRMED"}))
	number(t, f.overview(), "successful_cycles", 1)
	_, r := f.req("GET", adminPrefix+"/analytics/checks/pending_run?installation_id="+id, nil, "", true, 200)
	if data(r)["run"].(map[string]any)["result"] != "failed" {
		t.Fatal(r)
	}
}

func TestProxyTrustAndServiceFailure(t *testing.T) {
	f := setup(t)
	f.a.trustedProxies = []string{"172.18.0.2/32"}
	f.handler = f.a.router()
	f.a.loginAttempts["public:203.0.113.9"] = loginWindow{Start: f.now, Count: 600}
	for _, x := range []struct {
		remote string
		want   int
	}{{"172.18.0.2:12345", 429}, {"172.18.0.3:12345", 200}} {
		r := httptest.NewRequest("GET", "/api/search-sites", nil)
		r.RemoteAddr = x.remote
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != x.want {
			t.Fatalf("proxy %s status %d", x.remote, w.Code)
		}
	}
	f.a.store.db.Close()
	_, r := f.req("GET", "/api/search-sites", nil, "", false, 503)
	if r["code"] != "SERVICE_UNAVAILABLE" || r["data"] != nil {
		t.Fatal(r)
	}
}

func TestSimpleDashboardLoggedInPeopleAndProviderCounts(t *testing.T) {
	f := setup(t)
	a, key := f.register("person_a")
	b, _ := f.register("person_b")
	old := f.now.Add(-27 * time.Hour)
	err := f.a.store.db.Update(func(tx *bolt.Tx) error {
		accounts := []Account{
			{AccountSnapshot: AccountSnapshot{Ref: "a1", Provider: "baidu", AuthStatus: "valid", AuthCheckedAt: &f.now}, InstallationID: a},
			{AccountSnapshot: AccountSnapshot{Ref: "a2", Provider: "quark", AuthStatus: "valid", AuthCheckedAt: &f.now}, InstallationID: a},
			{AccountSnapshot: AccountSnapshot{Ref: "b1", Provider: "xunlei", AuthStatus: "valid", AuthCheckedAt: &old}, InstallationID: b},
			{AccountSnapshot: AccountSnapshot{Ref: "b2", Provider: "quark", AuthStatus: "expired", AuthCheckedAt: &f.now}, InstallationID: b},
			{AccountSnapshot: AccountSnapshot{Ref: "b3", Provider: "baidu", AuthStatus: "valid", AuthCheckedAt: &f.now}, InstallationID: b, Removed: true},
		}
		for _, x := range accounts {
			if err := put(tx, "accounts", x.InstallationID+"|"+x.Ref, x); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.events(key, finished("baidu_ok", "baidu_run", "baidu", "daily", f.now, true, true), finished("quark_fail", "quark_run", "quark", "daily", f.now, false, true), finished("manual_ok", "manual_run", "xunlei", "manual", f.now, true, true))
	o := f.overview()
	number(t, o, "registered_instances", 2)
	number(t, o, "logged_in_instances", 1)
	number(t, o, "valid_accounts", 2)
	number(t, o, "executed_runs", 2)
	number(t, o, "successful_cycles", 1)
	logged := o["valid_accounts_by_provider"].(map[string]any)
	number(t, logged, "baidu", 1)
	number(t, logged, "quark", 1)
	number(t, logged, "xunlei", 0)
	runs := o["executed_runs_by_provider"].(map[string]any)
	number(t, runs, "baidu", 1)
	number(t, runs, "quark", 1)
	number(t, runs, "xunlei", 0)
	_, r := f.req("GET", adminPrefix+"/analytics/overview?provider=quark", nil, "", true, 200)
	number(t, data(r), "logged_in_instances", 1)
	number(t, data(r), "valid_accounts", 1)
}
