package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"pansou/cloud"
	"pansou/update"
)

type updateAPIFake struct {
	checks, installs, settings atomic.Int32
	finish                     chan struct{}
}

func (*updateAPIFake) Status() update.Status {
	return update.Status{Enabled: true, CurrentVersion: "1.6.0", Phase: "idle"}
}
func (f *updateAPIFake) Check(context.Context) (update.Status, error) {
	f.checks.Add(1)
	return f.Status(), nil
}
func (f *updateAPIFake) Install(ctx context.Context) (update.Status, error) {
	f.installs.Add(1)
	select {
	case <-f.finish:
	case <-ctx.Done():
	}
	return f.Status(), nil
}
func (f *updateAPIFake) SetAuto(bool) error { f.settings.Add(1); return nil }

func TestUpdateAPIRequiresExplicitAdminAndTrustedGateway(t *testing.T) {
	f := &updateAPIFake{finish: make(chan struct{})}
	defer close(f.finish)
	r := gin.New()
	RegisterUpdateRoutes(r, f, []string{"1000"})
	for _, tc := range []struct {
		method, path, owner string
		body                any
		headers             map[string]string
		want                int
	}{
		{"GET", "/api/update/status", "1001", nil, nil, 200},
		{"GET", "/api/update/status", "", nil, nil, 401},
		{"POST", "/api/update/check", "1001", map[string]any{}, nil, 403},
		{"POST", "/api/update/check", "1000", map[string]any{}, map[string]string{"X-Seekbox-Action": ""}, 403},
		{"POST", "/api/update/check", "1000", map[string]any{}, map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"POST", "/api/update/check", "1000", map[string]any{"url": "https://evil.example/payload"}, nil, 400},
		{"POST", "/api/update/install", "1000", map[string]any{"version": "1.9.0"}, nil, 400},
		{"PATCH", "/api/update/settings", "1000", map[string]any{"auto_update": false, "command": "x"}, nil, 400},
		{"PATCH", "/api/update/settings", "1000", map[string]any{}, nil, 400},
		{"PATCH", "/api/update/settings", "1000", map[string]any{"auto_update": false}, nil, 200},
	} {
		w := cloudAPIRequest(t, r, tc.method, tc.path, tc.owner, tc.body, tc.headers)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/api/update/status", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Trim-Userid", "1000")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("untrusted peer bypassed gateway")
	}
	if f.checks.Load() != 0 || f.installs.Load() != 0 || f.settings.Load() != 1 {
		t.Fatal("unauthorized request reached updater")
	}
}
func TestUpdateAPIDefaultReadOnlyAndInstallSurvivesBrowser(t *testing.T) {
	f := &updateAPIFake{finish: make(chan struct{})}
	defer close(f.finish)
	readOnly := gin.New()
	RegisterUpdateRoutes(readOnly, f, nil)
	if w := cloudAPIRequest(t, readOnly, "POST", "/api/update/install", "1000", map[string]any{}, nil); w.Code != 403 {
		t.Fatal("implicit administrator")
	}
	r := gin.New()
	RegisterUpdateRoutes(r, f, []string{"1000"})
	w := cloudAPIRequest(t, r, "POST", "/api/update/install", "1000", map[string]any{}, nil)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for f.installs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.installs.Load() != 1 {
		t.Fatal("install not launched")
	}
	if w := cloudAPIRequest(t, r, "POST", "/api/update/install", "1000", map[string]any{}, nil); w.Code != 409 {
		t.Fatal("duplicate install admitted")
	}
}

func TestCloudMutationsReturnMaintenanceAndResume(t *testing.T) {
	t.Setenv("SEEKBOX_CENTER_URL", "http://127.0.0.1:1")
	s, err := cloud.NewService(t.TempDir(), []cloud.Provider{&cloudAPIFakeProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := gin.New()
	RegisterCloudRoutes(r, s)
	if err := s.PrepareUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := cloudAPIRequest(t, r, "PUT", "/api/cloud/center", "1000", map[string]any{"daily_enabled": false}, nil)
	if w.Code != 503 || w.Header().Get("Retry-After") != "30" {
		t.Fatalf("maintenance response: %d %s", w.Code, w.Body.String())
	}
	w = cloudAPIRequest(t, r, "GET", "/api/cloud/accounts", "1001", nil, nil)
	if w.Code != 200 {
		t.Fatal("read-only accounts should remain available")
	}
	s.ResumeUpdate()
	w = cloudAPIRequest(t, r, "PUT", "/api/cloud/center", "1000", map[string]any{"daily_enabled": false}, nil)
	if w.Code != 200 {
		t.Fatal("admission did not resume")
	}
}
