package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type serviceSaveCall struct{ Cookie, Path, Destination string }

type serviceFakeProvider struct {
	mu           sync.Mutex
	share        Share
	folders      map[string][]Folder
	calls        []serviceSaveCall
	reads        int
	identityID   string
	validateHook func(context.Context, Credentials) (Identity, error)
	folderHook   func(context.Context, Credentials, string) ([]Folder, error)
	validateErr  error
	readErr      error
	saveErr      error
	saveHook     func(context.Context, Entry) error
	sequence     int
}

func newServiceFakeProvider() *serviceFakeProvider {
	return &serviceFakeProvider{share: Share{ID: "shared-series", Entries: []Entry{serviceFile("episode-1", "01.mp4")}}, folders: map[string][]Folder{}}
}
func serviceFile(id, name string) Entry {
	return Entry{ID: id, Name: name, Path: name, Token: "share-file-token", ParentID: "0", Revision: "v1", Size: 42}
}
func (*serviceFakeProvider) ID() string     { return "quark" }
func (*serviceFakeProvider) RootID() string { return "0" }
func (p *serviceFakeProvider) Validate(ctx context.Context, credentials Credentials) (Identity, error) {
	p.mu.Lock()
	hook, id, err := p.validateHook, p.identityID, p.validateErr
	p.mu.Unlock()
	if hook != nil {
		return hook(ctx, credentials)
	}
	return Identity{ID: id, Name: "Test account"}, err
}
func (p *serviceFakeProvider) ListFolders(ctx context.Context, c Credentials, parent string) ([]Folder, error) {
	p.mu.Lock()
	hook := p.folderHook
	folders := append([]Folder(nil), p.folders[c["cookie"]+"\x00"+parent]...)
	p.mu.Unlock()
	if hook != nil {
		return hook(ctx, c, parent)
	}
	return folders, nil
}
func (p *serviceFakeProvider) ReadShare(_ context.Context, _ Credentials, _ ShareLink) (Share, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	result := p.share
	result.Entries = append([]Entry(nil), p.share.Entries...)
	result.Data = map[string]string{"stoken": "private-share-token"}
	return result, p.readErr
}
func (p *serviceFakeProvider) CreateFolder(_ context.Context, c Credentials, parent, name string) (Folder, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sequence++
	folder := Folder{ID: fmt.Sprintf("dir-%d", p.sequence), Name: name}
	p.folders[c["cookie"]+"\x00"+parent] = append(p.folders[c["cookie"]+"\x00"+parent], folder)
	return folder, nil
}
func (p *serviceFakeProvider) Save(ctx context.Context, c Credentials, _ Share, entry Entry, dest string) error {
	p.mu.Lock()
	p.calls = append(p.calls, serviceSaveCall{Cookie: c["cookie"], Path: entry.Path, Destination: dest})
	hook, err := p.saveHook, p.saveErr
	p.mu.Unlock()
	if hook != nil {
		return hook(ctx, entry)
	}
	return err
}
func (p *serviceFakeProvider) setEntries(entries ...Entry) {
	p.mu.Lock()
	p.share.Entries = append([]Entry(nil), entries...)
	p.mu.Unlock()
}
func (p *serviceFakeProvider) savedCalls() []serviceSaveCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]serviceSaveCall(nil), p.calls...)
}
func (p *serviceFakeProvider) setSaveError(err error) { p.mu.Lock(); p.saveErr = err; p.mu.Unlock() }

