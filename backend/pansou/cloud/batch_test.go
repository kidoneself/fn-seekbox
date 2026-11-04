package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Exercise the real worker + Baidu HTTP adapter, not a loop that mirrors them.
func TestBaiduWorkerSaves37FilesWithOneSubmission(t *testing.T) {
	var logins, listings, submissions atomic.Int32
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/gettemplatevariable":
			logins.Add(1)
			fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"csrf","uk":1}}`)
		case "/share/list":
			files := []map[string]any{}
			for i := 1; i <= 37; i++ {
				files = append(files, map[string]any{"fs_id": i, "server_filename": fmt.Sprintf("%02d.mkv", i), "size": 42, "isdir": 0})
			}
			bxJSON(t, w, map[string]any{"errno": 0, "share_id": "100", "uk": "200", "list": files})
		case "/api/list":
			listings.Add(1)
			fmt.Fprint(w, `{"errno":0,"list":[]}`)
		case "/share/transfer":
			submissions.Add(1)
			_ = r.ParseForm()
			var ids []int
			if err := json.Unmarshal([]byte(r.PostForm.Get("fsidlist")), &ids); err != nil || len(ids) != 37 {
				t.Errorf("not one batch of 37: %v %v", ids, err)
			}
			list := []map[string]any{}
			for _, id := range ids {
				list = append(list, map[string]any{"from_fs_id": id, "to_fs_id": id + 1000})
			}
			bxJSON(t, w, map[string]any{"errno": 0, "extra": map[string]any{"list": list}})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	s, err := NewService(t.TempDir(), []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.Connect(context.Background(), "alice", "", "baidu", "", Credentials{"cookie": "BDUSS=test"})
	if err != nil {
		t.Fatal(err)
	}
	before := logins.Load()
	req := TransferRequest{AccountID: a.ID, ShareURL: "https://pan.baidu.com/s/1test", DestinationID: "/", DestinationName: "/", RequestID: "first"}
	j, err := s.Transfer("alice", req)
	if err != nil {
		t.Fatal(err)
	}
	got := serviceWaitJob(t, s, "alice", j.ID)
	if got.Status != "succeeded" || got.Saved != 37 || got.Total != 37 {
		t.Fatalf("bad job %+v", got)
	}
	if submissions.Load() != 1 || listings.Load() != 1 || logins.Load()-before != 1 {
		t.Fatalf("excess requests: submit=%d list=%d login=%d", submissions.Load(), listings.Load(), logins.Load()-before)
	}
	req.RequestID = "again"
	j, err = s.Transfer("alice", req)
	if err != nil {
		t.Fatal(err)
	}
	got = serviceWaitJob(t, s, "alice", j.ID)
	if got.Skipped != 37 || got.Saved != 0 || submissions.Load() != 1 {
		t.Fatalf("duplicate batch transfer %+v", got)
	}
	t.Log("37 files: 1 share listing + 1 authentication + 1 destination listing + 1 transfer; repeat saves 0")
}

func TestBatchProvidersSendSiblingIDsTogether(t *testing.T) {
	entries := []Entry{{ID: "one", Name: "one.mkv", Path: "folder/one.mkv", ParentID: "folder", Token: "token-one"}, {ID: "two", Name: "two.mkv", Path: "folder/two.mkv", ParentID: "folder", Token: "token-two"}}
	t.Run("quark", func(t *testing.T) {
		writes, lists, polls := 0, 0, 0
		p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/sort"):
				lists++
				return quarkTestResponse(`{"code":0,"data":{"list":[]}}`), nil
			case strings.HasSuffix(r.URL.Path, "/save"):
				writes++
				var body struct {
					IDs    []string `json:"fid_list"`
					Tokens []string `json:"fid_token_list"`
					Parent string   `json:"pdir_fid"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if !reflect.DeepEqual(body.IDs, []string{"one", "two"}) || !reflect.DeepEqual(body.Tokens, []string{"token-one", "token-two"}) || body.Parent != "folder" {
					t.Errorf("bad batch %+v", body)
				}
				return quarkTestResponse(`{"code":0,"data":{"task_id":"task"}}`), nil
			default:
				polls++
				return quarkTestResponse(`{"code":0,"data":{"status":2}}`), nil
			}
		})
		got, err := p.SaveBatch(context.Background(), quarkTestCredentials, Share{ID: "share", Data: map[string]string{"stoken": "token"}}, entries, "target")
		if err != nil || !got.Submitted || len(got.Confirmed) != 2 || writes != 1 || lists != 1 || polls != 1 {
			t.Fatalf("bad batch %+v %v counts %d %d %d", got, err, writes, lists, polls)
		}
	})
	t.Run("xunlei", func(t *testing.T) {
		entries := append([]Entry(nil), entries...)
		for i := range entries {
			entries[i].Token = `["folder"]`
		}
		writes, lists := 0, 0
		p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/files":
				lists++
				fmt.Fprint(w, `{"files":[]}`)
			case "/share/restore":
				writes++
				var body struct {
					IDs       []string `json:"file_ids"`
					Ancestors []string `json:"ancestor_ids"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if !reflect.DeepEqual(body.IDs, []string{"one", "two"}) || !reflect.DeepEqual(body.Ancestors, []string{"folder"}) {
					t.Errorf("bad batch %+v", body)
				}
				fmt.Fprint(w, `{"restore_task_id":"task"}`)
			case "/tasks/task":
				fmt.Fprint(w, `{"phase":"PHASE_TYPE_COMPLETE","params":{"partial_failed":false}}`)
			default:
				t.Errorf("unexpected %s", r.URL.Path)
			}
		})
		got, err := p.SaveBatch(context.Background(), xunleiTestCredentials(), Share{ID: "share"}, entries, "target")
		if err != nil || !got.Submitted || len(got.Confirmed) != 2 || writes != 1 || lists != 1 {
			t.Fatalf("bad batch %+v %v", got, err)
		}
	})
}

func TestBaiduBatchPartialConfirmationAndPreflightCollision(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(fmt.Sprint(collision), func(t *testing.T) {
			writes := 0
			p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/gettemplatevariable":
					fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"csrf","uk":1}}`)
				case "/api/list":
					if collision {
						fmt.Fprint(w, `{"errno":0,"list":[{"fs_id":999,"server_filename":"two.mkv"}]}`)
					} else {
						fmt.Fprint(w, `{"errno":0,"list":[]}`)
					}
				case "/share/transfer":
					writes++
					fmt.Fprint(w, `{"errno":0,"extra":{"list":[{"from_fs_id":11,"to_fs_id":101}]}}`)
				}
			})
			got, err := p.SaveBatch(context.Background(), Credentials{"cookie": "BDUSS=test"}, Share{ID: "share"}, []Entry{{ID: "11", Name: "one.mkv"}, {ID: "12", Name: "two.mkv"}}, "/")
			if err == nil {
				t.Fatal("incomplete batch reported success")
			}
			if collision {
				if writes != 0 || got.Submitted || !errors.Is(err, ErrDestinationExists) {
					t.Fatalf("preflight wrote %+v %v", got, err)
				}
			} else if writes != 1 || !got.Submitted || !reflect.DeepEqual(got.Confirmed, []string{"11"}) {
				t.Fatalf("lost partial confirmation %+v %v", got, err)
			}
		})
	}
}

