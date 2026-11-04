package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func xunleiTestCredentials() Credentials {
	return Credentials{"access_token": "test-owner-access", "captcha_token": "test-owner-captcha", "device_id": "test-owner-device", "client_id": "test-owner-client"}
}

func xunleiTestProvider(t *testing.T, handler http.HandlerFunc) *xunleiProvider {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return &xunleiProvider{client: bxClient(), baseURL: s.URL, userURL: s.URL}
}

func TestXunleiRecursiveShareAndFinalTaskState(t *testing.T) {
	var tasks, details int
	p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-owner-access" || r.Header.Get("X-Captcha-Token") != "test-owner-captcha" {
			t.Error("missing owner headers")
		}
		switch r.URL.Path {
		case "/share":
			fmt.Fprint(w, `{"share_status":"OK","pass_code_token":"share-test-token","files":[{"id":"dir","name":"剧集","kind":"drive#folder","revision":"2"}]}`)
		case "/share/detail":
			details++
			if r.Method != "GET" || r.URL.Query().Get("parent_id") != "dir" || r.URL.Query().Get("pass_code_token") != "share-test-token" {
				t.Error("wrong official share detail protocol")
			}
			if r.URL.Query().Get("page_token") == "" {
				fmt.Fprint(w, `{"file_infos":[{"id":"one","name":"第1集.mkv","kind":"drive#file","size":"42","revision":"7","phase":"PHASE_TYPE_COMPLETE"}],"next_page_token":"next"}`)
			} else {
				fmt.Fprint(w, `{"files":[{"id":"two","name":"第2集.mkv","kind":"drive#file","size":43,"revision":8}],"next_page_token":""}`)
			}
		case "/files":
			fmt.Fprint(w, `{"files":[]}`)
		case "/share/restore":
			var data struct {
				ParentID  string   `json:"parent_id"`
				FileIDs   []string `json:"file_ids"`
				Ancestors []string `json:"ancestor_ids"`
				Token     string   `json:"pass_code_token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				t.Error(err)
			}
			if data.ParentID != "dest" || len(data.FileIDs) != 1 || data.FileIDs[0] != "one" || len(data.Ancestors) != 1 || data.Ancestors[0] != "dir" || data.Token != "share-test-token" {
				t.Errorf("wrong restore payload %#v", data)
			}
			fmt.Fprint(w, `{"share_status":"OK","restore_task_id":"task"}`)
		case "/tasks/task":
			tasks++
			if tasks == 1 {
				fmt.Fprint(w, `{"phase":"PHASE_TYPE_RUNNING","progress":100}`)
			} else {
				fmt.Fprint(w, `{"phase":"PHASE_TYPE_COMPLETE","progress":100,"params":{"partial_failed":"false"}}`)
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	share, err := p.ReadShare(context.Background(), xunleiTestCredentials(), ShareLink{URL: "https://pan.xunlei.com/s/share?pwd=abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if len(share.Entries) != 3 || share.Entries[1].Path != "剧集/第1集.mkv" || details != 2 {
		t.Fatalf("wrong share %#v", share)
	}
	if err := p.Save(context.Background(), xunleiTestCredentials(), share, share.Entries[1], "dest"); err != nil {
		t.Fatal(err)
	}
	if tasks != 2 {
		t.Fatal("progress 100 incorrectly accepted without terminal success")
	}
}

func TestXunleiRejectsPartialFailedAndMissingTasks(t *testing.T) {
	for _, response := range []string{`{"phase":"PHASE_TYPE_COMPLETE","params":{"partial_failed":true}}`, `{"phase":"PHASE_TYPE_COMPLETE","params":{"partial_failed":"true"}}`, `{"phase":"PHASE_TYPE_ERROR","progress":100}`, `{"phase":"PHASE_TYPE_COMPLETE","params":{"error_code":"5"}}`} {
		p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/files":
				fmt.Fprint(w, `{"files":[]}`)
			case "/share/restore":
				fmt.Fprint(w, `{"task_id":"task"}`)
			case "/tasks/task":
				fmt.Fprint(w, response)
			default:
				t.Error("unexpected request")
			}
		})
		if err := p.Save(context.Background(), xunleiTestCredentials(), Share{ID: "share", Data: map[string]string{}}, Entry{ID: "one", Name: "one.mkv"}, ""); err == nil {
			t.Fatal("failed task reported success")
		}
	}
}

func TestXunleiPaginationCycleAndCredentialExpiry(t *testing.T) {
	p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"files":[],"next_page_token":"again"}`) })
	if _, err := p.ListFolders(context.Background(), xunleiTestCredentials(), ""); err == nil {
		t.Fatal("pagination cycle accepted")
	}
	p = xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":"captcha_expired","error_code":9,"error_description":"test-owner-access must never be exposed"}`)
	})
	_, err := p.Validate(context.Background(), xunleiTestCredentials())
	if !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("expected auth expiration, got %v", err)
	}
}

