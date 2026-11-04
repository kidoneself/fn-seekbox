package cloud

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pansou/util/json"
)

func qrFixture(t *testing.T, provider *serviceFakeProvider) *Service {
	t.Helper()
	s := serviceStart(t, provider)
	s.qrCommand = func(ctx context.Context, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' "$1"`, "fixture", `{"status":"ready","credentials":{"cookie":"qr-private-test-cookie"}}`)
	}
	return s
}
func qrReady(t *testing.T, s *Service, owner, accountID string) QRState {
	t.Helper()
	state, err := s.StartQR(owner, "quark", accountID, "扫码账号")
	if err != nil {
		t.Fatal(err)
	}
	limit := time.Now().Add(5 * time.Second)
	for time.Now().Before(limit) {
		state, err = s.QRStatus(owner, state.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "ready" {
			return state
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("login process did not report ready")
	return QRState{}
}
func TestQRIsolationValidationAndIdempotency(t *testing.T) {
	p := newServiceFakeProvider()
	var validations atomic.Int64
	p.validateHook = func(_ context.Context, c Credentials) (Identity, error) {
		validations.Add(1)
		if c["cookie"] != "qr-private-test-cookie" {
			return Identity{}, errors.New("wrong credential")
		}
		return Identity{ID: "owner-cloud-identity", Name: "真实校验名"}, nil
	}
	s := qrFixture(t, p)
	state := qrReady(t, s, "alice", "")
	for _, action := range []func() error{
		func() error { _, e := s.QRStatus("bob", state.ID); return e },
		func() error { _, e := s.CompleteQR(context.Background(), "bob", state.ID); return e },
		func() error { return s.CancelQR("bob", state.ID) },
	} {
		if !errors.Is(action(), ErrNotFound) {
			t.Fatal("another user accessed QR login")
		}
	}
	before, _ := s.Accounts("alice")
	if len(before) != 0 {
		t.Fatal("reading QR status persisted an account")
	}
	first, err := s.CompleteQR(context.Background(), "alice", state.ID)
	if err != nil || first.Status != "connected" {
		t.Fatalf("completion: %v %#v", err, first)
	}
	second, err := s.CompleteQR(context.Background(), "alice", state.ID)
	if err != nil || second.Account.ID != first.Account.ID || validations.Load() != 1 {
		t.Fatal("completion duplicated validation/account")
	}
	raw, _ := json.Marshal(second)
	if strings.Contains(string(raw), "qr-private-test-cookie") || strings.Contains(string(raw), "credentials") {
		t.Fatal("credentials exposed")
	}
	bytes, err := os.ReadFile(s.store.db.Path())
	if err != nil || strings.Contains(string(bytes), "qr-private-test-cookie") {
		t.Fatal("credential was not encrypted")
	}
}
func TestQRCancelAndExpiryCannotSave(t *testing.T) {
	s := qrFixture(t, newServiceFakeProvider())
	state := qrReady(t, s, "alice", "")
	if err := s.CancelQR("alice", state.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteQR(context.Background(), "alice", state.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cancelled login was completed")
	}
	state = qrReady(t, s, "alice", "")
	q, _ := s.ownedQR("alice", state.ID)
	q.mu.Lock()
	q.state.ExpiresAt = now() - 1
	q.mu.Unlock()
	state, err := s.QRStatus("alice", state.ID)
	if err != nil || state.Status != "expired" {
		t.Fatal("login did not expire")
	}
	if _, err = s.CompleteQR(context.Background(), "alice", state.ID); err == nil {
		t.Fatal("expired credential saved")
	}
	accounts, _ := s.Accounts("alice")
	if len(accounts) != 0 {
		t.Fatal("unexpected account")
	}
}
func TestQRReconnectRejectsDifferentCloudIdentity(t *testing.T) {
	p := newServiceFakeProvider()
	p.identityID = "original"
	s := qrFixture(t, p)
	account, err := s.Connect(context.Background(), "alice", "", "quark", "original", Credentials{"cookie": "original-private-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.identityID = "different"
	p.mu.Unlock()
	state := qrReady(t, s, "alice", account.ID)
	state, err = s.CompleteQR(context.Background(), "alice", state.ID)
	if err != nil || state.Status != "failed" {
		t.Fatal("reconnection accepted another identity")
	}
	stored, _ := s.store.read("alice")
	credential, err := s.store.decrypt("alice", stored.Accounts[account.ID])
	if err != nil || credential["cookie"] != "original-private-cookie" {
		t.Fatal("old credential was replaced")
	}
}
func TestQRLimitAndShutdownStopPendingProcesses(t *testing.T) {
	s := qrFixture(t, newServiceFakeProvider())
	s.qrCommand = func(ctx context.Context, _ string) *exec.Cmd { return exec.CommandContext(ctx, "sh", "-c", "sleep 60") }
	for i := 0; i < 3; i++ {
		if _, err := s.StartQR("alice", "quark", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.StartQR("alice", "quark", "", ""); err == nil {
		t.Fatal("owner quota bypassed")
	}
	s.qrMu.Lock()
	for _, q := range s.qrSessions {
		q.cancel()
	}
	s.qrMu.Unlock()
	// Service cleanup waits for all children; cancellation must end the group.
}
