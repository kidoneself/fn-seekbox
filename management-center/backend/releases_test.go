package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
)

type releaseFixture struct {
	*fixture
	private ed25519.PrivateKey
}

func setupReleases(t *testing.T) *releaseFixture {
	f := setup(t)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.a.releaseKeys = map[string]ed25519.PublicKey{"test-key": public}
	return &releaseFixture{f, private}
}
func (f *releaseFixture) signed(version, channel, arch string, artifact []byte) SignedReleaseManifest {
	h := sha256.Sum256(artifact)
	payload := ReleasePayload{Schema: 1, App: "fn-seekbox", Version: version, Channel: channel, Arch: arch, MinBootstrap: 1, DataSchema: 1, SHA256: hex.EncodeToString(h[:]), Size: int64(len(artifact)), Notes: "修复更新流程"}
	raw, _ := json.Marshal(payload)
	return SignedReleaseManifest{KeyID: "test-key", Payload: base64.StdEncoding.EncodeToString(raw), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(f.private, raw))}
}
func (f *releaseFixture) upload(m SignedReleaseManifest, artifact []byte, session bool, origin string, status int) map[string]any {
	f.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("manifest", "manifest.json")
	json.NewEncoder(part).Encode(m)
	part, _ = writer.CreateFormFile("artifact", "../../ignored.tar.gz")
	part.Write(artifact)
	writer.Close()
	req := httptest.NewRequest("POST", adminPrefix+"/releases", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", origin)
	if session {
		req.AddCookie(f.cookie)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	if w.Code != status {
		f.t.Fatalf("upload got %d want %d: %s", w.Code, status, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		f.t.Fatal(err)
	}
	return result
}
func (f *releaseFixture) create(version, channel, arch string, publish bool) string {
	f.t.Helper()
	artifact := []byte("isolated signed artifact " + version + channel + arch)
	result := f.upload(f.signed(version, channel, arch, artifact), artifact, true, f.a.origin, 201)
	id := data(result)["id"].(string)
	if publish {
		f.req("POST", adminPrefix+"/releases/"+id+"/publish", gin.H{}, "", true, 200)
	}
	return id
}
func (f *releaseFixture) latest(channel, arch, version string) map[string]any {
	_, result := f.req("GET", "/api/client/releases/latest?app=fn-seekbox&channel="+channel+"&arch="+arch+"&version="+version, nil, "", false, 200)
	return data(result)
}
func TestReleaseUploadAuthenticationIntegrityAndLifecycle(t *testing.T) {
	f := setupReleases(t)
	artifact := []byte("signed archive fixture")
	m := f.signed("1.6.1", "stable", "amd64", artifact)
	f.upload(m, artifact, false, f.a.origin, 401)
	f.upload(m, artifact, true, "https://untrusted.example", 403)
	bad := m
	bad.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64))
	f.upload(bad, artifact, true, f.a.origin, 400)
	bad = m
	bad.KeyID = "not-trusted"
	f.upload(bad, artifact, true, f.a.origin, 400)
	f.upload(m, []byte("tampered"), true, f.a.origin, 400)
	result := f.upload(m, artifact, true, f.a.origin, 201)
	id := data(result)["id"].(string)
	f.upload(m, artifact, true, f.a.origin, 409)
	if f.latest("stable", "amd64", "1.6.0")["available"] != false {
		t.Fatal("draft advertised")
	}
	f.req("GET", "/api/client/releases/"+id+"/artifact", nil, "", false, 404)
	f.req("POST", adminPrefix+"/releases/"+id+"/publish", gin.H{}, "", false, 401)
	f.req("POST", adminPrefix+"/releases/"+id+"/publish", gin.H{}, "", true, 200)
	f.req("POST", adminPrefix+"/releases/"+id+"/publish", gin.H{}, "", true, 200)
	if f.latest("stable", "amd64", "1.6.0")["release_id"] != id {
		t.Fatal("published release missing")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/client/releases/"+id+"/artifact", nil))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), artifact) || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("invalid artifact response %d", w.Code)
	}
	f.req("POST", adminPrefix+"/releases/"+id+"/withdraw", gin.H{}, "", true, 200)
	f.req("POST", adminPrefix+"/releases/"+id+"/withdraw", gin.H{}, "", true, 200)
	if f.latest("stable", "amd64", "1.6.0")["available"] != false {
		t.Fatal("withdrawn release advertised")
	}
	f.req("GET", "/api/client/releases/"+id+"/artifact", nil, "", false, 404)
	f.req("POST", adminPrefix+"/releases/"+id+"/publish", gin.H{}, "", true, 409)
}
func TestReleaseVersionComparisonAndPublishRechecks(t *testing.T) {
	f := setupReleases(t)
	f.create("1.9.0", "stable", "amd64", true)
	newest := f.create("1.10.0", "stable", "amd64", true)
	f.create("2.0.0", "beta", "amd64", true)
	f.create("3.0.0", "stable", "arm64", true)
	draft := f.create("9.0.0", "stable", "amd64", false)
	if f.latest("stable", "amd64", "1.8.0")["release_id"] != newest {
		t.Fatal("must compare version components numerically")
	}
	if f.latest("stable", "amd64", "1.10.0")["available"] != false || f.latest("stable", "amd64", "10.0.0")["available"] != false {
		t.Fatal("equal/older version offered")
	}
	for _, version := range []string{"1.2", "v1.2.3", "01.2.3", "1.2.3-beta", "-1.2.3"} {
		f.req("GET", "/api/client/releases/latest?app=fn-seekbox&channel=stable&arch=amd64&version="+version, nil, "", false, 400)
	}
	path, _ := f.a.artifactPath(draft)
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	f.req("POST", adminPrefix+"/releases/"+draft+"/publish", gin.H{}, "", true, 409)
	if _, err := f.a.artifactPath("../../etc/passwd"); err == nil {
		t.Fatal("unsafe artifact ID allowed")
	}
}
func TestReleaseReportIdentityDeduplicationAndVersionDistribution(t *testing.T) {
	f := setupReleases(t)
	rel := f.create("1.6.1", "stable", "amd64", true)
	instance, token := f.register("release_client_a")
	other, otherToken := f.register("release_client_b")
	report := gin.H{"release_id": rel, "event_id": "update_one", "version": "1.6.1", "from_version": "1.5.13", "status": "success", "error_code": "", "occurred_at": f.now}
	f.req("POST", "/api/client/releases/report", report, "wrong-token", false, 401)
	_, out := f.req("POST", "/api/client/releases/report", report, token, false, 200)
	if data(out)["accepted"] != true || data(out)["duplicate"] != false {
		t.Fatal("missing durable receipt")
	}
	_, out = f.req("POST", "/api/client/releases/report", report, token, false, 200)
	if data(out)["duplicate"] != true {
		t.Fatal("retry not deduplicated")
	}
	report["status"] = "failed"
	report["error_code"] = "HEALTH_CHECK_FAILED"
	f.req("POST", "/api/client/releases/report", report, token, false, 409)
	report["status"] = "success"
	report["error_code"] = ""
	report["installation_id"] = other
	f.req("POST", "/api/client/releases/report", report, token, false, 400)
	delete(report, "installation_id")
	f.req("POST", "/api/client/releases/report", report, otherToken, false, 200)
	report["event_id"] = "unpublished"
	report["release_id"] = "rel_nonexistent"
	f.req("POST", "/api/client/releases/report", report, token, false, 400)
	report["release_id"] = rel
	report["event_id"] = "rollback_one"
	report["status"] = "rolled_back"
	report["error_code"] = "HEALTH_CHECK_FAILED"
	report["occurred_at"] = f.now.Add(time.Second)
	f.req("POST", "/api/client/releases/report", report, token, false, 200)
	_, out = f.req("GET", adminPrefix+"/releases", nil, "", true, 200)
	summary := data(out)
	if summary["total_reports"] != float64(3) {
		t.Fatal("duplicate report counted")
	}
	distribution := summary["version_distribution"].(map[string]any)
	if distribution["1.6.1"] != float64(1) || distribution["1.5.13"] != float64(1) {
		t.Fatalf("incorrect versions %#v", distribution)
	}
	for _, raw := range summary["recent_reports"].([]any) {
		id := raw.(map[string]any)["installation_id"]
		if id != instance && id != other {
			t.Fatal("incorrect instance mapping")
		}
	}
	// A newer heartbeat takes priority over an older update report.
	f.a.store.db.Update(func(tx *bolt.Tx) error {
		var i Instance
		get(tx, "instances", instance, &i)
		i.AppVersion = "1.7.0"
		at := f.now.Add(time.Minute)
		i.ReportedAt = &at
		return put(tx, "instances", instance, i)
	})
	_, out = f.req("GET", adminPrefix+"/releases", nil, "", true, 200)
	if data(out)["version_distribution"].(map[string]any)["1.7.0"] != float64(1) {
		t.Fatal("update report overwrote newer heartbeat")
	}
	f.req("POST", adminPrefix+"/instances/"+instance+"/revoke", gin.H{}, "", true, 200)
	f.req("POST", "/api/client/releases/report", report, token, false, 401)
}
func TestReleaseReportMultipleChannelsAndLegacyRetry(t *testing.T) {
	f := setupReleases(t)
	rel := f.create("1.6.1", "stable", "amd64", true)
	_, token := f.register("release_client")
	input := gin.H{"event_id": "legacy_one", "version": "1.6.1", "from_version": "1.5.13", "status": "success", "occurred_at": f.now}
	f.req("POST", "/api/client/releases/report", input, token, false, 200)
	_, out := f.req("POST", "/api/client/releases/report", input, token, false, 200)
	if data(out)["duplicate"] != true {
		t.Fatal("legacy retry not idempotent")
	}
	f.create("1.6.1", "beta", "amd64", true)
	input["event_id"] = "ambiguous"
	f.req("POST", "/api/client/releases/report", input, token, false, 409)
	input["release_id"] = rel
	f.req("POST", "/api/client/releases/report", input, token, false, 200)
}
func TestReleaseKeyConfigurationAndDocumentation(t *testing.T) {
	f := setupReleases(t)
	raw, _ := json.Marshal(map[string]string{"test-key": base64.StdEncoding.EncodeToString(f.private.Public().(ed25519.PublicKey))})
	keys, err := loadReleaseKeys("", string(raw))
	if err != nil || len(keys) != 1 {
		t.Fatalf("valid key rejected: %v", err)
	}
	file := filepath.Join(t.TempDir(), "keys.json")
	os.WriteFile(file, raw, 0600)
	if _, err := loadReleaseKeys(file, "ignored"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReleaseKeys("", `{"broken":"not-a-public-key"}`); err == nil {
		t.Fatal("invalid key accepted")
	}
	for _, endpoint := range f.a.registry {
		if endpoint.Path == adminPrefix+"/releases" && endpoint.Method == "POST" && endpoint.Auth != "admin_session" {
			t.Fatal("upload should require admin session")
		}
		if endpoint.Path == "/api/client/releases/latest" && endpoint.Auth != "public" {
			t.Fatal("latest is public")
		}
	}
	f.req("POST", adminPrefix+"/api-keys", gin.H{"name": "bad publisher", "permissions": []string{"POST " + adminPrefix + "/releases"}}, "", true, 400)
	_, out := f.req("POST", adminPrefix+"/api-keys", gin.H{"name": "read releases", "permissions": []string{"GET " + adminPrefix + "/releases"}}, "", true, 200)
	key := data(out)["api_key"].(string)
	f.req("GET", adminPrefix+"/releases", nil, key, false, 200)
	rel := f.create("1.6.1", "stable", "amd64", false)
	f.req("POST", adminPrefix+"/releases/"+rel+"/publish", gin.H{}, key, true, 403)
	f.req("GET", adminPrefix+"/releases", nil, "bad-key", true, 401)
	// No CORS origin is allowed to bypass writeGuard with an admin cookie.
	req := httptest.NewRequest("POST", adminPrefix+"/releases/"+rel+"/publish", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("missing Origin accepted")
	}
}
