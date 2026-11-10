package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"pansou/cloud"
)

type cloudAPIFakeProvider struct {
	validations atomic.Int64
	reads       atomic.Int64
	saves       atomic.Int64
	validateErr error
}

func (*cloudAPIFakeProvider) ID() string     { return "baidu" }
func (*cloudAPIFakeProvider) RootID() string { return "/" }
func (p *cloudAPIFakeProvider) Validate(context.Context, cloud.Credentials) (cloud.Identity, error) {
	p.validations.Add(1)
	return cloud.Identity{ID: "test-provider-owner", Name: "测试账号"}, p.validateErr
}
func (*cloudAPIFakeProvider) ListFolders(context.Context, cloud.Credentials, string) ([]cloud.Folder, error) {
	return []cloud.Folder{}, nil
}
func (p *cloudAPIFakeProvider) ReadShare(context.Context, cloud.Credentials, cloud.ShareLink) (cloud.Share, error) {
	p.reads.Add(1)
	return cloud.Share{ID: "share", Entries: []cloud.Entry{{ID: "file1", Name: "one.mkv", Path: "one.mkv", Revision: "v1"}}, Data: map[string]string{}}, nil
}
func (*cloudAPIFakeProvider) CreateFolder(_ context.Context, _ cloud.Credentials, parent, name string) (cloud.Folder, error) {
	return cloud.Folder{ID: parent + name, Name: name}, nil
}
func (p *cloudAPIFakeProvider) Save(context.Context, cloud.Credentials, cloud.Share, cloud.Entry, string) error {
	p.saves.Add(1)
	return nil
}

