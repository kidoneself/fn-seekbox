package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type quarkTestTransport func(*http.Request) (*http.Response, error)

func (fn quarkTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func quarkTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func quarkTestProvider(t *testing.T, fn func(*http.Request) (*http.Response, error)) *quarkProvider {
	t.Helper()
	p := NewQuarkProvider().(*quarkProvider)
	p.client.Transport = quarkTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || (r.URL.Host != "pan.quark.cn" && r.URL.Host != "drive-pc.quark.cn") {
			t.Fatalf("unexpected credential destination: %s", r.URL.Host)
		}
		if r.Header.Get("Cookie") != quarkTestCookie {
			t.Fatal("missing test Cookie")
		}
		if r.URL.Query().Get("pr") != "ucpro" || r.URL.Query().Get("fr") != "pc" {
			t.Fatal("missing common parameters")
		}
		return fn(r)
	})
	p.pollInterval = time.Millisecond
	p.taskTimeout = 100 * time.Millisecond
	return p
}

const quarkTestCookie = "test-cookie=unit-test; __uid=quark-user-id"

var quarkTestCredentials = Credentials{"cookie": quarkTestCookie}

func TestQuarkValidate(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		auth, fail bool
	}{
		{"valid", `{"success":true,"code":"OK","data":{"nickname":"我的夸克"}}`, false, false},
		{"website rejected success", `{"success":false,"code":"OK","data":{"nickname":"未授权"}}`, true, true},
		{"expired", `{"code":"NO_AUTH","message":"test-cookie=unit-test"}`, true, true},
		{"expired drive code", `{"code":31001}`, true, true},
		{"auth error", `{"code":"AUTH_ERROR_EXPIRED"}`, true, true},
		{"null account", `{"success":true,"code":"OK","data":null}`, true, true},
		{"missing account", `{"success":true,"code":"OK","data":{}}`, true, true},
		{"malformed", `{"success":true,"code":"OK","data":{"nickname":123}}`, false, true},
		{"missing code", `{"data":{"nickname":"x"}}`, false, true},
		{"untrusted message", `{"code":32123,"message":"test-cookie=unit-test; secret-token","data":{}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/1/clouddrive/member" {
					if tc.fail {
						t.Fatal("invalid website session reached member API")
					}
					return quarkTestResponse(`{"code":0,"data":{"total_capacity":1024,"use_capacity":1}}`), nil
				}
				if r.URL.Host != "pan.quark.cn" || r.URL.Path != "/account/info" {
					t.Fatal("wrong account endpoint")
				}
				return quarkTestResponse(tc.body), nil
			})
			identity, err := p.Validate(context.Background(), quarkTestCredentials)
			if (err != nil) != tc.fail || errors.Is(err, ErrAuthExpired) != tc.auth {
				t.Fatalf("identity=%+v error=%v", identity, err)
			}
			if !tc.fail && (identity.Name != "我的夸克" || identity.ID != "quark-user-id") {
				t.Fatalf("wrong identity: %+v", identity)
			}
			if err != nil && strings.Contains(err.Error(), "test-cookie") {
				t.Fatal("credentials leaked in error")
			}
		})
	}
}

func TestQuarkRedirectAndNetworkErrorsDoNotLeakCredentials(t *testing.T) {
	calls := 0
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://untrusted.example/?secret-token"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	_, err := p.Validate(context.Background(), quarkTestCredentials)
	if err == nil || calls != 1 || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("redirect not blocked: calls=%d err=%v", calls, err)
	}
	p = quarkTestProvider(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("secret-token test-cookie=unit-test")
	})
	_, err = p.Validate(context.Background(), quarkTestCredentials)
	if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "test-cookie") {
		t.Fatalf("unsafe network error: %v", err)
	}
}

func TestQuarkReadShareRecursesAndPaginates(t *testing.T) {
	pages := 0
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/1/clouddrive/share/sharepage/token":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["pwd_id"] != "abc123" || body["passcode"] != "abcd" {
				t.Fatalf("wrong token parameters: %#v", body)
			}
			return quarkTestResponse(`{"code":0,"data":{"stoken":"share-secret"}}`), nil
		case "/1/clouddrive/share/sharepage/detail":
			if r.URL.Query().Get("stoken") != "share-secret" {
				t.Fatal("missing share token")
			}
			parent, page := r.URL.Query().Get("pdir_fid"), r.URL.Query().Get("_page")
			pages++
			if parent == "0" && page == "1" {
				return quarkTestResponse(`{"code":0,"metadata":{"_total":2},"data":{"list":[{"fid":"folder","file_name":"剧集","dir":true}]}}`), nil
			}
			if parent == "0" && page == "2" {
				return quarkTestResponse(`{"code":0,"metadata":{"_total":2},"data":{"list":[{"fid":"readme","file_name":"说明.txt","dir":false,"size":20,"share_fid_token":"readme-token"}]}}`), nil
			}
			if parent == "folder" && page == "1" {
				return quarkTestResponse(`{"code":0,"metadata":{"_total":1},"data":{"list":[{"fid":"episode","file_name":"01.mp4","dir":false,"size":1024,"updated_at":123,"share_fid_token":"file-token"}]}}`), nil
			}
		}
		t.Fatalf("unexpected call %s parent=%s page=%s", r.URL.Path, r.URL.Query().Get("pdir_fid"), r.URL.Query().Get("_page"))
		return nil, nil
	})
	share, err := p.ReadShare(context.Background(), quarkTestCredentials, ShareLink{URL: "https://pan.quark.cn/s/abc123?pwd=abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if share.ID != "abc123" || len(share.Entries) != 3 || pages != 3 {
		t.Fatalf("incomplete result: %+v pages=%d", share, pages)
	}
	entry := share.Entries[1]
	if entry.Path != "剧集/01.mp4" || entry.ParentID != "folder" || entry.Token != "file-token" || entry.Size != 1024 || entry.Revision == "" {
		t.Fatalf("wrong nested entry: %+v", entry)
	}
	fileA := quarkFile{ID: "episode", Name: "01.mp4", Size: 1024, Token: "rotating-token", UpdatedAt: json.RawMessage(`123`)}
	if fileA.revision() != entry.Revision {
		t.Fatal("rotating token changed revision")
	}
	fileA.Size++
	if fileA.revision() == entry.Revision {
		t.Fatal("content change did not change revision")
	}
}

func TestQuarkReadShareRejectsUntrustedURLs(t *testing.T) {
	p := quarkTestProvider(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid URL triggered network call")
		return nil, nil
	})
	for _, raw := range []string{"http://pan.quark.cn/s/abc", "https://evil.example/s/abc", "https://pan.quark.cn.evil.example/s/abc", "https://user:pass@pan.quark.cn/s/abc", "https://pan.quark.cn:123/s/abc", "https://pan.quark.cn/s/../../account"} {
		if _, err := p.ReadShare(context.Background(), quarkTestCredentials, ShareLink{URL: raw}); err == nil {
			t.Fatalf("accepted invalid URL %s", raw)
		}
	}
}

func TestQuarkReadShareFailsWithoutPartialResult(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"missing token", `{"code":0,"data":{"list":[{"fid":"a","file_name":"a.mp4","dir":false}]}}`},
		{"unsafe path", `{"code":0,"data":{"list":[{"fid":"a","file_name":"../escape","dir":false,"share_fid_token":"x"}]}}`},
		{"oversized share", `{"code":0,"metadata":{"_total":10001},"data":{"list":[]}}`},
		{"incomplete page", `{"code":0,"metadata":{"_total":1},"data":{"list":[]}}`},
		{"missing list", `{"code":0,"data":{}}`},
		{"duplicate ID", `{"code":0,"data":{"list":[{"fid":"a","file_name":"a","dir":false,"share_fid_token":"x"},{"fid":"a","file_name":"b","dir":false,"share_fid_token":"y"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/token") {
					return quarkTestResponse(`{"code":0,"data":{"stoken":"share-secret"}}`), nil
				}
				return quarkTestResponse(tc.body), nil
			})
			share, err := p.ReadShare(context.Background(), quarkTestCredentials, ShareLink{URL: "https://pan.quark.cn/s/abc123"})
			if err == nil || len(share.Entries) != 0 || share.ID != "" {
				t.Fatalf("partial share escaped: %+v err=%v", share, err)
			}
		})
	}
}

