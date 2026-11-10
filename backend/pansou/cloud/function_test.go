package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type functionFake struct {
	*serviceFakeProvider
	objects    map[string][]checkObject
	deletes    []checkObject
	deleteFail bool
	uncertain  bool
	rootDelay  int
	foreign    bool
}

func (f *functionFake) checkFiles(_ context.Context, _ Credentials, parent string) ([]checkObject, error) {
	if parent == "0" && len(f.objects[parent]) > 1 && f.rootDelay > 0 {
		f.rootDelay--
		return append([]checkObject(nil), f.objects[parent][:1]...), nil
	}
	return append([]checkObject(nil), f.objects[parent]...), nil
}
func (f *functionFake) CreateFolder(_ context.Context, _ Credentials, parent, name string) (Folder, error) {
	id := "test-dir"
	f.objects[parent] = append(f.objects[parent], checkObject{ID: id, Name: name, IsDir: true})
	return Folder{ID: id, Name: name}, nil
}
func (f *functionFake) Save(_ context.Context, _ Credentials, _ Share, e Entry, target string) error {
	f.objects[target] = append(f.objects[target], checkObject{ID: "saved-file", Name: e.Name, Size: e.Size})
	if f.foreign {
		f.objects[target] = append(f.objects[target], checkObject{ID: "foreign", Name: "用户文件.txt", Size: 6})
	}
	if f.uncertain {
		return errors.New("task timeout")
	}
	return nil
}
func (f *functionFake) deleteCheckObject(_ context.Context, _ Credentials, parent string, obj checkObject) error {
	if f.deleteFail {
		return errors.New("delete failed")
	}
	f.deletes = append(f.deletes, obj)
	kept := []checkObject{}
	for _, v := range f.objects[parent] {
		if v.ID != obj.ID {
			kept = append(kept, v)
		}
	}
	f.objects[parent] = kept
	return nil
}
func functionService(t *testing.T) (*Service, *functionFake, Account) {
	t.Helper()
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &functionFake{serviceFakeProvider: newServiceFakeProvider(), objects: map[string][]checkObject{"0": {{ID: "user-file", Name: "keep.txt", Size: 500}}}}
	s := &Service{store: st, ctx: ctx, cancel: cancel, providers: map[string]Provider{"quark": f}, wake: make(chan struct{}, 1), centerClient: centerHTTPClient()}
	t.Cleanup(func() { cancel(); st.db.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"provider": "quark", "source_id": "test-source", "source_version": "1", "share_url": "https://pan.quark.cn/s/test", "file_path": "01.mp4", "max_file_size_bytes": 1024}})
	}))
	t.Cleanup(server.Close)
	s.centerBase = server.URL
	a := serviceConnect(t, s, "owner", "private-cookie")
	return s, f, a
}
func executeTestFunction(t *testing.T, s *Service, a Account) functionRecord {
	t.Helper()
	queued, err := s.QueueFunctionCheck("owner", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := s.store.read("owner")
	s.executeFunction("owner", st.Checks[queued.ID])
	st, _ = s.store.read("owner")
	return st.Checks[queued.ID]
}
func TestFunctionCheckClosedLoop(t *testing.T) {
	s, f, a := functionService(t)
	r := executeTestFunction(t, s, a)
	if r.Status != "passed" || r.CleanupPending || len(f.deletes) != 2 {
		t.Fatalf("unexpected check %+v deletes=%v", r, f.deletes)
	}
	if len(f.objects["0"]) != 1 || f.objects["0"][0].ID != "user-file" {
		t.Fatal("unrelated file changed")
	}
	for _, stage := range r.Stages {
		if stage.Status != "passed" {
			t.Fatal(stage)
		}
	}
}
func TestFunctionCheckSourceWithoutConfiguredPath(t *testing.T) {
	for _, legacyPath := range []string{"", "不存在的文件.txt"} {
		t.Run(legacyPath, func(t *testing.T) {
			s, f, a := functionService(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				source := map[string]any{"provider": "quark", "source_id": "small-file", "source_version": "2", "share_url": "https://pan.quark.cn/s/test"}
				if legacyPath != "" {
					source["file_path"] = legacyPath
				}
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": source})
			}))
			defer server.Close()
			s.centerBase = server.URL
			f.share.Entries = []Entry{serviceFile("small", "汁源分享.jpg")}
			preview, err := s.FunctionSourcePreview(context.Background(), "owner", a.ID)
			if err != nil || preview["can_check"] != true || preview["selected_path"] != "汁源分享.jpg" {
				t.Fatalf("preview: %v %v", preview, err)
			}
			r := executeTestFunction(t, s, a)
			if r.Status != "passed" || r.CleanupPending || len(f.deletes) != 2 {
				t.Fatalf("check failed: %+v", r)
			}
			if len(f.objects["0"]) != 1 || f.objects["0"][0].ID != "user-file" {
				t.Fatal("unrelated file changed")
			}
		})
	}
}