func TestXunleiConflictPreventsRestore(t *testing.T) {
	p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files" {
			t.Error("conflicting save made a write request")
		}
		fmt.Fprint(w, `{"files":[{"id":"existing","name":"one.mkv","kind":"drive#file"}]}`)
	})
	err := p.Save(context.Background(), xunleiTestCredentials(), Share{ID: "share"}, Entry{ID: "one", Name: "one.mkv"}, "")
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatal(err)
	}
}

func xunleiBundleCredentials(t *testing.T, expired bool, meta map[string]string) Credentials {
	t.Helper()
	expiry := time.Now().Add(time.Hour)
	if expired {
		expiry = time.Now().Add(-time.Hour)
	}
	b, err := json.Marshal(xunleiWebSession{AccessToken: "owner-old-access", RefreshToken: "owner-old-refresh", ExpiresAt: expiry, Headers: map[string]string{"x-device-id": "owner-device", "x-client-id": "owner-client", "x-captcha-token": "owner-old-captcha", "x-device-sign": "owner-device-sign"}, CaptchaMeta: meta})
	if err != nil {
		t.Fatal(err)
	}
	return Credentials{"web_session": string(b)}
}

func TestXunleiWebSessionRefreshesAndPersistsBeforeUse(t *testing.T) {
	var auth, captcha, files, commits int
	p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/token":
			auth++
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["refresh_token"] != "owner-old-refresh" || body["client_id"] != "owner-client" || body["grant_type"] != "refresh_token" || body["client_secret"] != "" || r.Header.Get("Authorization") != "" {
				t.Error("wrong owner refresh request")
			}
			fmt.Fprint(w, `{"access_token":"owner-new-access","refresh_token":"owner-new-refresh","expires_in":7200}`)
		case "/files":
			files++
			if commits < 1 || r.Header.Get("Authorization") != "Bearer owner-new-access" || r.Header.Get("x-device-sign") != "owner-device-sign" {
				t.Error("refreshed token used before save or owner context lost")
			}
			if r.Header.Get("x-captcha-token") == "owner-old-captcha" {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":"captcha_invalid","error_code":9}`)
			} else {
				if commits != 2 {
					t.Error("captcha used before encrypted save")
				}
				fmt.Fprint(w, `{"files":[]}`)
			}
		case "/shield/captcha/init":
			captcha++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["action"] != "GET:/drive/v1/files" || body["device_id"] != "owner-device" {
				t.Error("wrong captcha refresh action")
			}
			fmt.Fprint(w, `{"captcha_token":"owner-new-captcha"}`)
		default:
			t.Errorf("unexpected refresh request %s", r.URL.Path)
		}
	})
	c := xunleiBundleCredentials(t, true, map[string]string{"captcha_sign": "owner-captured-sign", "timestamp": "123"})
	ctx := withCredentialCommit(context.Background(), func(next Credentials) error {
		commits++
		var saved xunleiWebSession
		if err := json.Unmarshal([]byte(next["web_session"]), &saved); err != nil {
			t.Fatal(err)
		}
		if saved.RefreshToken != "owner-new-refresh" {
			t.Error("rotated refresh token lost")
		}
		return nil
	})
	if _, err := p.ListFolders(ctx, c, ""); err != nil {
		t.Fatal(err)
	}
	if auth != 1 || captcha != 1 || files != 2 || commits != 2 {
		t.Fatalf("wrong bounded refresh counts %d/%d/%d/%d", auth, captcha, files, commits)
	}
	if !strings.Contains(c["web_session"], "owner-new-refresh") || !strings.Contains(c["web_session"], "owner-new-captcha") {
		t.Fatal("active credential map was not refreshed")
	}
}

