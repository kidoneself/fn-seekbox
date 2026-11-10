package cloud

import (
	"errors"
	"path"
	"testing"
)

func TestContentProgress(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		names                 []string
		kind, label, evidence string
	}{
		{"数字不是数量", []string{"01.mkv", "45.mkv", "45-4K.mkv", "花絮99.mp4"}, "episode", "第45集", "45-4K.mkv"},
		{"季度顺序", []string{"S01E99.mkv", "S02E03.mkv"}, "episode", "第2季 · 第3集", "S02E03.mkv"},
		{"中文目录", []string{"第二季/01.mp4", "第二季/第十二集.mkv"}, "episode", "第2季 · 第12集", "第二季/第十二集.mkv"},
		{"英文目录", []string{"Season 2/EP07.mkv"}, "episode", "第2季 · 第7集", "Season 2/EP07.mkv"},
		{"期数上下", []string{"节目第十二期上.mp4", "节目第十二期（下）.mp4", "节目第99期加更.mp4"}, "issue", "第12期（下）", "节目第十二期（下）.mp4"},
		{"日期期数", []string{"2026.11.01第7期上.mp4", "2026.11.02第7期下.mp4"}, "issue", "第7期（下）", "2026.11.02第7期下.mp4"},
		{"年份清晰度广告", []string{"2026.mkv", "2160.mp4", "1080P.mkv", "第99集.txt", "2026.11.01电影.mkv"}, "unknown", "", ""},
		{"普通压缩包", []string{"01.zip", "99.rar"}, "unknown", "", ""},
		{"日期跨年", []string{"20251231正片.mp4", "20260101正片.mp4"}, "date", "2026-01-01", "20260101正片.mp4"},
		{"非法日期", []string{"2026.02.29.mp4", "2026.13.01.mp4"}, "unknown", "", ""},
		{"目录更新到不能信", []string{"全60集/01.mp4", "更新到第60集/02.mp4"}, "episode", "第2集", "更新到第60集/02.mp4"},
		{"目录日期不能信", []string{"2026.01.01备份/正片.mp4", "2026.01.02备份/正片.mp4"}, "unknown", "", ""},
		{"混合类型", []string{"第10集.mkv", "第20期.mp4"}, "unknown", "", ""},
		{"彩蛋目录", []string{"花絮/第99集.mp4", "第8集.mp4"}, "episode", "第8集", "第8集.mp4"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entries := []Entry{}
			for _, name := range tt.names {
				entries = append(entries, Entry{Name: path.Base(name), Path: name})
			}
			got := analyzeProgress(entries)
			if got.Kind != tt.kind || got.Label != tt.label || got.Evidence != tt.evidence {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestFollowProgressPartialRetryRestartAndFailedInspection(t *testing.T) {
	p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider(), outcome: func(e []Entry) (BatchResult, error) {
		return BatchResult{Submitted: true, Confirmed: []string{e[0].ID}}, errors.New("uncertain result")
	}}
	p.setEntries(serviceFile("one", "01.mkv"), serviceFile("two", "02.mkv"))
	dir := t.TempDir()
	s, err := NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	a := serviceConnect(t, s, "alice", "cookie")
	sub, j, err := s.Subscribe("alice", serviceRequest(a, ""), 15)
	if err != nil {
		t.Fatal(err)
	}
	got := serviceWaitJob(t, s, "alice", j.ID)
	assertProgress := func(j Job) {
		t.Helper()
		if j.Progress == nil || j.Progress.Source.Label != "第2集" || j.Progress.Saved.Label != "第1集" || j.Progress.ConfirmedFiles != 1 {
			t.Fatalf("wrong confirmed progress: %+v", j)
		}
	}
	assertProgress(got)
	if got.Status != "partial" {
		t.Fatal(got.Status)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.store.read("alice")
	if err != nil {
		t.Fatal(err)
	}
	if state.Subscriptions[sub.ID].Progress.Saved.Label != "第1集" {
		t.Fatal("restart lost progress")
	}
	j, err = s.CheckSubscription("alice", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertProgress(serviceWaitJob(t, s, "alice", j.ID))
	if len(p.batchSizes()) != 1 {
		t.Fatal("retry wrote an uncertain file")
	}
	p.mu.Lock()
	p.readErr = errors.New("upstream unavailable")
	p.mu.Unlock()
	j, err = s.CheckSubscription("alice", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = serviceWaitJob(t, s, "alice", j.ID)
	if got.Progress != nil {
		t.Fatal("failed inspection fabricated progress")
	}
	state, _ = s.store.read("alice")
	if state.Subscriptions[sub.ID].Progress.Source.Label != "第2集" {
		t.Fatal("failed inspection erased previous observation")
	}
}

func TestFollowProgressBackfillsNoNewFilesAndCannotCrossDestination(t *testing.T) {
	p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider()}
	p.setEntries(serviceFile("one", "01.mkv"), serviceFile("three", "03.mkv"))
	s, a := batchService(t, p)
	sub, j, err := s.Subscribe("alice", serviceRequest(a, ""), 15)
	if err != nil {
		t.Fatal(err)
	}
	serviceWaitJob(t, s, "alice", j.ID)
	// Simulate a subscription created by a version without progress metadata.
	if err = s.store.update("alice", func(state *userState) error {
		v := state.Subscriptions[sub.ID]
		v.Progress = nil
		state.Subscriptions[sub.ID] = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	j, err = s.CheckSubscription("alice", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := serviceWaitJob(t, s, "alice", j.ID)
	if got.Saved != 0 || got.Skipped != 2 || got.Progress.Saved.Label != "第3集" {
		t.Fatalf("no-change backfill %+v", got)
	}
	state, _ := s.store.read("alice")
	record := state.Jobs[j.ID]
	record.Request.DestinationID = "another"
	progress, err := s.store.followProgress("alice", record, p.share, now())
	if err != nil {
		t.Fatal(err)
	}
	if progress.ConfirmedFiles != 0 || progress.Saved.Kind != "unknown" {
		t.Fatalf("cross-destination progress %+v", progress)
	}
}
