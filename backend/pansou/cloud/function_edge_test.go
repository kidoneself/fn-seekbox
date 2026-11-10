package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFunctionAuthCheckedEvenWhenSourceUnavailable(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "expired"}[expired], func(t *testing.T) {
			s, f, a := functionService(t)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(503)
				json.NewEncoder(w).Encode(map[string]any{"code": "SOURCE_UNAVAILABLE"})
			}))
			defer server.Close()
			s.centerBase = server.URL
			validated := 0
			f.validateHook = func(context.Context, Credentials) (Identity, error) {
				validated++
				if expired {
					return Identity{}, ErrAuthExpired
				}
				return Identity{}, nil
			}
			r := executeTestFunction(t, s, a)
			st, _ := s.store.read("owner")
			if validated != 1 {
				t.Fatalf("source outage prevented account validation: calls=%d", validated)
			}
			if len(f.objects["0"]) != 1 || len(f.deletes) != 0 || r.Status == "passed" {
				t.Fatal("failure wrote to drive or passed")
			}
			if expired {
				if requests != 0 || r.ErrorCode != "AUTH_EXPIRED" || st.Accounts[a.ID].Status != "expired" {
					t.Fatalf("expired account not stopped before source request: requests=%d record=%+v", requests, r)
				}
			} else if r.AuthStatus != "valid" || requests != 1 {
				t.Fatalf("valid auth lost on source outage: %+v", r)
			}
		})
	}
}
func TestFunctionIdentityMismatchBlocksFutureTransfers(t *testing.T) {
	s, f, a := functionService(t)
	s.store.update("owner", func(st *userState) error {
		v := st.Accounts[a.ID]
		v.ProviderUserID = "original"
		st.Accounts[a.ID] = v
		return nil
	})
	f.identityID = "different"
	r := executeTestFunction(t, s, a)
	st, _ := s.store.read("owner")
	if st.Accounts[a.ID].Status == "ready" || r.ErrorCode != "AUTH_IDENTITY_MISMATCH" || len(f.objects["0"]) != 1 {
		t.Fatalf("identity mismatch still usable: account=%+v check=%+v", st.Accounts[a.ID].Account, r)
	}
	if _, err := s.Transfer("owner", serviceRequest(a, "identity-changed")); err == nil {
		t.Fatal("mismatched identity accepted transfer")
	}
}
func TestFunctionCleanupRevalidatesAccount(t *testing.T) {
	for _, kind := range []string{"expired", "identity", "network"} {
		t.Run(kind, func(t *testing.T) {
			s, f, a := functionService(t)
			f.deleteFail = true
			r := executeTestFunction(t, s, a)
			f.deleteFail = false
			if kind == "expired" {
				f.validateErr = ErrAuthExpired
			} else if kind == "network" {
				f.validateErr = errors.New("network timeout")
			} else {
				s.store.update("owner", func(st *userState) error {
					v := st.Accounts[a.ID]
					v.ProviderUserID = "original"
					st.Accounts[a.ID] = v
					return nil
				})
				f.identityID = "other"
			}
			if err := s.QueueFunctionCleanup("owner", r.ID); err != nil {
				t.Fatal(err)
			}
			st, _ := s.store.read("owner")
			s.executeCleanup("owner", st.Checks[r.ID])
			st, _ = s.store.read("owner")
			if !st.Checks[r.ID].CleanupPending || len(f.deletes) != 0 {
				t.Fatalf("cleanup deleted without validating %s account", kind)
			}
			if kind == "expired" && st.Accounts[a.ID].Status != "expired" {
				t.Fatal("expired cleanup auth not recorded")
			}
			if kind == "network" && st.Accounts[a.ID].Status == "expired" {
				t.Fatal("network error marked expired")
			}
		})
	}
}
func TestFunctionNoAccountsAndDailyDeduplication(t *testing.T) {
	s, f, a := functionService(t)
	at := time.Now().In(shanghai).AddDate(0, 0, 1)
	if _, err := s.SetCenterConfig("owner", newTrue(), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.store.update("owner", func(st *userState) error {
		st.Center.LastSlots[a.ID] = midnight(at).UnixMilli()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount("owner", a.ID); err != nil {
		t.Fatal(err)
	}
	s.scheduleFunctions(at)
	st, _ := s.store.read("owner")
	if len(st.Checks) != 0 || len(st.Center.LastSlots) != 0 || len(heartbeat(st)["accounts"].([]map[string]any)) != 0 || len(f.objects["0"]) != 1 {
		t.Fatal("no-account instance scheduled drive work")
	}
	if _, err := s.QueueFunctionCheck("owner", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted account: %v", err)
	}
	a = serviceConnect(t, s, "owner", "new-cookie")
	s.scheduleFunctions(at)
	s.scheduleFunctions(at)
	st, _ = s.store.read("owner")
	if len(st.Checks) != 1 {
		t.Fatal("daily task duplicated", len(st.Checks))
	}
	f.validateErr = ErrAuthExpired
	for _, r := range st.Checks {
		s.executeFunction("owner", r)
	}
	s.scheduleFunctions(at)
	st, _ = s.store.read("owner")
	if len(st.Checks) != 1 || st.Accounts[a.ID].Status != "expired" {
		t.Fatal("expired check was retried within same daily slot")
	}
	for _, r := range st.Checks {
		if r.Status == "passed" || r.CleanupPending {
			t.Fatal("expired account wrote or counted success")
		}
	}

}

func newTrue() *bool { v := true; return &v }

func TestFunctionQueuedStagesUpgrade(t *testing.T) {
	s, f, a := functionService(t)
	q, err := s.QueueFunctionCheck("owner", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := s.store.read("owner")
	r := st.Checks[q.ID]
	// A queued record persisted by the prior version fetched the source first.
	r.Stages[0].Stage, r.Stages[1].Stage = "fetch_source", "validate_account"
	if err := s.saveFunction("owner", r); err != nil {
		t.Fatal(err)
	}
	f.validateErr = ErrAuthExpired
	s.executeFunction("owner", r)
	st, _ = s.store.read("owner")
	r = st.Checks[q.ID]
	for _, stage := range r.Stages {
		if stage.Stage == "validate_account" && stage.Status != "failed" {
			t.Fatal("wrong failed stage", r.Stages)
		}
		if stage.Stage == "fetch_source" && stage.Status != "skipped" {
			t.Fatal("source requested for expired account", r.Stages)
		}
	}
}
func TestFunctionFailedDeleteDoesNotCountExternalCleanup(t *testing.T) {
	for _, missing := range []string{"file", "folder"} {
		t.Run(missing, func(t *testing.T) {
			s, f, a := functionService(t)
			f.deleteFail = true
			r := executeTestFunction(t, s, a)
			if !r.DeleteSubmitted || !r.CleanupPending {
				t.Fatal("expected failed delete intent")
			}
			// The provider rejected deletion; another client then removed the object.
			f.objects[r.TargetID] = nil
			if missing == "folder" {
				f.objects["0"] = f.objects["0"][:1]
			}
			f.deleteFail = false
			if err := s.QueueFunctionCleanup("owner", r.ID); err != nil {
				t.Fatal(err)
			}
			st, _ := s.store.read("owner")
			s.executeCleanup("owner", st.Checks[r.ID])
			st, _ = s.store.read("owner")
			if !st.Checks[r.ID].CleanupPending || st.Checks[r.ID].CleanupStatus == "passed" {
				t.Fatal("unconfirmed delete counted as success")
			}
			if len(f.deletes) != 0 {
				t.Fatal("cleanup continued after missing deletion receipt")
			}
		})
	}
}
func TestFunctionCleanupResumesAfterConfirmedFileDelete(t *testing.T) {
	s, f, a := functionService(t)
	f.deleteDirectoryFail = true
	r := executeTestFunction(t, s, a)
	if !r.CleanupPending || len(f.deletes) != 1 {
		t.Fatal("expected file deleted and folder pending")
	}
	f.deleteDirectoryFail = false
	if err := s.QueueFunctionCleanup("owner", r.ID); err != nil {
		t.Fatal(err)
	}
	st, _ := s.store.read("owner")
	s.executeCleanup("owner", st.Checks[r.ID])
	st, _ = s.store.read("owner")
	if st.Checks[r.ID].CleanupPending || st.Checks[r.ID].CleanupStatus != "passed" || len(f.deletes) != 2 {
		t.Fatal("confirmed file deletion did not allow folder cleanup")
	}
}
func TestFunctionSourceMetadataMustBeReportable(t *testing.T) {
	for _, fixture := range []struct{ name, id, version string }{
		{"invalid-id", "invalid source id", "1"},
		{"long-id", strings.Repeat("x", 65), "1"},
		{"long-version", "source", strings.Repeat("1", 33)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s, f, a := functionService(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
					"provider": "quark", "share_url": "https://pan.quark.cn/s/test", "source_id": fixture.id, "source_version": fixture.version,
				}})
			}))
			defer server.Close()
			s.centerBase = server.URL
			r := executeTestFunction(t, s, a)
			if r.Status == "passed" || len(f.objects["0"]) != 1 || len(f.deletes) != 0 {
				t.Fatal("unreportable source used for remote writes")
			}
		})
	}
}
func TestQueuedTransferDoesNotReclassifyAccountErrorAsExpired(t *testing.T) {
	s, f, a := functionService(t)
	job, err := s.Transfer("owner", serviceRequest(a, "queued-account-error"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.update("owner", func(st *userState) error {
		account := st.Accounts[a.ID]
		account.Status, account.LastError, account.AuthCheckedAt = "error", "账号身份不匹配", 0
		st.Accounts[a.ID] = account
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, _ := s.store.read("owner")
	s.execute("owner", st.Jobs[job.ID])
	st, _ = s.store.read("owner")
	if st.Accounts[a.ID].Status != "error" || st.Accounts[a.ID].LastError != "账号身份不匹配" || st.Jobs[job.ID].Status != "failed" || f.reads != 0 {
		t.Fatal("queued transfer misclassified account error or touched share")
	}
}