type batchFakeProvider struct {
	*serviceFakeProvider
	batchMu sync.Mutex
	batches [][]Entry
	outcome func([]Entry) (BatchResult, error)
}

func (p *batchFakeProvider) SaveBatch(_ context.Context, _ Credentials, _ Share, entries []Entry, _ string) (BatchResult, error) {
	p.batchMu.Lock()
	defer p.batchMu.Unlock()
	if _, _, err := batchEntries(entries); err != nil {
		return BatchResult{}, err
	}
	p.batches = append(p.batches, append([]Entry(nil), entries...))
	if p.outcome != nil {
		return p.outcome(entries)
	}
	ids := []string{}
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return BatchResult{Submitted: true, Confirmed: ids}, nil
}
func (p *batchFakeProvider) batchSizes() []int {
	p.batchMu.Lock()
	defer p.batchMu.Unlock()
	r := []int{}
	for _, b := range p.batches {
		r = append(r, len(b))
	}
	return r
}
func batchService(t *testing.T, p *batchFakeProvider) (*Service, Account) {
	t.Helper()
	s, err := NewService(t.TempDir(), []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, serviceConnect(t, s, "alice", "cookie")
}
func TestBatchWorkerPartitionsDirectoriesAndSubscriptionOnlyAddsNew(t *testing.T) {
	p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider()}
	entries := []Entry{}
	for i := 0; i < 137; i++ {
		e := serviceFile(fmt.Sprint(i), fmt.Sprintf("%03d.mkv", i))
		e.Path = "season/" + e.Name
		entries = append(entries, e)
	}
	entries = append(entries, serviceFile("root", "root.mkv"))
	p.setEntries(entries...)
	s, a := batchService(t, p)
	sub, j, err := s.Subscribe("alice", serviceRequest(a, ""), 15)
	if err != nil {
		t.Fatal(err)
	}
	got := serviceWaitJob(t, s, "alice", j.ID)
	if got.Status != "succeeded" || got.Saved != 138 || !reflect.DeepEqual(p.batchSizes(), []int{1, 100, 37}) {
		t.Fatalf("wrong partition %+v %v", got, p.batchSizes())
	}
	for i := 0; i < 2; i++ {
		e := serviceFile(fmt.Sprintf("new%d", i), fmt.Sprintf("new%d.mkv", i))
		e.Path = "season/" + e.Name
		entries = append(entries, e)
	}
	p.setEntries(entries...)
	j, err = s.CheckSubscription("alice", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = serviceWaitJob(t, s, "alice", j.ID)
	if got.Status != "succeeded" || got.Saved != 2 || got.Skipped != 138 || !reflect.DeepEqual(p.batchSizes(), []int{1, 100, 37, 2}) {
		t.Fatalf("duplicate follow batch %+v %v", got, p.batchSizes())
	}
}
func TestBatchJournalKeepsUncertainMembersAcrossRestart(t *testing.T) {
	p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider(), outcome: func(e []Entry) (BatchResult, error) {
		return BatchResult{Submitted: true, Confirmed: []string{e[0].ID}}, errors.New("connection lost")
	}}
	p.setEntries(serviceFile("one", "one"), serviceFile("two", "two"))
	dir := t.TempDir()
	s, err := NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	a := serviceConnect(t, s, "alice", "cookie")
	j, err := s.Transfer("alice", serviceRequest(a, "first"))
	if err != nil {
		t.Fatal(err)
	}
	got := serviceWaitJob(t, s, "alice", j.ID)
	if got.Status != "partial" || got.Saved != 1 {
		t.Fatalf("bad partial %+v", got)
	}
	s.Close()
	s, err = NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err = s.Transfer("alice", serviceRequest(a, "retry"))
	if err != nil {
		t.Fatal(err)
	}
	got = serviceWaitJob(t, s, "alice", j.ID)
	if got.Status != "failed" || !strings.Contains(got.Error, "未确认") || len(p.batchSizes()) != 1 {
		t.Fatalf("uncertain batch resubmitted %+v", got)
	}
}
func TestBatchPreflightFailureCanRetryWithoutPendingMarks(t *testing.T) {
	p := &batchFakeProvider{serviceFakeProvider: newServiceFakeProvider(), outcome: func([]Entry) (BatchResult, error) { return BatchResult{}, ErrDestinationExists }}
	p.setEntries(serviceFile("one", "one"), serviceFile("two", "two"))
	s, a := batchService(t, p)
	j, _ := s.Transfer("alice", serviceRequest(a, "first"))
	serviceWaitJob(t, s, "alice", j.ID)
	p.batchMu.Lock()
	p.outcome = nil
	p.batchMu.Unlock()
	j, _ = s.Transfer("alice", serviceRequest(a, "retry"))
	got := serviceWaitJob(t, s, "alice", j.ID)
	if got.Status != "succeeded" || got.Saved != 2 {
		t.Fatalf("preflight poisoned journal %+v", got)
	}
}