func TestXunleiRefreshPersistenceFailureStopsRequests(t *testing.T) {
	for _, hasCallback := range []bool{false, true} {
		apiCalls := 0
		p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/auth/token" {
				apiCalls++
			}
			fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":7200}`)
		})
		c := xunleiBundleCredentials(t, true, nil)
		original := c["web_session"]
		ctx := context.Background()
		if hasCallback {
			ctx = withCredentialCommit(ctx, func(Credentials) error { return errors.New("storage failure") })
		}
		if _, err := p.ListFolders(ctx, c, ""); err == nil {
			t.Fatal("continued after unpersisted refresh")
		}
		if apiCalls != 0 || c["web_session"] != original {
			t.Fatal("unpersisted credentials used or replaced")
		}
	}
}

func TestXunleiAuthenticationRefreshIsBounded(t *testing.T) {
	apiCalls, refreshCalls := 0, 0
	p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/token" {
			refreshCalls++
			fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":7200}`)
			return
		}
		apiCalls++
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"unauthenticated"}`)
	})
	ctx := withCredentialCommit(context.Background(), func(Credentials) error { return nil })
	_, err := p.ListFolders(ctx, xunleiBundleCredentials(t, false, nil), "")
	if !errors.Is(err, ErrAuthExpired) || apiCalls != 2 || refreshCalls != 1 {
		t.Fatalf("unbounded refresh: %d/%d %v", apiCalls, refreshCalls, err)
	}
}

func TestXunleiCaptchaChallengeRequiresOwner(t *testing.T) {
	for _, meta := range []map[string]string{nil, {"captcha_sign": "owner-sign"}} {
		calls, commits := 0, 0
		p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path == "/shield/captcha/init" {
				fmt.Fprint(w, `{"url":"https://verification.example/owner-step","captcha_token":"not-usable-before-verification"}`)
				return
			}
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"captcha_invalid","error_code":9}`)
		})
		ctx := withCredentialCommit(context.Background(), func(Credentials) error { commits++; return nil })
		_, err := p.ListFolders(ctx, xunleiBundleCredentials(t, false, meta), "")
		if !errors.Is(err, ErrAuthExpired) || commits != 0 || calls > 2 {
			t.Fatalf("challenge mishandled calls=%d commits=%d err=%v", calls, commits, err)
		}
	}
}

type xunleiFailingTransport struct{ calls int }

func (r *xunleiFailingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, errors.New("connection dropped after write")
}

func TestXunleiNeverRetriesAmbiguousWriteNetworkFailure(t *testing.T) {
	transport := &xunleiFailingTransport{}
	p := NewXunleiProvider().(*xunleiProvider)
	p.client.Transport = transport
	var out map[string]any
	err := p.request(context.Background(), xunleiBundleCredentials(t, false, map[string]string{"captcha_sign": "owner-sign"}), "POST", "/share/restore", nil, map[string]string{"share_id": "share"}, &out)
	if err == nil || transport.calls != 1 {
		t.Fatalf("ambiguous write retried: calls=%d err=%v", transport.calls, err)
	}
}
