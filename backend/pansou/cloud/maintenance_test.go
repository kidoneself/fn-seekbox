package cloud

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestUpdateDrainPreservesQueuedJobsAcrossOwners(t *testing.T) {
	t.Setenv("SEEKBOX_CENTER_URL", "http://127.0.0.1:1")
	p := newServiceFakeProvider()
	started, finish := make(chan struct{}), make(chan struct{})
	var saves atomic.Int32
	p.saveHook = func(ctx context.Context, _ Entry) error {
		if saves.Add(1) == 1 {
			close(started)
			select {
			case <-finish:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	s := serviceStart(t, p)
	alice := serviceConnect(t, s, "alice", "alice-cookie")
	bob := serviceConnect(t, s, "bob", "bob-cookie")
	first, err := s.Transfer("alice", serviceRequest(alice, "first"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	second, err := s.Transfer("bob", serviceRequest(bob, "second"))
	if err != nil {
		t.Fatal(err)
	}
	prepared := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { prepared <- s.PrepareUpdate(ctx) }()
	serviceWait(t, func() bool { paused, _ := s.UpdateMaintenance(); return paused })
	if _, err := s.Transfer("bob", serviceRequest(bob, "third")); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("admission remained open: %v", err)
	}
	select {
	case <-prepared:
		t.Fatal("drained during live save")
	default:
	}
	close(finish)
	if err := <-prepared; err != nil {
		t.Fatal(err)
	}
	done, _ := s.Job("alice", first.ID)
	queued, _ := s.Job("bob", second.ID)
	if done.Status != "succeeded" || queued.Status != "queued" || saves.Load() != 1 {
		t.Fatalf("drain changed work: first=%s queued=%s saves=%d", done.Status, queued.Status, saves.Load())
	}
	for range 20 {
		if s.runCycle() {
			t.Fatal("worker claimed during maintenance")
		}
	}
	s.ResumeUpdate()
	serviceWaitJob(t, s, "bob", second.ID)
	if saves.Load() != 2 {
		t.Fatal("queued work was not resumed")
	}
}

func TestUpdateDrainCancellationReopensAdmission(t *testing.T) {
	t.Setenv("SEEKBOX_CENTER_URL", "http://127.0.0.1:1")
	s := serviceStart(t, newServiceFakeProvider())
	release, err := s.beginOperation()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	prepared := make(chan error, 1)
	go func() { prepared <- s.PrepareUpdate(ctx) }()
	serviceWait(t, func() bool { p, _ := s.UpdateMaintenance(); return p })
	if err := s.PrepareUpdate(context.Background()); !errors.Is(err, ErrMaintenance) {
		t.Fatal("second update took over maintenance")
	}
	cancel()
	if err := <-prepared; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	extra, err := s.beginOperation()
	if err != nil {
		t.Fatal("canceled update left service paused")
	}
	extra()
}

func TestUpdateDrainWaitsForQRCompletion(t *testing.T) {
	t.Setenv("SEEKBOX_CENTER_URL", "http://127.0.0.1:1")
	s := qrFixture(t, newServiceFakeProvider())
	qr := qrReady(t, s, "alice", "")
	prepared := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { prepared <- s.PrepareUpdate(ctx) }()
	serviceWait(t, func() bool { p, _ := s.UpdateMaintenance(); return p })
	if _, err := s.StartQR("bob", "quark", "", ""); !errors.Is(err, ErrMaintenance) {
		t.Fatal("new QR admitted")
	}
	select {
	case <-prepared:
		t.Fatal("ready QR credentials were not awaited")
	default:
	}
	result, err := s.CompleteQR(context.Background(), "alice", qr.ID)
	if err != nil || result.Status != "connected" {
		t.Fatalf("existing QR unable to finish: %v %s", err, result.Status)
	}
	if err := <-prepared; err != nil {
		t.Fatal(err)
	}
	accounts, _ := s.Accounts("alice")
	if len(accounts) != 1 {
		t.Fatal("QR account was not saved")
	}
	s.ResumeUpdate()
}

func TestUpdateCandidateStartsPausedBeforeTakingQueuedWork(t *testing.T) {
	t.Setenv("SEEKBOX_CENTER_URL", "http://127.0.0.1:1")
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	provider := newServiceFakeProvider()
	seed := &Service{store: st, providers: map[string]Provider{"quark": provider}, wake: make(chan struct{}, 1)}
	account := serviceConnect(t, seed, "alice", "private-cookie")
	queued, err := seed.Transfer("alice", serviceRequest(account, "queued-before-upgrade"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.db.Close(); err != nil {
		t.Fatal(err)
	}
	candidate, err := NewServiceWithStartupMaintenance(dir, []Provider{provider}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Close()
	if paused, active := candidate.UpdateMaintenance(); !paused || active != 0 {
		t.Fatalf("candidate worker was admitted: paused=%v active=%d", paused, active)
	}
	if candidate.runCycle() {
		t.Fatal("candidate claimed queued task before health commit")
	}
	if _, err := candidate.Transfer("alice", serviceRequest(account, "during-health-check")); !errors.Is(err, ErrMaintenance) {
		t.Fatal("candidate accepted new work")
	}
	job, _ := candidate.Job("alice", queued.ID)
	if job.Status != "queued" || len(provider.savedCalls()) != 0 {
		t.Fatal("candidate changed queued transfer")
	}
	candidate.ResumeUpdate() // Only the matched terminal transaction may call this.
	finished := serviceWaitJob(t, candidate, "alice", queued.ID)
	if finished.Status != "succeeded" || len(provider.savedCalls()) != 1 {
		t.Fatal("committed runtime did not resume its queue")
	}
}