func TestFunctionCheckSelectsOneSmallFile(t *testing.T) {
	s, f, a := functionService(t)
	large := serviceFile("large", "大文件.bin")
	large.Size = 2048
	small := serviceFile("small", "测试.txt")
	small.Path = "子目录/测试.txt"
	small.Size = 8
	f.share.Entries = []Entry{large, serviceFile("other", "其他.txt"), small}
	r := executeTestFunction(t, s, a)
	if r.Status != "passed" || len(f.deletes) != 2 || f.deletes[0].Name != small.Name {
		t.Fatalf("incorrect selection: %+v %v", r, f.deletes)
	}
}

func TestFunctionCheckOversizedSourceDoesNotWrite(t *testing.T) {
	s, f, a := functionService(t)
	f.share.Entries[0].Size = 2048
	r := executeTestFunction(t, s, a)
	if r.Status != "failed" || r.ErrorCode != "TEST_FILE_TOO_LARGE" || r.CleanupPending || len(f.deletes) != 0 || len(f.objects["0"]) != 1 {
		t.Fatalf("unexpected write or outcome: %+v", r)
	}
}

func TestFunctionCheckFailureSafety(t *testing.T) {
	for _, scenario := range []string{"uncertain", "foreign", "missing", "delete"} {
		t.Run(scenario, func(t *testing.T) {
			s, f, a := functionService(t)
			switch scenario {
			case "uncertain":
				f.uncertain = true
			case "foreign":
				f.foreign = true
			case "missing":
				f.share.Entries = nil
			case "delete":
				f.deleteFail = true
			}
			r := executeTestFunction(t, s, a)
			if r.Status == "passed" {
				t.Fatal("false success")
			}
			if len(f.deletes) != 0 {
				t.Fatal("deleted unconfirmed content")
			}
			if scenario == "missing" {
				if r.ErrorCode != "TEST_FILE_NOT_FOUND" {
					t.Fatal("missing source misclassified", r.ErrorCode)
				}
				if r.CleanupPending || len(f.objects["0"]) != 1 {
					t.Fatal("wrote on missing test file")
				}
				return
			}
			if !r.CleanupPending {
				t.Fatal("lost pending cleanup")
			}
			if _, err := s.QueueFunctionCheck("owner", a.ID); err == nil {
				t.Fatal("duplicate test allowed over uncertain write")
			}
			if _, err := s.Connect(context.Background(), "owner", a.ID, "quark", "", Credentials{"cookie": "new-cookie"}); err != nil {
				t.Fatalf("reconnect blocked: %v", err)
			}
			if err := s.DeleteAccount("owner", a.ID); err == nil {
				t.Fatal("allowed deletion of cleanup credentials")
			}
			if scenario == "delete" {
				f.deleteFail = false
				if err := s.QueueFunctionCleanup("owner", r.ID); err != nil {
					t.Fatal(err)
				}
				st, _ := s.store.read("owner")
				s.executeCleanup("owner", st.Checks[r.ID])
				st, _ = s.store.read("owner")
				if st.Checks[r.ID].CleanupPending || st.Checks[r.ID].Status != "incomplete" {
					t.Fatal("cleanup must retain original outcome")
				}
			}
		})
	}
}
func TestFunctionScheduleAtOneSecondAfterMidnight(t *testing.T) {
	s, _, a := functionService(t)
	slot := time.Date(2026, 11, 5, 0, 0, 0, 0, shanghai)
	if err := s.store.update("owner", func(st *userState) error {
		ac := st.Accounts[a.ID]
		ac.CreatedAt = slot.Add(-48 * time.Hour).UnixMilli()
		st.Accounts[a.ID] = ac
		st.Center.Config = CheckConfig{EnabledSince: slot.Add(-24 * time.Hour).UnixMilli()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Retain the owner's opt-out even after the execution time has arrived.
	s.scheduleFunctions(slot.Add(time.Second))
	st, err := s.store.read("owner")
	if err != nil || len(st.Checks) != 0 || len(st.Center.LastSlots) != 0 {
		t.Fatalf("disabled schedule created a run: %+v, %v", st.Checks, err)
	}
	if err := s.store.update("owner", func(st *userState) error {
		st.Center.Config.DailyEnabled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{slot, slot.Add(time.Second - time.Nanosecond)} {
		s.scheduleFunctions(at)
		st, err = s.store.read("owner")
		if err != nil || len(st.Checks) != 0 || len(st.Center.LastSlots) != 0 {
			t.Fatalf("scheduled before 00:00:01 at %s: %+v, %v", at, st.Checks, err)
		}
	}
	// Use UTC here to verify the schedule is based on Beijing time, not host time.
	s.scheduleFunctions(slot.Add(time.Second).UTC())
	s.scheduleFunctions(slot.Add(2 * time.Second))
	st, err = s.store.read("owner")
	if err != nil || len(st.Checks) != 1 || st.Center.LastSlots[a.ID] != slot.UnixMilli() {
		t.Fatalf("unexpected daily slot: %+v, %v", st.Center.LastSlots, err)
	}
	for _, r := range st.Checks {
		if r.Trigger != "daily" || r.ScheduledFor != slot.UnixMilli() {
			t.Fatalf("changed midnight reporting identity: %+v", r)
		}
	}
}

func TestNextFunctionCheckAt(t *testing.T) {
	for _, tc := range []struct{ name, at, want string }{
		{"before midnight", "2026-11-04T23:59:59+08:00", "2026-11-05T00:00:01+08:00"},
		{"at midnight", "2026-11-05T00:00:00+08:00", "2026-11-05T00:00:01+08:00"},
		{"just before due", "2026-11-05T00:00:00.999999999+08:00", "2026-11-05T00:00:01+08:00"},
		{"already due", "2026-11-05T00:00:01+08:00", "2026-11-06T00:00:01+08:00"},
		{"UTC input", "2026-11-04T16:00:00Z", "2026-11-05T00:00:01+08:00"},
		{"new year", "2026-12-31T23:59:59+08:00", "2027-01-01T00:00:01+08:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339Nano, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339Nano, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if got := nextFunctionCheckAt(at); !got.Equal(want) {
				t.Fatalf("got %s, want %s", got, want)
			}
		})
	}
}

func TestHeartbeatReportsFunctionExecutionTime(t *testing.T) {
	s, _, _ := functionService(t)
	st, err := s.store.read("owner")
	if err != nil {
		t.Fatal(err)
	}
	st.Center.Config.DailyEnabled = true
	accounts := heartbeat(st)["accounts"].([]map[string]any)
	next, err := time.Parse(time.RFC3339Nano, accounts[0]["next_check_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	next = next.In(shanghai)
	if next.Hour() != 0 || next.Minute() != 0 || next.Second() != 1 || next.Nanosecond() != 0 {
		t.Fatalf("heartbeat did not report 00:00:01: %s", next)
	}
	st.Center.Config.DailyEnabled = false
	accounts = heartbeat(st)["accounts"].([]map[string]any)
	if accounts[0]["next_check_at"] != nil || accounts[0]["check_enabled"] != false {
		t.Fatalf("disabled schedule announced another run: %+v", accounts[0])
	}
}

func TestFunctionScheduleAndRecovery(t *testing.T) {
	s, _, a := functionService(t)
	at := time.Date(2026, 11, 5, 0, 0, 2, 0, shanghai)
	s.store.update("owner", func(st *userState) error {
		ac := st.Accounts[a.ID]
		ac.CreatedAt = at.Add(-48 * time.Hour).UnixMilli()
		st.Accounts[a.ID] = ac
		st.Center.Config = CheckConfig{DailyEnabled: true, EnabledSince: at.Add(-24 * time.Hour).UnixMilli()}
		return nil
	})
	s.scheduleFunctions(at)
	s.scheduleFunctions(at)
	st, _ := s.store.read("owner")
	if len(st.Checks) != 1 {
		t.Fatal("slot not idempotent")
	}
	var id string
	for _, r := range st.Checks {
		id = r.ID
		if r.Trigger != "daily" || r.ScheduledFor != midnight(at).UnixMilli() {
			t.Fatal(r)
		}
	}
	s.store.update("owner", func(st *userState) error {
		r := st.Checks[id]
		r.Status = "running"
		r.StartedAt = now()
		r.Submitted = true
		r.CleanupPending = true
		st.Checks[id] = r
		recoverFunctions(st)
		return nil
	})
	st, _ = s.store.read("owner")
	if st.Checks[id].Status != "incomplete" || !st.Checks[id].CleanupPending {
		t.Fatal("lost interrupted write")
	}
	s.scheduleFunctions(at.Add(72 * time.Hour))
	st, _ = s.store.read("owner")
	if len(st.Checks) != 1 {
		t.Fatal("replayed uncertain write")
	}
	s.store.update("owner", func(st *userState) error { r := st.Checks[id]; r.CleanupPending = false; st.Checks[id] = r; return nil })
	s.scheduleFunctions(at.Add(72*time.Hour + time.Hour))
	st, _ = s.store.read("owner")
	if len(st.Checks) != 2 {
		t.Fatal("catch-up should create only latest slot")
	}
	for _, r := range st.Checks {
		if r.ID != id && (r.Trigger != "catch_up" || r.ScheduledFor != midnight(at.Add(72*time.Hour)).UnixMilli()) {
			t.Fatal(r)
		}
	}
}
func TestCenterReportingAndPartialAck(t *testing.T) {
	s, _, a := functionService(t)
	registrations, heartbeats, batches := 0, 0, 0
	var instance, nonce string
	var previousSeq uint64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if r.Method == "POST" {
			json.NewDecoder(r.Body).Decode(&req)
		}
		data := any(map[string]any{})
		switch r.URL.Path {
		case "/api/client/register":
			registrations++
			instance, _ = req["client_instance_id"].(string)
			nonce, _ = req["registration_nonce"].(string)
			if len(nonce) != 64 || instance == "" {
				t.Error("missing persistent random identity")
			}
			data = map[string]any{"installation_id": "installation", "report_token": "test-report-secret"}
		case "/api/client/config":
			data = map[string]any{"accepted_schema_versions": []int{1}, "heartbeat_interval_seconds": 600, "event_batch_max_items": 100, "event_batch_max_bytes": 262144}
		case "/api/search-sites":
			data = []CenterSite{{ID: "ikantv", Name: "test", Enabled: true}}
		case "/api/client/heartbeat":
			heartbeats++
			if r.Header.Get("Authorization") != "Bearer test-report-secret" {
				t.Error("bad auth")
			}
			seq := uint64(req["snapshot_seq"].(float64))
			if seq <= previousSeq {
				t.Error("nonmonotonic seq")
			}
			previousSeq = seq
			data = map[string]any{"accepted_seq": seq, "applied": true}
		case "/api/client/events/batch":
			batches++
			encoded, _ := json.Marshal(req)
			if strings.Contains(string(encoded), "private-cookie") || strings.Contains(string(encoded), "01.mp4") || strings.Contains(string(encoded), "pan.quark.cn") {
				t.Error("private data leaked")
			}
			events := req["events"].([]any)
			id := events[0].(map[string]any)["event_id"].(string)
			if batches == 1 {
				data = map[string]any{"accepted_event_ids": []string{}, "duplicate_event_ids": []string{id}, "rejected_events": []any{}}
			} else {
				data = map[string]any{"rejected_events": []any{map[string]any{"event_id": id, "code": "INVALID_FIELD", "field": "data.result", "retryable": false}}}
			}
		default:
			t.Error(r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	// Execute a real mocked check first, then report its actual result into this isolated server.
	enabled := true
	s.SetCenterConfig("owner", nil, &enabled)
	r := executeTestFunction(t, s, a)
	s.centerBase = server.URL
	s.store.update("owner", func(st *userState) error { st.Center.Events = st.Center.Events[len(st.Center.Events)-1:]; return nil })
	s.syncCenter("owner")
	st, _ := s.store.read("owner")
	if registrations != 1 || heartbeats != 1 || batches != 1 || len(st.Center.Events) != 0 || st.Center.LastError != "" {
		t.Fatalf("report failed %+v", st.Center)
	}
	if strings.Contains(st.Center.Secret, "test-report-secret") || strings.Contains(st.Center.Secret, nonce) {
		t.Fatal("secret not encrypted")
	}
	s.store.update("owner", func(st *userState) error {
		finishFunction(st, r)
		st.Center.LastAttemptAt = 0
		st.Center.LastReportAt = 0
		return nil
	})
	s.syncCenter("owner")
	st, _ = s.store.read("owner")
	if registrations != 1 || heartbeats != 2 || len(st.Center.Events) != 0 || st.Center.RejectedEvents != 1 {
		t.Fatalf("bad rejection/retry %+v", st.Center)
	}
	s.SetCenterConfig("owner", nil, new(bool))
	s.syncCenter("owner")
	if heartbeats != 2 {
		t.Fatal("reported when disabled")
	}
}

func TestFunctionValidationNetworkErrorIsUnknown(t *testing.T) {
	s, f, a := functionService(t)
	f.validateErr = errors.New("network timeout")
	r := executeTestFunction(t, s, a)
	st, _ := s.store.read("owner")
	if r.AuthStatus != "unknown" || checkAuth(st.Accounts[a.ID].Account) != "unknown" {
		t.Fatal("network failure reported as valid auth")
	}
	if st.Accounts[a.ID].Status == "expired" {
		t.Fatal("network failure expired credentials")
	}
}
func TestFunctionDoesNotCountExternalDeletion(t *testing.T) {
	s, f, a := functionService(t)
	f.deleteFail = true
	r := executeTestFunction(t, s, a)
	// Failed delete had an intent, but an externally missing file before any delete
	// was submitted must not count as tested deletion capability.
	r.DeleteSubmitted = false
	f.objects[r.TargetID] = nil
	f.deleteFail = false
	if err := s.deleteFunctionObjects(context.Background(), "owner", Credentials{}, f, f, &r); err == nil {
		t.Fatal("external deletion counted as function check")
	}
}

func TestUnchangedStateDoesNotCommit(t *testing.T) {
	s, _, _ := functionService(t)
	s.store.update("owner", func(st *userState) error {
		for i := 0; i < 30; i++ {
			id := fmt.Sprint(i)
			st.Jobs[id] = jobRecord{Job: Job{ID: id, Status: "succeeded"}}
		}
		return nil
	})
	if err := s.store.update("owner", func(st *userState) error { return nil }); err != nil {
		t.Fatal(err)
	}
	beforeStats := s.store.db.Stats()
	before := beforeStats.TxStats.GetWrite()
	for i := 0; i < 5; i++ {
		if err := s.store.update("owner", func(st *userState) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	afterStats := s.store.db.Stats()
	if got := afterStats.TxStats.GetWrite(); got != before {
		t.Fatal("no-op updates caused disk writes", before, got)
	}
}

func TestFunctionCheckWaitsForCreatedFolder(t *testing.T) {
	s, f, a := functionService(t)
	f.rootDelay = 2
	r := executeTestFunction(t, s, a)
	if r.Status != "passed" || r.CleanupPending || len(f.deletes) != 2 {
		t.Fatalf("delayed directory failed: %+v", r)
	}
}
func TestFunctionCheckRecoversNativeCreateReceipt(t *testing.T) {
	s, f, a := functionService(t)
	q, err := s.QueueFunctionCheck("owner", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	name := checkFolderPrefix + strings.ToLower(q.ID)
	f.objects["0"] = append(f.objects["0"], checkObject{ID: "created-folder", Name: name, IsDir: true})
	s.store.update("owner", func(st *userState) error {
		r := st.Checks[q.ID]
		r.Status = "incomplete"
		r.CleanupPending = true
		r.TargetID = "created-folder"
		r.TargetName = name
		st.Checks[q.ID] = r
		return nil
	})
	if err := s.QueueFunctionCleanup("owner", q.ID); err != nil {
		t.Fatal(err)
	}
	st, _ := s.store.read("owner")
	s.executeCleanup("owner", st.Checks[q.ID])
	st, _ = s.store.read("owner")
	if st.Checks[q.ID].CleanupPending || st.Checks[q.ID].Status != "incomplete" || len(f.deletes) != 1 || len(f.objects["0"]) != 1 {
		t.Fatal("receipt cleanup failed or altered result")
	}
}