func TestQuarkCreateFolderReusesExisting(t *testing.T) {
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatal("created duplicate folder")
		}
		return quarkTestResponse(`{"code":0,"data":{"list":[{"fid":"already","file_name":"追更","dir":true}]}}`), nil
	})
	folder, err := p.CreateFolder(context.Background(), quarkTestCredentials, "0", "追更")
	if err != nil || folder.ID != "already" {
		t.Fatalf("folder=%+v err=%v", folder, err)
	}
}

func TestQuarkCreateFolder(t *testing.T) {
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return quarkTestResponse(`{"code":0,"data":{"list":[]}}`), nil
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/1/clouddrive/file" || body["file_name"] != "剧集" || body["pdir_fid"] != "parent" {
			t.Fatalf("invalid create request: %#v", body)
		}
		return quarkTestResponse(`{"code":0,"data":{"fid":"new-folder"}}`), nil
	})
	folder, err := p.CreateFolder(context.Background(), quarkTestCredentials, "parent", "剧集")
	if err != nil || folder.ID != "new-folder" {
		t.Fatalf("folder=%+v err=%v", folder, err)
	}
}

func quarkTestShareAndEntry() (Share, Entry) {
	return Share{ID: "abc123", Data: map[string]string{"stoken": "share-secret"}}, Entry{ID: "file1", Name: "01.mp4", Token: "file-token", ParentID: "folder1"}
}