func serviceStart(t *testing.T, p *serviceFakeProvider) *Service {
	t.Helper()
	s, err := NewService(t.TempDir(), []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func serviceConnect(t *testing.T, s *Service, owner, cookie string) Account {
	t.Helper()
	a, err := s.Connect(context.Background(), owner, "", "quark", "", Credentials{"cookie": cookie})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func serviceRequest(a Account, id string) TransferRequest {
	return TransferRequest{AccountID: a.ID, ShareURL: "https://pan.quark.cn/s/series", DestinationID: "0", Title: "测试剧集", RequestID: id}
}
func serviceWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("worker did not reach the expected state")
}
func serviceWaitJob(t *testing.T, s *Service, owner, id string) Job {
	t.Helper()
	var result Job
	serviceWait(t, func() bool {
		job, err := s.Job(owner, id)
		if err != nil {
			t.Fatal(err)
		}
		result = job
		return !active(job.Status)
	})
	return result
}
func serviceWaitSubscriptionIdle(t *testing.T, s *Service, owner, id string, transferred int) {
	t.Helper()
	serviceWait(t, func() bool {
		state, err := s.store.read(owner)
		if err != nil {
			t.Fatal(err)
		}
		sub := state.Subscriptions[id]
		return sub.Status == "idle" && sub.Transferred == transferred
	})
}

func TestServiceSeparatesUsersAndNeverReturnsCredentials(t *testing.T) {
	p := newServiceFakeProvider()
	s := serviceStart(t, p)
	alice := serviceConnect(t, s, "alice", "alice-private-cookie")
	bob := serviceConnect(t, s, "bob", "bob-private-cookie")
	for owner, want := range map[string]string{"alice": alice.ID, "bob": bob.ID} {
		accounts, err := s.Accounts(owner)
		if err != nil || len(accounts) != 1 || accounts[0].ID != want {
			t.Fatalf("account isolation failed for %s: %+v %v", owner, accounts, err)
		}
	}
	if _, err := s.Folders(context.Background(), "bob", alice.ID, "0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign folders: %v", err)
	}
	if _, err := s.CheckAccount(context.Background(), "bob", alice.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign check: %v", err)
	}
	if err := s.DeleteAccount("bob", alice.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if _, err := s.Connect(context.Background(), "bob", alice.ID, "quark", "", Credentials{"cookie": "replacement"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign reconnect: %v", err)
	}
	if _, err := s.Transfer("bob", serviceRequest(alice, "foreign")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign transfer: %v", err)
	}
	sub, job, err := s.Subscribe("alice", serviceRequest(alice, ""), 15)
	if err != nil {
		t.Fatal(err)
	}
	if result := serviceWaitJob(t, s, "alice", job.ID); result.Status != "succeeded" {
		t.Fatalf("job failed: %+v", result)
	}
	if _, err := s.Job("bob", job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign job: %v", err)
	}
	enabled := false
	if _, err := s.UpdateSubscription("bob", sub.ID, &enabled, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign subscription update: %v", err)
	}
	if _, err := s.CheckSubscription("bob", sub.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign subscription check: %v", err)
	}
	if err := s.DeleteSubscription("bob", sub.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign subscription delete: %v", err)
	}
	others, err := s.Subscriptions("bob")
	if err != nil || len(others) != 0 {
		t.Fatal("subscriptions leaked between users")
	}
	otherJobs, err := s.Jobs("bob")
	if err != nil || len(otherJobs) != 0 {
		t.Fatal("jobs leaked between users")
	}
	accounts, _ := s.Accounts("alice")
	subscriptions, _ := s.Subscriptions("alice")
	jobs, _ := s.Jobs("alice")
	response, err := json.Marshal([]any{accounts, subscriptions, jobs, sub, job})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"alice-private-cookie", "bob-private-cookie", "private-share-token", "share-file-token", "encrypted_credentials"} {
		if strings.Contains(string(response), secret) {
			t.Fatalf("API response leaks credential field %s", secret)
		}
	}
	bobJob, err := s.Transfer("bob", serviceRequest(bob, "bob-first"))
	if err != nil {
		t.Fatal(err)
	}
	if result := serviceWaitJob(t, s, "bob", bobJob.ID); result.Saved != 1 || result.Skipped != 0 {
		t.Fatalf("one user's transfer suppressed another user's transfer: %+v", result)
	}
}

func TestServiceOneTimeAndSubscriptionSaveOnlyNewFilesIncludingSubdirectories(t *testing.T) {
	p := newServiceFakeProvider()
	directory := Entry{ID: "season", Name: "第一季", Path: "第一季", IsDir: true, Revision: "dir"}
	first := serviceFile("e1", "01.mp4")
	first.Path = "第一季/01.mp4"
	first.ParentID = "season"
	p.setEntries(directory, first)
	s := serviceStart(t, p)
	a := serviceConnect(t, s, "alice", "private-cookie")
	req := serviceRequest(a, "initial-save")
	job, err := s.Transfer("alice", req)
	if err != nil {
		t.Fatal(err)
	}
	result := serviceWaitJob(t, s, "alice", job.ID)
	if result.Status != "succeeded" || result.Saved != 1 || result.Total != 1 {
		t.Fatalf("initial transfer: %+v", result)
	}
	calls := p.savedCalls()
	if len(calls) != 1 || calls[0].Path != "第一季/01.mp4" || calls[0].Destination == "0" {
		t.Fatalf("nested path was flattened: %+v", calls)
	}
	duplicate, err := s.Transfer("alice", req)
	if err != nil || duplicate.ID != job.ID {
		t.Fatalf("request replay is not idempotent: %+v %v", duplicate, err)
	}
	conflicting := req
	conflicting.DestinationID = "another-directory"
	if _, err := s.Transfer("alice", conflicting); err == nil {
		t.Fatal("reused idempotency key for a different write")
	}
	sub, firstCheck, err := s.Subscribe("alice", req, 15)
	if err != nil {
		t.Fatal(err)
	}
	result = serviceWaitJob(t, s, "alice", firstCheck.ID)
	if result.Status != "succeeded" || result.Saved != 0 || result.Skipped != 1 {
		t.Fatalf("subscription re-saved a one-time transfer: %+v", result)
	}
	replayedSub, replayedJob, err := s.Subscribe("alice", req, 15)
	if err != nil || replayedSub.ID != sub.ID || replayedJob.ID != firstCheck.ID {
		t.Fatalf("subscription request replay created a duplicate: sub=%s job=%s err=%v", replayedSub.ID, replayedJob.ID, err)
	}
	duplicateRequest := req
	duplicateRequest.RequestID = "another-subscription-request"
	if _, _, err := s.Subscribe("alice", duplicateRequest, 15); err == nil {
		t.Fatal("same share created a duplicate subscription with a fresh request ID")
	}
	second := serviceFile("e2", "02.mp4")
	second.Path = "第一季/02.mp4"
	second.ParentID = "season"
	p.setEntries(directory, first, second)
	check, err := s.CheckSubscription("alice", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	result = serviceWaitJob(t, s, "alice", check.ID)
	if result.Status != "succeeded" || result.Saved != 1 || result.Skipped != 1 || result.Total != 2 {
		t.Fatalf("incremental check: %+v", result)
	}
	calls = p.savedCalls()
	if len(calls) != 2 || calls[1].Path != "第一季/02.mp4" || calls[0].Destination != calls[1].Destination {
		t.Fatalf("new episode did not reuse its folder: %+v", calls)
	}
	another, err := s.Transfer("alice", serviceRequest(a, "second-save"))
	if err != nil {
		t.Fatal(err)
	}
	result = serviceWaitJob(t, s, "alice", another.ID)
	if result.Saved != 0 || result.Skipped != 2 || len(p.savedCalls()) != 2 {
		t.Fatalf("same share written twice: %+v", result)
	}
}

func TestServiceSubscriptionFirstRunPauseResumeAndDelete(t *testing.T) {
	p := newServiceFakeProvider()
	s := serviceStart(t, p)
	a := serviceConnect(t, s, "alice", "private-cookie")
	sub, job, err := s.Subscribe("alice", serviceRequest(a, ""), 15)
	if err != nil {
		t.Fatal(err)
	}
	if result := serviceWaitJob(t, s, "alice", job.ID); result.Saved != 1 {
		t.Fatalf("first subscription did not save initial content: %+v", result)
	}
	paused := false
	if result, err := s.UpdateSubscription("alice", sub.ID, &paused, nil, nil); err != nil || result.Enabled || result.Status != "paused" {
		t.Fatalf("pause failed: %+v %v", result, err)
	}
	if _, err := s.CheckSubscription("alice", sub.ID); err == nil {
		t.Fatal("paused subscription allowed a manual check")
	}
	before, _ := s.Jobs("alice")
	p.setEntries(serviceFile("episode-1", "01.mp4"), serviceFile("episode-2", "02.mp4"))
	if err := s.store.update("alice", func(state *userState) error {
		sub := state.Subscriptions[sub.ID]
		sub.NextCheckAt = now() - 1
		state.Subscriptions[sub.ID] = sub
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.schedule()
	after, _ := s.Jobs("alice")
	if len(after) != len(before) || len(p.savedCalls()) != 1 {
		t.Fatal("scheduler executed a paused subscription")
	}
	resumed := true
	if _, err := s.UpdateSubscription("alice", sub.ID, &resumed, nil, nil); err != nil {
		t.Fatal(err)
	}
	serviceWaitSubscriptionIdle(t, s, "alice", sub.ID, 2)
	if len(p.savedCalls()) != 2 {
		t.Fatal("resume did not check and save new episode")
	}
	if err := s.DeleteAccount("alice", a.ID); err == nil {
		t.Fatal("deleted an account still used by a subscription")
	}
	if err := s.DeleteSubscription("alice", sub.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount("alice", a.ID); err != nil {
		t.Fatal(err)
	}
	accounts, _ := s.Accounts("alice")
	subs, _ := s.Subscriptions("alice")
	if len(accounts) != 0 || len(subs) != 0 {
		t.Fatal("delete did not remove account and subscription")
	}
}

func TestServiceUnconfirmedSaveSurvivesRestartWithoutRepeatingWrite(t *testing.T) {
	p := newServiceFakeProvider()
	p.setSaveError(errors.New("connection lost after remote accepted transfer"))
	dir := t.TempDir()
	s, err := NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	a := serviceConnect(t, s, "alice", "private-cookie")
	job, err := s.Transfer("alice", serviceRequest(a, "uncertain-first"))
	if err != nil {
		t.Fatal(err)
	}
	if result := serviceWaitJob(t, s, "alice", job.ID); result.Status != "failed" || result.Saved != 0 {
		t.Fatalf("unconfirmed write was reported successful: %+v", result)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	p.setSaveError(nil)
	s, err = NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	retry, err := s.Transfer("alice", serviceRequest(a, "uncertain-retry"))
	if err != nil {
		t.Fatal(err)
	}
	result := serviceWaitJob(t, s, "alice", retry.ID)
	if result.Status != "failed" || !strings.Contains(result.Error, "未确认") || len(p.savedCalls()) != 1 {
		t.Fatalf("unconfirmed result retried a remote write: %+v calls=%d", result, len(p.savedCalls()))
	}
}

func TestServiceAuthExpiryAfterSubmissionKeepsUncertainJournal(t *testing.T) {
	p := newServiceFakeProvider()
	p.setSaveError(ErrAuthExpired)
	s := serviceStart(t, p)
	a := serviceConnect(t, s, "alice", "old-private-cookie")
	if err := s.store.update("alice", func(st *userState) error {
		account := st.Accounts[a.ID]
		account.AuthCheckedAt = 1
		st.Accounts[a.ID] = account
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	job, err := s.Transfer("alice", serviceRequest(a, "expired-poll"))
	if err != nil {
		t.Fatal(err)
	}
	if result := serviceWaitJob(t, s, "alice", job.ID); result.Status != "failed" {
		t.Fatalf("expired save succeeded: %+v", result)
	}
	accounts, _ := s.Accounts("alice")
	if len(accounts) != 1 || accounts[0].Status != "expired" || accounts[0].AuthCheckedAt <= 1 {
		t.Fatalf("account did not expire: %+v", accounts)
	}
	if _, err := s.Transfer("alice", serviceRequest(a, "before-reconnect")); err == nil {
		t.Fatal("expired account accepted a new write")
	}
	p.setSaveError(nil)
	if _, err := s.Connect(context.Background(), "alice", a.ID, "quark", "", Credentials{"cookie": "new-private-cookie"}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Transfer("alice", serviceRequest(a, "after-reconnect"))
	if err != nil {
		t.Fatal(err)
	}
	result := serviceWaitJob(t, s, "alice", retry.ID)
	if result.Status != "failed" || !strings.Contains(result.Error, "未确认") || len(p.savedCalls()) != 1 {
		t.Fatalf("post-submission auth expiry allowed duplicate write: %+v calls=%d", result, len(p.savedCalls()))
	}
}

func TestServiceRestartRecoversQueuedAndInterruptedJobs(t *testing.T) {
	p := newServiceFakeProvider()
	dir := t.TempDir()
	s, err := NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	a := serviceConnect(t, s, "alice", "private-cookie")
	s.cancel()
	s.wg.Wait()
	queued := jobRecord{Job: Job{ID: "queued-before-restart", AccountID: a.ID, Provider: "quark", Status: "queued", CreatedAt: now()}, Request: serviceRequest(a, "queued-request")}
	running := jobRecord{Job: Job{ID: "running-before-restart", AccountID: a.ID, Provider: "quark", Status: "running", CreatedAt: now()}, Request: serviceRequest(a, "running-request")}
	if err := s.store.update("alice", func(state *userState) error {
		state.Jobs[queued.ID] = queued
		state.Jobs[running.ID] = running
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	result := serviceWaitJob(t, s, "alice", queued.ID)
	if result.Status != "succeeded" || result.Saved != 1 {
		t.Fatalf("queued job was lost on restart: %+v", result)
	}
	interrupted, err := s.Job("alice", running.ID)
	if err != nil || interrupted.Status != "interrupted" || interrupted.FinishedAt == 0 {
		t.Fatalf("running job was not marked interrupted: %+v %v", interrupted, err)
	}
	if len(p.savedCalls()) != 1 {
		t.Fatal("interrupted job was blindly replayed")
	}
}

func TestServiceAccountErrorsRedactSecrets(t *testing.T) {
	p := newServiceFakeProvider()
	s := serviceStart(t, p)
	a := serviceConnect(t, s, "alice", "private-cookie-value")
	p.mu.Lock()
	p.validateErr = fmt.Errorf("rejected credential %s", "private-cookie-value")
	p.mu.Unlock()
	account, err := s.CheckAccount(context.Background(), "alice", a.ID)
	if err != nil || account.Status != "error" || strings.Contains(account.LastError, "private-cookie-value") {
		t.Fatalf("unsafe account error: %+v %v", account, err)
	}
	p.mu.Lock()
	p.validateErr = ErrAuthExpired
	p.mu.Unlock()
	account, err = s.CheckAccount(context.Background(), "alice", a.ID)
	if err != nil || account.Status != "expired" {
		t.Fatalf("expired login not recorded: %+v %v", account, err)
	}
}

func TestServiceErrorsRedactIndividualCookieValues(t *testing.T) {
	p := newServiceFakeProvider()
	s := serviceStart(t, p)
	cookie := "BDUSS=private-bduss-component; STOKEN=private-stoken-component"
	a := serviceConnect(t, s, "alice", cookie)
	p.mu.Lock()
	p.validateErr = errors.New("upstream rejected BDUSS private-bduss-component and STOKEN private-stoken-component")
	p.mu.Unlock()
	account, err := s.CheckAccount(context.Background(), "alice", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-bduss-component", "private-stoken-component"} {
		if strings.Contains(account.LastError, secret) {
			t.Fatalf("account error exposed an individual Cookie value: %s", secret)
		}
	}
}

func TestServiceReconnectCannotReplaceUnderlyingCloudIdentity(t *testing.T) {
	p := newServiceFakeProvider()
	p.identityID = "provider-user-A"
	s := serviceStart(t, p)
	a := serviceConnect(t, s, "alice", "original-account-cookie")
	before, err := s.store.read("alice")
	if err != nil {
		t.Fatal(err)
	}
	original := before.Accounts[a.ID]
	p.mu.Lock()
	p.identityID = "provider-user-B"
	p.mu.Unlock()
	if _, err := s.Connect(context.Background(), "alice", a.ID, "quark", "", Credentials{"cookie": "different-account-cookie"}); err == nil {
		t.Fatal("reconnect silently changed the underlying cloud account")
	}
	after, err := s.store.read("alice")
	if err != nil {
		t.Fatal(err)
	}
	preserved := after.Accounts[a.ID]
	if preserved.Secret != original.Secret || preserved.ProviderUserID != "provider-user-A" {
		t.Fatal("rejected reconnect changed the original identity or credentials")
	}
	p.mu.Lock()
	p.identityID = "provider-user-A"
	p.mu.Unlock()
	renewed, err := s.Connect(context.Background(), "alice", a.ID, "quark", "", Credentials{"cookie": "renewed-account-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ProviderUserID != "provider-user-A" {
		t.Fatal("same-account renewal lost identity")
	}
	state, err := s.store.read("alice")
	if err != nil {
		t.Fatal(err)
	}
	creds, err := s.store.decrypt("alice", state.Accounts[a.ID])
	if err != nil || creds["cookie"] != "renewed-account-cookie" {
		t.Fatalf("same-account renewal did not persist credentials: %v", err)
	}
}

func TestServiceStopsWritingWhenSelectedDirectoryIsRemovedOrReplaced(t *testing.T) {
	for _, tc := range []struct {
		name    string
		folders []Folder
	}{
		{"removed", nil},
		{"replaced", []Folder{{ID: "new-different-id", Name: "追更"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newServiceFakeProvider()
			p.folders["private-cookie\x000"] = []Folder{{ID: "selected-dir", Name: "追更"}}
			s := serviceStart(t, p)
			a := serviceConnect(t, s, "alice", "private-cookie")
			req := serviceRequest(a, "original-target")
			req.DestinationID = "selected-dir"
			req.DestinationName = "/追更"
			first, err := s.Transfer("alice", req)
			if err != nil {
				t.Fatal(err)
			}
			if result := serviceWaitJob(t, s, "alice", first.ID); result.Status != "succeeded" {
				t.Fatalf("valid target rejected: %+v", result)
			}
			p.mu.Lock()
			p.folders["private-cookie\x000"] = tc.folders
			p.mu.Unlock()
			p.setEntries(serviceFile("episode-1", "01.mp4"), serviceFile("episode-2", "02.mp4"))
			req.RequestID = "after-target-change"
			retry, err := s.Transfer("alice", req)
			if err != nil {
				t.Fatal(err)
			}
			result := serviceWaitJob(t, s, "alice", retry.ID)
			if result.Status != "failed" || len(p.savedCalls()) != 1 {
				t.Fatalf("changed target allowed a write: %+v calls=%d", result, len(p.savedCalls()))
			}
		})
	}
}

func TestServiceRotatedCredentialsCannotBeOverwrittenByStaleContext(t *testing.T) {
	p := newServiceFakeProvider()
	s := serviceStart(t, p)
	account := serviceConnect(t, s, "alice", "original-cookie")
	first, _, _, err := s.account("alice", account.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := first
	currentContext := s.credentialContext(context.Background(), "alice", &first)
	staleContext := s.credentialContext(context.Background(), "alice", &stale)
	for _, secret := range []string{"rotated-once", "rotated-twice"} {
		if err := CommitCredentials(currentContext, Credentials{"cookie": secret}); err != nil {
			t.Fatal(err)
		}
	}
	if err := CommitCredentials(staleContext, Credentials{"cookie": "stale-overwrite"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("stale context could overwrite rotated credentials: %v", err)
	}
	_, _, credentials, err := s.account("alice", account.ID)
	if err != nil || credentials["cookie"] != "rotated-twice" {
		t.Fatalf("latest rotation was lost: %v", err)
	}
	if err := CommitCredentials(context.Background(), Credentials{"cookie": "no-persistence-handler"}); err == nil {
		t.Fatal("rotation succeeded without persistence context")
	}
}

func TestServiceSerializesAccountOperationsAcrossCredentialRotation(t *testing.T) {
	p := newServiceFakeProvider()
	s := serviceStart(t, p)
	account := serviceConnect(t, s, "alice", "original-cookie")
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	folderDone := make(chan error, 1)
	checkDone := make(chan error, 1)
	observed := make(chan string, 1)
	p.mu.Lock()
	p.folderHook = func(ctx context.Context, _ Credentials, _ string) ([]Folder, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []Folder{}, CommitCredentials(ctx, Credentials{"cookie": "rotated-during-browse"})
	}
	p.validateHook = func(_ context.Context, c Credentials) (Identity, error) {
		observed <- c["cookie"]
		return Identity{Name: "Test account"}, nil
	}
	p.mu.Unlock()
	go func() { _, err := s.Folders(context.Background(), "alice", account.ID, "0"); folderDone <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("folder operation did not start")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.CheckAccount(canceled, "alice", account.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting account operation ignored cancellation: %v", err)
	}
	go func() { _, err := s.CheckAccount(context.Background(), "alice", account.ID); checkDone <- err }()
	select {
	case <-observed:
		t.Fatal("concurrent operation used credentials before rotation finished")
	case <-time.After(10 * time.Millisecond):
	}
	unblock()
	if err := <-folderDone; err != nil {
		t.Fatal(err)
	}
	if err := <-checkDone; err != nil {
		t.Fatal(err)
	}
	if value := <-observed; value != "rotated-during-browse" {
		t.Fatal("serialized operation used stale credentials")
	}
	_, _, credentials, err := s.account("alice", account.ID)
	if err != nil || credentials["cookie"] != "rotated-during-browse" {
		t.Fatalf("rotated credentials were not saved: %v", err)
	}
}
