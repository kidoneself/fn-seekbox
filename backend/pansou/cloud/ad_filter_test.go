package cloud

import (
	"errors"
	"reflect"
	"testing"
)

func TestAdAttachmentRules(t *testing.T) {
	for _, name := range []string{"加群领取资源.TXT", "扫码关注.jpg", "Q Q 群.url", "关注【公众号】.png", "广告.txt", "推广链接.html", "更多资源.txt", "防失联.webp"} {
		if !isAdAttachment(serviceFile("id", name)) {
			t.Errorf("missed attachment %s", name)
		}
	}
	for _, name := range []string{"广告狂人.txt", "公众号运营教程.txt", "说明.txt", "封面.jpg", "广告.mp4", "扫码关注.srt", "推广链接.zip", "加群.pdf", "广告.exe", "加群.mp3", "推广.epub"} {
		if isAdAttachment(serviceFile("id", name)) {
			t.Errorf("excluded normal/protected file %s", name)
		}
	}
	if isAdAttachment(Entry{Name: "加群.txt", IsDir: true}) {
		t.Fatal("directory classified as attachment")
	}
}

func TestAdFilterKeepsMediaUnderPromotionalDirectory(t *testing.T) {
	entries := []Entry{
		{ID: "d1", Name: "加群", Path: "加群", IsDir: true},
		{ID: "d2", Name: "广告", Path: "广告", IsDir: true},
		{ID: "f1", Name: "01.mkv", Path: "加群/01.mkv"},
		{ID: "f2", Name: "加群.txt", Path: "广告/加群.txt"},
	}
	kept, n := filterAdAttachments(entries)
	if n != 1 || len(kept) != 2 || kept[0].ID != "f1" || kept[1].ID != "d1" {
		t.Fatalf("wrong filtered tree: %+v, %d", kept, n)
	}
}

func TestAdFilterWorkerPreservesBatchingAndCanSaveExcludedFilesLater(t *testing.T) {
	p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider()}
	p.setEntries(serviceFile("one", "01.mkv"), serviceFile("two", "02.mkv"), serviceFile("ad", "加群.txt"))
	s, a := batchService(t, p)
	req := serviceRequest(a, "filtered")
	req.FilterAds = true
	j, err := s.Transfer("alice", req)
	if err != nil {
		t.Fatal(err)
	}
	got := serviceWaitJob(t, s, "alice", j.ID)
	if got.Status != "succeeded" || got.Total != 3 || got.Saved != 2 || got.Filtered != 1 || !reflect.DeepEqual(p.batchSizes(), []int{2}) {
		t.Fatalf("bad filtered transfer %+v batches %v", got, p.batchSizes())
	}
	req.FilterAds = false
	if _, err = s.Transfer("alice", req); err == nil {
		t.Fatal("same request ID accepted different filter")
	}
	req.RequestID = "unfiltered"
	j, err = s.Transfer("alice", req)
	if err != nil {
		t.Fatal(err)
	}
	got = serviceWaitJob(t, s, "alice", j.ID)
	if got.Status != "succeeded" || got.Saved != 1 || got.Skipped != 2 || got.Filtered != 0 || !reflect.DeepEqual(p.batchSizes(), []int{2, 1}) {
		t.Fatalf("excluded file could not be saved later %+v", got)
	}
	p.batchMu.Lock()
	defer p.batchMu.Unlock()
	if p.batches[1][0].ID != "ad" {
		t.Fatal("wrong file saved on disabling filter")
	}
}

func TestAdFilterAllAdsAndLegacyDefault(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider()}
		ad := serviceFile("ad", "扫码关注.png")
		ad.Path = "推广/" + ad.Name
		p.setEntries(Entry{ID: "dir", Name: "推广", Path: "推广", IsDir: true}, ad)
		s, a := batchService(t, p)
		req := serviceRequest(a, "all-ads")
		req.FilterAds = enabled
		j, err := s.Transfer("alice", req)
		if err != nil {
			t.Fatal(err)
		}
		got := serviceWaitJob(t, s, "alice", j.ID)
		if got.Status != "succeeded" || got.Total != 1 {
			t.Fatalf("bad job %+v", got)
		}
		if enabled {
			if got.Filtered != 1 || got.Saved != 0 || len(p.batchSizes()) != 0 {
				t.Fatalf("ads submitted %+v", got)
			}
			p.mu.Lock()
			created := p.sequence
			p.mu.Unlock()
			if created != 0 {
				t.Fatal("created empty ad folder")
			}
		} else if got.Filtered != 0 || got.Saved != 1 {
			t.Fatalf("legacy transfer behavior changed %+v", got)
		}
	}
}

func TestSubscriptionAdFilterPersistsAndToggles(t *testing.T) {
	p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider()}
	entries := []Entry{serviceFile("one", "01.mkv"), serviceFile("ad1", "加群.txt")}
	p.setEntries(entries...)
	s, a := batchService(t, p)
	req := serviceRequest(a, "follow-filter")
	req.FilterAds = true
	sub, j, err := s.Subscribe("alice", req, 15)
	if err != nil {
		t.Fatal(err)
	}
	got := serviceWaitJob(t, s, "alice", j.ID)
	if got.Filtered != 1 || got.Saved != 1 {
		t.Fatalf("initial follow %+v", got)
	}
	serviceWaitSubscriptionIdle(t, s, "alice", sub.ID, 1)
	req.FilterAds = false
	if _, _, err = s.Subscribe("alice", req, 15); err == nil {
		t.Fatal("same subscription request ID accepted different filter")
	}
	entries = append(entries, serviceFile("two", "02.mkv"), serviceFile("ad2", "推广链接.url"))
	p.setEntries(entries...)
	j, err = s.CheckSubscription("alice", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = serviceWaitJob(t, s, "alice", j.ID)
	if got.Filtered != 2 || got.Saved != 1 || got.Skipped != 1 {
		t.Fatalf("follow did not inherit filtering %+v", got)
	}
	serviceWaitSubscriptionIdle(t, s, "alice", sub.ID, 2)
	off := false
	if _, err = s.UpdateSubscription("bob", sub.ID, nil, nil, &off); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign toggle: %v", err)
	}
	sub, err = s.UpdateSubscription("alice", sub.ID, nil, nil, &off)
	if err != nil || sub.FilterAds {
		t.Fatalf("toggle failed %+v %v", sub, err)
	}
	j, err = s.CheckSubscription("alice", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = serviceWaitJob(t, s, "alice", j.ID)
	if got.Filtered != 0 || got.Saved != 2 || got.Skipped != 2 {
		t.Fatalf("toggle did not apply %+v", got)
	}
}

func TestSubscriptionRejectsFilterChangeDuringTransfer(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider(), outcome: func(e []Entry) (BatchResult, error) {
		close(started)
		<-release
		return BatchResult{Submitted: true, Confirmed: []string{e[0].ID}}, nil
	}}
	s, a := batchService(t, p)
	defer close(release)
	sub, _, err := s.Subscribe("alice", serviceRequest(a, "blocked"), 15)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	on := true
	if _, err = s.UpdateSubscription("alice", sub.ID, nil, nil, &on); err == nil {
		t.Fatal("filter changed during active transfer")
	}
}
