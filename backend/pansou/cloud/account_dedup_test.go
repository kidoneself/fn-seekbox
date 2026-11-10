package cloud

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestRepeatedLoginKeepsAccountSubscriptionAndTransferJournal(t *testing.T) {
	p := newServiceFakeProvider()
	p.identityID = "stable-cloud-user"
	s := serviceStart(t, p)
	original, err := s.Connect(context.Background(), "alice", "", "quark", "我的追更账号", Credentials{"cookie": "old-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	sub, job, err := s.Subscribe("alice", serviceRequest(original, "first-follow"), 60)
	if err != nil {
		t.Fatal(err)
	}
	if result := serviceWaitJob(t, s, "alice", job.ID); result.Status != "succeeded" {
		t.Fatalf("first transfer: %+v", result)
	}
	serviceWaitSubscriptionIdle(t, s, "alice", sub.ID, 1)
	if err := s.store.update("alice", func(st *userState) error {
		a := st.Accounts[original.ID]
		a.Status, a.LastError = "expired", "expired login"
		st.Accounts[original.ID] = a
		// Reauthentication must work even when the account quota is full.
		for i := 0; i < 29; i++ {
			id := fmt.Sprintf("other-%d", i)
			st.Accounts[id] = storedAccount{Account: Account{ID: id, Provider: "quark", ProviderUserID: id}}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	renewed, err := s.Connect(context.Background(), "alice", "", "quark", "默认扫码备注", Credentials{"cookie": "new-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID != original.ID || renewed.CreatedAt != original.CreatedAt || renewed.Name != original.Name || renewed.Status != "ready" || renewed.LastError != "" {
		t.Fatalf("account not preserved: %+v", renewed)
	}
	st, err := s.store.read("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Accounts) != 30 || st.Subscriptions[sub.ID].AccountID != original.ID || st.Subscriptions[sub.ID].Transferred != 1 {
		t.Fatal("login changed account count or subscription progress")
	}
	credentials, err := s.store.decrypt("alice", st.Accounts[original.ID])
	if err != nil || credentials["cookie"] != "new-cookie" {
		t.Fatalf("new credential not bound to original account: %v", err)
	}
	repeated, err := s.CheckSubscription("alice", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result := serviceWaitJob(t, s, "alice", repeated.ID); result.Status != "succeeded" || result.Saved != 0 || result.Skipped != 1 {
		t.Fatalf("repeated login lost dedup journal: %+v", result)
	}
	if len(p.savedCalls()) != 1 {
		t.Fatal("previously saved file was submitted again")
	}
}

func TestConcurrentNewLoginsForSameIdentityCreateOneAccount(t *testing.T) {
	p := newServiceFakeProvider()
	p.identityID = "same-cloud-user"
	s := serviceStart(t, p)
	const count = 12
	var wg sync.WaitGroup
	results := make(chan Account, count)
	failures := make(chan error, count)
	start := make(chan struct{})
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			a, e := s.Connect(context.Background(), "alice", "", "quark", "", Credentials{"cookie": fmt.Sprintf("session-%d", i)})
			if e != nil {
				failures <- e
				return
			}
			results <- a
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	accounts, err := s.Accounts("alice")
	if err != nil || len(accounts) != 1 {
		t.Fatalf("duplicate accounts: %d %v", len(accounts), err)
	}
	for a := range results {
		if a.ID != accounts[0].ID {
			t.Fatal("concurrent login returned another account ID")
		}
	}
	st, err := s.store.read("alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.decrypt("alice", st.Accounts[accounts[0].ID]); err != nil {
		t.Fatalf("credential bound to temporary ID: %v", err)
	}
}

type identityProvider struct {
	*serviceFakeProvider
	providerID string
}

func (p *identityProvider) ID() string { return p.providerID }

func TestLoginDedupIsScopedToOwnerProviderAndNonemptyIdentity(t *testing.T) {
	p := newServiceFakeProvider()
	p.validateHook = func(_ context.Context, c Credentials) (Identity, error) {
		return Identity{ID: c["cookie"], Name: "相同昵称"}, nil
	}
	s, err := NewService(t.TempDir(), []Provider{p, &identityProvider{p, "baidu"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ids := map[string]bool{}
	cases := []struct{ owner, provider, cookie string }{{"alice", "quark", "A"}, {"alice", "quark", "B"}, {"bob", "quark", "A"}, {"alice", "baidu", "A"}}
	for _, tc := range cases {
		a, err := s.Connect(context.Background(), tc.owner, "", tc.provider, "", Credentials{"cookie": tc.cookie})
		if err != nil {
			t.Fatal(err)
		}
		if ids[a.ID] {
			t.Fatal("different identity/provider/owner merged")
		}
		ids[a.ID] = true
	}
	p.mu.Lock()
	p.validateHook = func(context.Context, Credentials) (Identity, error) { return Identity{Name: "相同昵称"}, nil }
	p.mu.Unlock()
	first := serviceConnect(t, s, "alice", "unknown-1")
	second := serviceConnect(t, s, "alice", "unknown-2")
	if first.ID == second.ID {
		t.Fatal("unknown identities merged by display name")
	}
}

func TestDifferentQRSessionsReuseValidatedAccount(t *testing.T) {
	p := newServiceFakeProvider()
	p.identityID = "same-qr-user"
	s := qrFixture(t, p)
	first := qrReady(t, s, "alice", "")
	second := qrReady(t, s, "alice", "")
	a, err := s.CompleteQR(context.Background(), "alice", first.ID)
	if err != nil || a.Status != "connected" {
		t.Fatalf("first scan: %v %+v", err, a)
	}
	b, err := s.CompleteQR(context.Background(), "alice", second.ID)
	if err != nil || b.Status != "connected" {
		t.Fatalf("second scan: %v %+v", err, b)
	}
	accounts, err := s.Accounts("alice")
	if err != nil || len(accounts) != 1 || a.Account.ID != b.Account.ID {
		t.Fatalf("separate QR sessions added duplicates: %d %v", len(accounts), err)
	}
}