func cloudAPITestRouter(t *testing.T) (*gin.Engine, *cloudAPIFakeProvider) {
	t.Helper()
	p := &cloudAPIFakeProvider{}
	s, err := cloud.NewService(t.TempDir(), []cloud.Provider{p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	r := gin.New()
	r.Use(CORSMiddleware())
	RegisterCloudRoutes(r, s)
	return r, p
}

func cloudAPIRequest(t *testing.T, r http.Handler, method, address, owner string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, address, bytes.NewReader(data))
	req.RemoteAddr = "127.0.0.1:43210"
	if owner != "" {
		req.Header.Set("X-Trim-Userid", owner)
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-Seekbox-Action", "1")
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func cloudAPIConnect(t *testing.T, r http.Handler, owner string) cloud.Account {
	t.Helper()
	w := cloudAPIRequest(t, r, "POST", "/api/cloud/accounts", owner, map[string]any{"provider": "baidu", "name": "账号 " + owner, "credentials": map[string]string{"cookie": "owner-cookie-" + owner}}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Account cloud.Account `json:"account"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Account.ID == "" {
		t.Fatal("missing account ID")
	}
	if strings.Contains(w.Body.String(), "owner-cookie-") {
		t.Fatal("credentials exposed in connect response")
	}
	return out.Account
}

func TestCloudAPITrustedGatewayIdentity(t *testing.T) {
	r, _ := cloudAPITestRouter(t)
	for _, test := range []struct {
		name, remote, owner, forwarded string
		want                           int
	}{
		{"missing identity", "127.0.0.1:43210", "", "", 401},
		{"remote forged identity", "198.51.100.10:43210", "1000", "", 401},
		{"forwarded loopback cannot override peer", "198.51.100.10:43210", "1000", "127.0.0.1", 401},
		{"local IPv4 gateway", "127.0.0.1:43210", "1000", "198.51.100.10", 200},
		{"local IPv6 gateway", "[::1]:43210", "1000", "", 200},
		{"malformed remote", "127.0.0.1", "1000", "", 401},
		{"blank identity", "127.0.0.1:43210", "   ", "", 401},
		{"oversized identity", "127.0.0.1:43210", strings.Repeat("a", 257), "", 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/cloud/accounts", nil)
			req.RemoteAddr = test.remote
			req.Header.Set("X-Trim-Userid", test.owner)
			req.Header.Set("X-Forwarded-For", test.forwarded)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != test.want {
				t.Fatalf("got %d want %d: %s", w.Code, test.want, w.Body.String())
			}
			if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("personal response may be cached")
			}
		})
	}
}

func TestCloudAPIAccountJobAndSubscriptionIsolation(t *testing.T) {
	r, p := cloudAPITestRouter(t)
	a := cloudAPIConnect(t, r, "1000")
	jobResponse := cloudAPIRequest(t, r, "POST", "/api/cloud/transfers", "1000", cloud.TransferRequest{AccountID: a.ID, ShareURL: "https://pan.baidu.com/s/1first", RequestID: "test-request-1"}, nil)
	if jobResponse.Code != 202 {
		t.Fatalf("job create: %d %s", jobResponse.Code, jobResponse.Body.String())
	}
	var job struct {
		Job cloud.Job `json:"job"`
	}
	if err := json.Unmarshal(jobResponse.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	subResponse := cloudAPIRequest(t, r, "POST", "/api/cloud/subscriptions", "1000", map[string]any{"account_id": a.ID, "share_url": "https://pan.baidu.com/s/1second", "interval_minutes": 60}, nil)
	if subResponse.Code != 202 {
		t.Fatalf("subscription create: %d %s", subResponse.Code, subResponse.Body.String())
	}
	var sub struct {
		Subscription cloud.Subscription `json:"subscription"`
	}
	if err := json.Unmarshal(subResponse.Body.Bytes(), &sub); err != nil {
		t.Fatal(err)
	}
	for _, collection := range []string{"accounts", "jobs", "subscriptions"} {
		w := cloudAPIRequest(t, r, "GET", "/api/cloud/"+collection, "2000", nil, nil)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		var data map[string][]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if len(data[collection]) != 0 {
			t.Fatalf("other user can enumerate %s", collection)
		}
	}
	for _, test := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/accounts/" + a.ID + "/folders", nil},
		{"POST", "/accounts/" + a.ID + "/check", nil},
		{"PUT", "/accounts/" + a.ID, map[string]any{"provider": "baidu", "credentials": map[string]string{"cookie": "attacker-cookie"}}},
		{"DELETE", "/accounts/" + a.ID, nil},
		{"GET", "/jobs/" + job.Job.ID, nil},
		{"POST", "/transfers", cloud.TransferRequest{AccountID: a.ID, ShareURL: "https://pan.baidu.com/s/1third", RequestID: "attacker-request"}},
		{"PATCH", "/subscriptions/" + sub.Subscription.ID, map[string]any{"enabled": false}},
		{"POST", "/subscriptions/" + sub.Subscription.ID + "/check", nil},
		{"DELETE", "/subscriptions/" + sub.Subscription.ID, nil},
	} {
		w := cloudAPIRequest(t, r, test.method, "/api/cloud"+test.path, "2000", test.body, nil)
		if w.Code != 404 {
			t.Fatalf("cross-user %s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
	}
	if p.validations.Load() != 1 {
		t.Fatal("cross-user account check/update reached provider")
	}
	for _, endpoint := range []string{"/accounts", "/jobs/" + job.Job.ID, "/subscriptions"} {
		w := cloudAPIRequest(t, r, "GET", "/api/cloud"+endpoint, "1000", nil, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "owner-cookie-") {
			t.Fatalf("owner data unavailable or credentials exposed: %s", w.Body.String())
		}
	}
}

func TestCloudAPIMutationsRequireActionAndJSON(t *testing.T) {
	r, p := cloudAPITestRouter(t)
	body := map[string]any{"provider": "baidu", "credentials": map[string]string{"cookie": "private-cookie"}}
	for _, headers := range []map[string]string{
		{"X-Seekbox-Action": ""},
		{"X-Seekbox-Action": "true"},
		{"Content-Type": "text/plain"},
		{"Content-Type": "application/x-www-form-urlencoded"},
		{"Content-Type": ""},
		{"Sec-Fetch-Site": "cross-site"},
	} {
		w := cloudAPIRequest(t, r, "POST", "/api/cloud/accounts", "1000", body, headers)
		if w.Code != 403 {
			t.Fatalf("unsafe mutation allowed: %v -> %d", headers, w.Code)
		}
	}
	for _, method := range []string{"PUT", "DELETE", "PATCH", "POST"} {
		w := cloudAPIRequest(t, r, method, "/api/cloud/accounts/any", "1000", nil, map[string]string{"X-Seekbox-Action": ""})
		// Unregistered methods return 404 before middleware; every existing
		// mutation route must reject the missing application action header.
		if method == "PUT" || method == "DELETE" {
			if w.Code != 403 {
				t.Fatalf("%s empty-body mutation allowed: %d", method, w.Code)
			}
		}
	}
	if p.validations.Load() != 0 {
		t.Fatal("rejected mutation reached provider")
	}
}

func TestCloudAPICORSPreflightDoesNotGrantWriteHeader(t *testing.T) {
	r, _ := cloudAPITestRouter(t)
	req := httptest.NewRequest("OPTIONS", "/api/cloud/accounts", nil)
	req.Header.Set("Origin", "https://untrusted.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type,x-seekbox-action,x-trim-userid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("preflight: %d", w.Code)
	}
	for _, h := range strings.Split(w.Header().Get("Access-Control-Allow-Headers"), ",") {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "x-seekbox-action", "x-trim-userid", "*":
			t.Fatalf("CORS grants protected header %s", h)
		}
	}
	if strings.EqualFold(w.Header().Get("Access-Control-Allow-Credentials"), "true") {
		t.Fatal("CORS unexpectedly permits authenticated cross-origin requests")
	}
}

func TestCloudAPIOversizedJSONAndErrorDoNotExposeCredentials(t *testing.T) {
	r, p := cloudAPITestRouter(t)
	marker := "private-credential-never-echo"
	w := cloudAPIRequest(t, r, "POST", "/api/cloud/accounts", "1000", map[string]any{"provider": "baidu", "credentials": map[string]string{"cookie": marker + strings.Repeat("x", 65<<10)}}, nil)
	if w.Code != 400 || strings.Contains(w.Body.String(), marker) {
		t.Fatalf("unsafe oversized response: %d %s", w.Code, w.Body.String())
	}
	if p.validations.Load() != 0 {
		t.Fatal("oversized JSON reached provider")
	}
	p.validateErr = errors.New("provider rejected " + marker)
	w = cloudAPIRequest(t, r, "POST", "/api/cloud/accounts", "1000", map[string]any{"provider": "baidu", "credentials": map[string]string{"cookie": marker}}, nil)
	if w.Code != 400 || strings.Contains(w.Body.String(), marker) {
		t.Fatalf("credential leaked from upstream error: %d %s", w.Code, w.Body.String())
	}
}

func TestCloudAPIRejectsInvalidTransferBeforeQueueing(t *testing.T) {
	r, p := cloudAPITestRouter(t)
	a := cloudAPIConnect(t, r, "1000")
	for _, test := range []struct{ url, destination, requestID string }{
		{"https://127.0.0.1/s/1test", "/", "request1"},
		{"https://pan.baidu.com.attacker.example/s/1test", "/", "request2"},
		{"https://pan.quark.cn/s/test", "/", "request3"},
		{"https://pan.baidu.com:443/s/1test", "/", "request4"},
		{"http://pan.baidu.com/s/1test", "/", "request5"},
		{"https://pan.baidu.com/s/1test", "/\x00", "request6"},
		{"https://pan.baidu.com/s/1test", "/", ""},
	} {
		w := cloudAPIRequest(t, r, "POST", "/api/cloud/transfers", "1000", cloud.TransferRequest{AccountID: a.ID, ShareURL: test.url, DestinationID: test.destination, RequestID: test.requestID}, nil)
		if w.Code != 400 {
			t.Fatalf("invalid request accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w := cloudAPIRequest(t, r, "GET", "/api/cloud/jobs", "1000", nil, nil)
	var out struct {
		Jobs []cloud.Job `json:"jobs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 0 || p.reads.Load() != 0 || p.saves.Load() != 0 {
		t.Fatal("invalid transfer made a job or remote write")
	}
}

func TestCloudAPINilServiceReturns503(t *testing.T) {
	r := gin.New()
	r.Use(CORSMiddleware())
	RegisterCloudRoutes(r, nil)
	w := cloudAPIRequest(t, r, "GET", "/api/cloud/accounts", "1000", nil, nil)
	if w.Code != 503 {
		t.Fatalf("unavailable module: %d %s", w.Code, w.Body.String())
	}
	w = cloudAPIRequest(t, r, "GET", "/api/cloud/accounts", "", nil, nil)
	if w.Code != 401 {
		t.Fatal("nil service bypassed identity check")
	}
}

func TestCloudAPIRejectsOversizedBodyOnBodylessMutation(t *testing.T) {
	r, p := cloudAPITestRouter(t)
	a := cloudAPIConnect(t, r, "1000")
	for _, unknownLength := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/api/cloud/accounts/"+a.ID+"/check", strings.NewReader(strings.Repeat(" ", 65<<10)))
		if unknownLength {
			req.ContentLength = -1
			req.TransferEncoding = []string{"chunked"}
		}
		req.RemoteAddr = "127.0.0.1:43210"
		req.Header.Set("X-Trim-Userid", "1000")
		req.Header.Set("X-Seekbox-Action", "1")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 && w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized ignored body accepted: %d", w.Code)
		}
		if p.validations.Load() != 1 {
			t.Fatal("oversized ignored body reached account check")
		}
	}
}

func TestCloudAPIRejectsTrailingJSON(t *testing.T) {
	r, p := cloudAPITestRouter(t)
	first := `{"provider":"baidu","credentials":{"cookie":"private-first-cookie"}}`
	for _, body := range []string{first + ` {"credentials":{"cookie":"private-second-cookie"}}`, first + strings.Repeat(" ", 65<<10)} {
		req := httptest.NewRequest("POST", "/api/cloud/accounts", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:43210"
		req.Header.Set("X-Trim-Userid", "1000")
		req.Header.Set("X-Seekbox-Action", "1")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 && w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("trailing payload accepted: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-first-cookie") || strings.Contains(w.Body.String(), "private-second-cookie") {
			t.Fatal("credential echoed in error")
		}
	}
	if p.validations.Load() != 0 {
		t.Fatal("trailing payload reached provider")
	}
}

func TestCloudFunctionSettingsIsolationAndGuard(t *testing.T) {
	r, _ := cloudAPITestRouter(t)
	// Daily checks alone never register or upload telemetry.
	resp := cloudAPIRequest(t, r, "PUT", "/api/cloud/center", "alice", map[string]any{"daily_enabled": true}, nil)
	if resp.Code != 200 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	resp = cloudAPIRequest(t, r, "GET", "/api/cloud/center", "bob", nil, nil)
	if resp.Code != 200 || strings.Contains(resp.Body.String(), `"daily_enabled":true`) {
		t.Fatal("owner settings leaked")
	}
	for _, route := range []string{"/api/cloud/accounts/foreign/function-check", "/api/cloud/function-checks/foreign/cleanup"} {
		resp = cloudAPIRequest(t, r, "POST", route, "alice", map[string]any{}, map[string]string{"X-Seekbox-Action": ""})
		if resp.Code != 403 {
			t.Fatal("missing action header accepted", route, resp.Code)
		}
	}
	resp = cloudAPIRequest(t, r, "GET", "/api/cloud/function-checks", "", nil, nil)
	if resp.Code != 401 {
		t.Fatal("unauthenticated records access")
	}
}