func TestQuarkSaveRequiresFinalSuccess(t *testing.T) {
	polls := 0
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/1/clouddrive/file/sort":
			return quarkTestResponse(`{"code":0,"data":{"list":[]}}`), nil
		case "/1/clouddrive/share/sharepage/save":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["to_pdir_fid"] != "target" || body["pdir_fid"] != "folder1" || body["pdir_save_all"] != false || body["stoken"] != "share-secret" {
				t.Fatalf("wrong transfer request: %#v", body)
			}
			if len(body["fid_list"].([]any)) != 1 || body["fid_list"].([]any)[0] != "file1" || body["fid_token_list"].([]any)[0] != "file-token" {
				t.Fatal("wrong file selection")
			}
			return quarkTestResponse(`{"code":0,"data":{"task_id":"task1"}}`), nil
		case "/1/clouddrive/task":
			polls++
			if polls == 1 {
				return quarkTestResponse(`{"code":0,"data":{"status":1}}`), nil
			}
			return quarkTestResponse(`{"code":0,"data":{"status":2}}`), nil
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
		return nil, nil
	})
	share, entry := quarkTestShareAndEntry()
	if err := p.Save(context.Background(), quarkTestCredentials, share, entry, "target"); err != nil {
		t.Fatal(err)
	}
	if polls != 2 {
		t.Fatalf("task was not confirmed: %d polls", polls)
	}
}

func TestQuarkSaveFailures(t *testing.T) {
	for _, tc := range []struct{ name, state string }{
		{"failed", `{"status":3,"fail_code":32003,"fail_msg":"share-secret"}`},
		{"paused", `{"status":4}`},
		{"unknown", `{"status":9}`},
		{"missing status", `{}`},
		{"timeout", `{"status":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/sort") {
					return quarkTestResponse(`{"code":0,"data":{"list":[]}}`), nil
				}
				if strings.HasSuffix(r.URL.Path, "/save") {
					return quarkTestResponse(`{"code":0,"data":{"task_id":"task1"}}`), nil
				}
				return quarkTestResponse(fmt.Sprintf(`{"code":0,"data":%s}`, tc.state)), nil
			})
			p.taskTimeout = 5 * time.Millisecond
			share, entry := quarkTestShareAndEntry()
			err := p.Save(context.Background(), quarkTestCredentials, share, entry, "target")
			if err == nil || strings.Contains(err.Error(), "share-secret") {
				t.Fatalf("invalid error: %v", err)
			}
			if tc.name == "failed" && (!strings.Contains(err.Error(), "32003") || strings.Contains(err.Error(), "空间不足")) {
				t.Fatalf("task failure misdiagnosed as a capacity problem: %v", err)
			}
		})
	}
}

func TestQuarkSaveDoesNotOverwriteExistingName(t *testing.T) {
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/sort") {
			t.Fatal("write attempted despite name collision")
		}
		return quarkTestResponse(`{"code":0,"data":{"list":[{"fid":"existing","file_name":"01.mp4","dir":false}]}}`), nil
	})
	share, entry := quarkTestShareAndEntry()
	if err := p.Save(context.Background(), quarkTestCredentials, share, entry, "target"); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("wrong conflict result: %v", err)
	}
}

func TestQuarkValidateRejectsWebsiteOnlySession(t *testing.T) {
	calls := 0
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path == "/account/info" {
			return quarkTestResponse(`{"success":true,"code":"OK","data":{"nickname":"网页登录有效"}}`), nil
		}
		if r.URL.Path != "/1/clouddrive/member" {
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return quarkTestResponse(`{"code":31001,"status":401}`), nil
	})
	identity, err := p.Validate(context.Background(), quarkTestCredentials)
	if !errors.Is(err, ErrAuthExpired) || identity.Name != "" || calls != 2 {
		t.Fatalf("website-only session was accepted: identity=%+v calls=%d error=%v", identity, calls, err)
	}
}

func TestQuarkStableAccountIdentity(t *testing.T) {
	for _, raw := range []string{"a=b", "__uid=0", "__uid=000", "__uid=first; __uid=second", "__uid=", "__uid=" + strings.Repeat("x", 129)} {
		if id, err := quarkAccountID(raw); err == nil || id != "" {
			t.Fatalf("accepted ambiguous or missing account identity: %q", raw)
		}
	}
	if id, err := quarkAccountID("x=y; __uid=opaque+account/id="); err != nil || id != "opaque+account/id=" {
		t.Fatalf("changed provider-issued opaque identity: %q %v", id, err)
	}
}
