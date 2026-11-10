package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type remoteQRProvider struct{ *serviceFakeProvider }

func (*remoteQRProvider) ID() string { return "xunlei" }

func TestRemoteQRNASValidatesAndPersistsBeforeAcknowledging(t *testing.T) {
	t.Setenv("SEEKBOX_REMOTE_XUNLEI", "1")
	var mu sync.Mutex
	accountRef := ""
	var claims, acks, cancels atomic.Int32
	var service *Service
	rawSession := `{"access_token":"REMOTE_TEST_ACCESS","refresh_token":"REMOTE_TEST_REFRESH","headers":{"x-device-id":"test-device","x-client-id":"test-client","x-captcha-token":"test-captcha"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer report-test-token" {
			t.Error("missing instance token")
		}
		var response any
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/client/scan/sessions":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			mu.Lock()
			accountRef = in["account_id"]
			mu.Unlock()
			response = map[string]any{"id": "00000000-0000-0000-0000-000000000001", "provider": "xunlei", "account_id": in["account_id"], "status": "ready", "expires_at": now() + 60000}
		case strings.HasSuffix(r.URL.Path, "/claim"):
			claims.Add(1)
			mu.Lock()
			ref := accountRef
			mu.Unlock()
			response = map[string]any{"provider": "xunlei", "account_id": ref, "credentials": Credentials{"web_session": rawSession}}
		case strings.HasSuffix(r.URL.Path, "/ack"):
			accounts, err := service.Accounts("alice")
			if err != nil || len(accounts) != 1 {
				t.Error("acknowledged before local persistence")
			}
			acks.Add(1)
			response = map[string]bool{"acknowledged": true}
		case r.Method == "DELETE":
			cancels.Add(1)
			response = map[string]bool{"cancelled": true}
		default:
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": response})
	}))
	defer server.Close()
	t.Setenv("SEEKBOX_CENTER_URL", server.URL)
	provider := &remoteQRProvider{newServiceFakeProvider()}
	provider.validateHook = func(_ context.Context, c Credentials) (Identity, error) {
		if c["web_session"] != rawSession {
			return Identity{}, errors.New("wrong session")
		}
		return Identity{ID: "remote-account", Name: "测试账号"}, nil
	}
	var err error
	service, err = NewService(t.TempDir(), []Provider{provider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err = service.saveCenterSecret("alice", Credentials{"report_token": "report-test-token"}); err != nil {
		t.Fatal(err)
	}
	state, err := service.StartQR("alice", "xunlei", "", "远端扫码")
	if err != nil {
		t.Fatal(err)
	}
	for limit := time.Now().Add(3 * time.Second); time.Now().Before(limit); {
		state, err = service.QRStatus("alice", state.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state.Status != "ready" || claims.Load() != 1 {
		t.Fatalf("remote not ready: %#v", state)
	}
	payload, _ := json.Marshal(state)
	if strings.Contains(string(payload), "REMOTE_TEST") {
		t.Fatal("credentials exposed to browser")
	}
	if _, err = service.QRStatus("bob", state.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross user access")
	}
	state, err = service.CompleteQR(context.Background(), "alice", state.ID)
	if err != nil || state.Status != "connected" || acks.Load() != 1 {
		t.Fatalf("complete: %v %#v", err, state)
	}
	_, _ = service.CompleteQR(context.Background(), "alice", state.ID)
	if acks.Load() != 1 {
		t.Fatal("duplicate ack")
	}
	raw, _ := os.ReadFile(service.store.db.Path())
	if strings.Contains(string(raw), "REMOTE_TEST") {
		t.Fatal("plaintext credentials on disk")
	}
	for limit := time.Now().Add(time.Second); time.Now().Before(limit) && cancels.Load() == 0; {
		time.Sleep(time.Millisecond)
	}
	if cancels.Load() != 1 {
		t.Fatal("remote temporary session not released")
	}
}

func TestRemoteQRCancelQueuedSessionWithoutSaving(t *testing.T) {
	t.Setenv("SEEKBOX_REMOTE_XUNLEI", "1")
	cancelled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			cancelled <- struct{}{}
			_, _ = w.Write([]byte(`{"code":0,"data":{"cancelled":true}}`))
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"id": "00000000-0000-0000-0000-000000000002", "provider": "xunlei", "account_id": in["account_id"], "status": "queued", "queue_position": 2, "expires_at": now() + 60000}})
	}))
	defer server.Close()
	t.Setenv("SEEKBOX_CENTER_URL", server.URL)
	s, err := NewService(t.TempDir(), []Provider{&remoteQRProvider{newServiceFakeProvider()}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.saveCenterSecret("alice", Credentials{"report_token": "report-test-token"}); err != nil {
		t.Fatal(err)
	}
	q, err := s.StartQR("alice", "xunlei", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(2 * time.Second); time.Now().Before(until); {
		q, err = s.QRStatus("alice", q.ID)
		if err != nil {
			t.Fatal(err)
		}
		if q.Status == "queued" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if q.Status != "queued" {
		t.Fatal(q.Status)
	}
	if err = s.CancelQR("alice", q.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not reach scanner")
	}
	accounts, _ := s.Accounts("alice")
	if len(accounts) != 0 {
		t.Fatal("cancellation saved account")
	}
}
