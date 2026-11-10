package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func scanRequest(f *fixture, method, path, token, origin string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("X-Scan-Client", "attacker_supplied_identity")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func TestScanProxyUsesVerifiedInstanceAndRechecksRevocation(t *testing.T) {
	f := setup(t)
	id, token := f.register("scan_client")
	var calls atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 40) || r.Header.Get("X-Scan-Client") != id || r.Header.Get("Origin") != "" {
			t.Error("proxy identity or authentication mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000000","status":"queued"}`))
	}))
	defer internal.Close()
	f.a.scanURL, f.a.scanSecret = internal.URL, strings.Repeat("k", 40)
	path := "/api/client/scan/sessions"
	input := gin.H{"provider": "xunlei", "account_id": "account_0000000001", "request_id": "request_0000000001"}
	for _, tc := range []struct {
		token, origin string
		status        int
	}{{"", "", 401}, {"invalid", "", 401}, {token, "https://hermes.smartloan.cn", 403}, {token, "", 200}} {
		w := scanRequest(f, "POST", path, tc.token, tc.origin, input)
		if w.Code != tc.status {
			t.Fatalf("unexpected scan response: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatal("unauthorized request reached scanner")
	}
	f.req("POST", adminPrefix+"/instances/"+id+"/revoke", gin.H{}, "", true, 200)
	if w := scanRequest(f, "POST", path, token, "", input); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if calls.Load() != 1 {
		t.Fatal("revoked client reached scanner")
	}
}
func TestScanStatusDynamicKeyGrantRevokeAndNoCookieFallback(t *testing.T) {
	f := setup(t)
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/status" {
			t.Error(r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"active":0,"queued":0}`))
	}))
	defer internal.Close()
	f.a.scanURL, f.a.scanSecret = internal.URL, strings.Repeat("k", 40)
	body := gin.H{"name": "Scan monitor", "permissions": []string{}, "enabled": true, "expires_at": nil}
	_, r := f.req("POST", adminPrefix+"/api-keys", body, "", true, 200)
	key := data(r)["api_key"].(string)
	id := data(r)["key"].(map[string]any)["id"].(string)
	route := adminPrefix + "/scan/status"
	f.req("GET", route, nil, key, true, 403)
	body["permissions"] = []string{"GET " + route}
	f.req("PUT", adminPrefix+"/api-keys/"+id, body, "", true, 200)
	f.req("GET", route, nil, key, false, 200)
	f.req("GET", route, nil, "invalid", true, 401)
	body["permissions"] = []string{}
	f.req("PUT", adminPrefix+"/api-keys/"+id, body, "", true, 200)
	f.req("GET", route, nil, key, true, 403)
	if w := scanRequest(f, "POST", "/api/client/scan/sessions", key, "", gin.H{}); w.Code != 401 {
		t.Fatal("management key accepted for account credentials")
	}
}
func TestScanProxyLimitsAndUpstreamFailure(t *testing.T) {
	f := setup(t)
	_, token := f.register("scan_client")
	path := "/api/client/scan/sessions"
	if w := scanRequest(f, "POST", path, token, "", gin.H{}); w.Code != 503 {
		t.Fatal(w.Code)
	}
	mode := "limit"
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "limit" {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"code":"RATE_LIMITED","message":"扫码请求较多"}`))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("not json"))
	}))
	defer internal.Close()
	f.a.scanURL, f.a.scanSecret = internal.URL, strings.Repeat("k", 40)
	w := scanRequest(f, "POST", path, token, "", gin.H{})
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code)
	}
	mode = "invalid"
	w = scanRequest(f, "POST", path, token, "", gin.H{})
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
	w = scanRequest(f, "POST", path, token, "", gin.H{"padding": strings.Repeat("a", 9000)})
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
	w = scanRequest(f, "GET", path+"/invalid", token, "", nil)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
