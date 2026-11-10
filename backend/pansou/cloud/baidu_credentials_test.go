package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBaiduRotatedLoginSurvivesRestart(t *testing.T) {
	var calls atomic.Int32
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		cookie, err := r.Cookie("BDUSS")
		expected := "initial"
		if n > 1 {
			expected = fmt.Sprintf("renewed-%d", n-1)
		}
		if err != nil || cookie.Value != expected {
			t.Error("previous rotation was not used for the next check")
		}
		http.SetCookie(w, &http.Cookie{Name: "BDUSS", Value: fmt.Sprintf("renewed-%d", n), Path: "/"})
		fmt.Fprint(w, `{"errno":0,"result":{"uk":123,"bdstoken":"csrf"}}`)
	})
	dir := t.TempDir()
	s, err := NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.Connect(context.Background(), "alice", "", "baidu", "", Credentials{"cookie": "BDUSS=initial; STOKEN=stable"})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	checked, err := s.CheckAccount(context.Background(), "alice", account.ID)
	if err != nil || checked.Status != "ready" {
		s.Close()
		t.Fatalf("existing account rotation failed: %v", err)
	}
	s.Close()
	s, err = NewService(dir, []Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _, credentials, err := s.account("alice", account.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := &http.Request{Header: http.Header{"Cookie": {credentials["cookie"]}}}
	cookie, err := r.Cookie("BDUSS")
	if err != nil || cookie.Value != "renewed-2" {
		t.Fatal("rotated login cookie was lost after restart")
	}
	checked, err = s.CheckAccount(context.Background(), "alice", account.ID)
	if err != nil || checked.Status != "ready" || calls.Load() != 3 {
		t.Fatalf("restart login check failed: %v", err)
	}
}

func TestBaiduCookieCommitFailureStopsFurtherRequests(t *testing.T) {
	var lists atomic.Int32
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gettemplatevariable" {
			lists.Add(1)
		}
		http.SetCookie(w, &http.Cookie{Name: "STOKEN", Value: "new", Path: "/"})
		fmt.Fprint(w, `{"errno":0,"result":{"uk":123,"bdstoken":"csrf"}}`)
	})
	c := Credentials{"cookie": "BDUSS=old; STOKEN=old"}
	want := errors.New("storage unavailable")
	ctx := withCredentialCommit(context.Background(), func(Credentials) error { return want })
	_, err := p.ListFolders(ctx, c, "/")
	if !errors.Is(err, want) || lists.Load() != 0 || c["cookie"] != "BDUSS=old; STOKEN=old" {
		t.Fatal("failed credential persistence did not stop the operation")
	}
}

func TestBaiduExpiredResponseDoesNotEraseRetainedCredentials(t *testing.T) {
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "BDUSS", Path: "/", MaxAge: -1})
		fmt.Fprint(w, `{"errno":-6}`)
	})
	commits := 0
	ctx := withCredentialCommit(context.Background(), func(Credentials) error { commits++; return nil })
	c := Credentials{"cookie": "BDUSS=retained"}
	_, err := p.Validate(ctx, c)
	if !errors.Is(err, ErrAuthExpired) || commits != 0 || c["cookie"] != "BDUSS=retained" {
		t.Fatal("expired response overwrote retained login")
	}
}

func TestBaiduBusinessResponsePersistsAccountCookiesOnly(t *testing.T) {
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/gettemplatevariable" {
			fmt.Fprint(w, `{"errno":0,"result":{"uk":123,"bdstoken":"csrf"}}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "STOKEN", Value: "renewed", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "BDCLND", Value: "share-password", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "csrfToken", Value: "scoped", Path: "/api"})
		fmt.Fprint(w, `{"errno":-32}`)
	})
	var committed Credentials
	c := Credentials{"cookie": "BDUSS=retained; STOKEN=old; custom=keep"}
	ctx := withCredentialCommit(context.Background(), func(updated Credentials) error { committed = updated; return nil })
	_, err := p.ListFolders(ctx, c, "/")
	if err == nil || !strings.Contains(committed["cookie"], "STOKEN=renewed") ||
		!strings.Contains(c["cookie"], "custom=keep") || strings.Contains(committed["cookie"], "share-password") ||
		strings.Contains(committed["cookie"], "scoped") || committed["cookie"] != c["cookie"] {
		t.Fatal("business response failed to persist only the account cookie update")
	}
}

func TestBaiduDomainRotationReplacesImportedHostCookie(t *testing.T) {
	p := NewBaiduProvider().(*baiduProvider)
	s, err := p.session(Credentials{"cookie": "BDUSS=old"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://pan.baidu.com/api/gettemplatevariable")
	s.client.Jar.SetCookies(u, []*http.Cookie{{Name: "BDUSS", Value: "new", Domain: ".baidu.com", Path: "/"}})
	all := s.client.Jar.Cookies(u)
	if len(all) != 1 || all[0].Value != "new" {
		t.Fatal("domain rotation left a stale duplicate Cookie header")
	}
	s.client.Jar.SetCookies(u, []*http.Cookie{{Name: "BDUSS", Value: "foreign", Domain: "other.invalid", Path: "/"}})
	all = s.client.Jar.Cookies(u)
	if len(all) != 1 || all[0].Value != "new" {
		t.Fatal("foreign domain replaced account credentials")
	}
}

func TestBaiduHTMLLogoutDoesNotEraseRetainedCredentials(t *testing.T) {
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/gettemplatevariable" {
			fmt.Fprint(w, `{"errno":0,"result":{"uk":123,"bdstoken":"csrf"}}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "BDUSS", Path: "/", MaxAge: -1})
		fmt.Fprint(w, "<html>login required</html>")
	})
	commits := 0
	ctx := withCredentialCommit(context.Background(), func(Credentials) error { commits++; return nil })
	c := Credentials{"cookie": "BDUSS=retained"}
	s, err := p.authSession(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	var html string
	err = s.request(ctx, "GET", "/s/test", nil, nil, &html)
	if !errors.Is(err, ErrAuthExpired) || commits != 0 || c["cookie"] != "BDUSS=retained" {
		t.Fatal("HTML logout overwrote retained login")
	}
}
