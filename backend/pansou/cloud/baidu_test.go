package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func bxJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func baiduTestProvider(t *testing.T, handler http.HandlerFunc) *baiduProvider {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return &baiduProvider{client: bxClient(), baseURL: s.URL}
}

func TestBaiduRecursiveShareAndConfirmedSave(t *testing.T) {
	var pages, saves int
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("BDUSS"); err != nil || cookie.Value != "owner-test-cookie" {
			t.Error("missing account cookie")
		}
		switch r.URL.Path {
		case "/api/gettemplatevariable":
			fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"test-csrf","uk":9007199254740999}}`)
		case "/s/1test":
			fmt.Fprint(w, `<script>locals.mset({"shareid":9007199254740997,"share_uk":9007199254740998});</script>`)
		case "/share/verify":
			_ = r.ParseForm()
			if r.PostForm.Get("pwd") != "abcd" {
				t.Error("missing extraction code")
			}
			fmt.Fprint(w, `{"errno":0,"randsk":"test-share-session"}`)
		case "/share/list":
			if cookie, err := r.Cookie("BDCLND"); err != nil || cookie.Value != "test-share-session" {
				t.Error("lost share verification cookie")
			}
			pages++
			if r.URL.Query().Get("root") == "1" {
				fmt.Fprint(w, `{"errno":0,"has_more":0,"list":[{"fs_id":9007199254740993,"server_filename":"剧集","path":"/原始/剧集","isdir":1}]}`)
			} else if r.URL.Query().Get("page") == "1" {
				if r.URL.Query().Get("dir") != "/原始/剧集" {
					t.Error("wrong directory")
				}
				fmt.Fprint(w, `{"errno":0,"has_more":1,"list":[{"fs_id":9007199254740994,"server_filename":"第1集.mkv","isdir":0,"size":42,"md5":"abc","server_mtime":123}]}`)
			} else {
				fmt.Fprint(w, `{"errno":0,"has_more":0,"list":[{"fs_id":"9007199254740995","server_filename":"第2集.mkv","isdir":0,"size":43,"md5":"def","server_mtime":124}]}`)
			}
		case "/api/list":
			fmt.Fprint(w, `{"errno":0,"list":[]}`)
		case "/share/transfer":
			saves++
			_ = r.ParseForm()
			if r.PostForm.Get("fsidlist") != "[9007199254740994]" || r.PostForm.Get("path") != "/追更/剧集" {
				t.Error("wrong save data")
			}
			if r.URL.Query().Get("ondup") != "newcopy" || r.URL.Query().Get("from") != "9007199254740998" {
				t.Error("wrong save owner or conflict mode")
			}
			fmt.Fprint(w, `{"errno":0,"extra":{"list":[{"from_fs_id":9007199254740994,"to_fs_id":9007199254741001}]}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	c := Credentials{"cookie": "BDUSS=owner-test-cookie; STOKEN=owner-test-stoken"}
	share, err := p.ReadShare(context.Background(), c, ShareLink{URL: "https://pan.baidu.com/s/1test?pwd=abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if share.ID != "9007199254740997" || len(share.Entries) != 3 || pages != 3 {
		t.Fatalf("bad recursive result: %#v, pages=%d", share, pages)
	}
	if share.Entries[1].Path != "剧集/第1集.mkv" || share.Entries[1].ID != "9007199254740994" || share.Entries[1].Revision == "" {
		t.Fatalf("bad entry %#v", share.Entries[1])
	}
	if err := p.Save(context.Background(), c, share, share.Entries[1], "/追更/剧集"); err != nil {
		t.Fatal(err)
	}
	if saves != 1 {
		t.Fatal("save was not performed")
	}
}

func TestBaiduRejectsAmbiguousSuccessAndConflict(t *testing.T) {
	for _, test := range []struct {
		name, list, result string
		expected           error
		saves              int
	}{
		{"accepted only", `[]`, `{"errno":0,"taskid":123}`, nil, 1},
		{"different file", `[]`, `{"errno":0,"extra":{"list":[{"from_fs_id":12,"to_fs_id":999}]}}`, nil, 1},
		{"per-file failure despite success envelope", `[]`, `{"errno":0,"info":[{"errno":-32}],"extra":{"list":[{"from_fs_id":11,"to_fs_id":999}]}}`, nil, 1},
		{"existing destination", `[{"fs_id":100,"server_filename":"one.mkv"}]`, ``, ErrDestinationExists, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			saves := 0
			p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/gettemplatevariable":
					fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"csrf","uk":1}}`)
				case "/api/list":
					fmt.Fprintf(w, `{"errno":0,"list":%s}`, test.list)
				case "/share/transfer":
					saves++
					fmt.Fprint(w, test.result)
				default:
					t.Fatal("unexpected request")
				}
			})
			err := p.Save(context.Background(), Credentials{"cookie": "BDUSS=test"}, Share{ID: "2", Data: map[string]string{"owner": "3"}}, Entry{ID: "11", Name: "one.mkv"}, "/")
			if err == nil {
				t.Fatal("unconfirmed transfer reported success")
			}
			if test.expected != nil && !errors.Is(err, test.expected) {
				t.Fatal(err)
			}
			if saves != test.saves {
				t.Fatalf("saves=%d", saves)
			}
		})
	}
}

func TestBaiduAPIMetadataSeKeyAndAsyncConfirmation(t *testing.T) {
	listing := 0
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/gettemplatevariable":
			fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"csrf","uk":123}}`)
		case "/share/verify":
			_, _ = w.Write([]byte(`{"errno":0,"randsk":"encoded%2Bkey%2Fvalue"}`))
		case "/share/list":
			cookie, err := r.Cookie("BDCLND")
			if err != nil || cookie.Value != url.QueryEscape("encoded+key/value") {
				t.Error("share cookie escaping changed")
			}
			fmt.Fprint(w, `{"errno":0,"share_id":"222","uk":"333","list":[{"fs_id":"11","server_filename":"one.mkv","size":"42","isdir":"0","server_mtime":"123","md5":"expected-hash"}]}`)
		case "/share/transfer":
			if r.URL.Query().Get("sekey") != "encoded+key/value" || r.URL.Query().Get("shareid") != "222" || r.URL.Query().Get("from") != "333" {
				t.Error("missing API share metadata/sekey")
			}
			fmt.Fprint(w, `{"errno":0,"taskid":55,"info":[{"errno":0}]}`)
		case "/api/list":
			listing++
			switch listing {
			case 1:
				fmt.Fprint(w, `{"errno":0,"list":[]}`)
			case 2:
				fmt.Fprint(w, `{"errno":0,"list":[{"fs_id":99,"server_filename":"unrelated.mkv","size":42}]}`)
			case 3:
				fmt.Fprint(w, `{"errno":0,"list":[{"fs_id":100,"server_filename":"one.mkv","size":40,"md5":"expected-hash"}]}`)
			default:
				fmt.Fprint(w, `{"errno":0,"list":[{"fs_id":100,"server_filename":"one.mkv","size":42,"md5":"expected-hash"}]}`)
			}
		default:
			t.Errorf("API metadata should not need HTML: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	c := Credentials{"cookie": "BDUSS=test"}
	identity, err := p.Validate(context.Background(), c)
	if err != nil || identity.ID != "123" {
		t.Fatalf("wrong identity %#v %v", identity, err)
	}
	share, err := p.ReadShare(context.Background(), c, ShareLink{URL: "https://pan.baidu.com/s/1test", Password: "abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save(context.Background(), c, share, share.Entries[0], "/"); err != nil {
		t.Fatal(err)
	}
	if listing != 4 {
		t.Fatalf("accepted unrelated or incomplete file: listings=%d", listing)
	}
}

func TestBaiduAuthErrorsAndUntrustedShareURLs(t *testing.T) {
	for _, response := range []string{`{"errno":-6}`, `{"errno":0,"result":{"uk":12}}`} {
		p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) })
		_, err := p.Validate(context.Background(), Credentials{"cookie": "BDUSS=test"})
		if !errors.Is(err, ErrAuthExpired) {
			t.Fatalf("expected expired credential, got %v", err)
		}
	}
	p := NewBaiduProvider()
	for _, address := range []string{"https://pan.baidu.com.attacker.invalid/s/1abc", "http://127.0.0.1/s/1abc", "https://pan.baidu.com/s/1../../secret"} {
		if _, err := p.ReadShare(context.Background(), Credentials{}, ShareLink{URL: address}); err == nil {
			t.Fatal("untrusted URL accepted")
		}
	}
}

func TestProviderRedirectNeverLeaksCredentials(t *testing.T) {
	hits := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer destination.Close()
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) })
	_, err := p.Validate(context.Background(), Credentials{"cookie": "BDUSS=private-test-marker"})
	if err == nil || hits != 0 || strings.Contains(err.Error(), "private-test-marker") {
		t.Fatalf("unsafe redirect: hits=%d err=%v", hits, err)
	}
}
