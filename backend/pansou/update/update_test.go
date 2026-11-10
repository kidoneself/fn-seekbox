package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testEntry struct {
	name string
	data []byte
	kind byte
	link string
}

func testArchive(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(e.data))
		if kind != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0755, Typeflag: kind, Size: size, Linkname: e.link}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := tw.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func elfFixture(arch string) []byte {
	b := make([]byte, 64)
	copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(b[16:], 2)
	machine := uint16(62)
	if arch == "arm64" {
		machine = 183
	}
	binary.LittleEndian.PutUint16(b[18:], machine)
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint16(b[52:], 64)
	return b
}
func validEntries() []testEntry {
	return []testEntry{{name: "bin/seekbox", data: elfFixture("amd64")}, {name: "bin/gateway.py", data: []byte("# test")}, {name: "web/index.html", data: []byte("test")}, {name: "web/version.json", data: []byte(`{"version":"1.6.1"}`)}, {name: "runtime.json", data: []byte(`{"version":"1.6.1","arch":"amd64","data_schema":1}`)}}
}
func fixtureManifest(archive []byte) Manifest {
	hash := sha256.Sum256(archive)
	return Manifest{Schema: 1, App: "fn-seekbox", Version: "1.6.1", Channel: "stable", Arch: "amd64", MinBootstrap: 1, DataSchema: 1, SHA256: hex.EncodeToString(hash[:]), Size: int64(len(archive))}
}
func signed(m Manifest, key ed25519.PrivateKey) SignedManifest {
	b, _ := json.Marshal(m)
	return SignedManifest{KeyID: "test", Payload: base64.StdEncoding.EncodeToString(b), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, b))}
}
func testConfig(t *testing.T) (Config, ed25519.PrivateKey) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	c := Config{Root: t.TempDir(), CurrentVersion: "1.6.0", App: "fn-seekbox", Channel: "stable", Arch: "amd64", PublicKeys: map[string]ed25519.PublicKey{"test": pub}, DisableScheduler: true, AllowLoopbackHTTP: true}
	return c, key
}

func TestVerifySignedManifest(t *testing.T) {
	c, key := testConfig(t)
	m := fixtureManifest([]byte("test"))
	if _, e := verifyManifest(signed(m, key), c); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name   string
		change func(*Manifest)
	}{{"old", func(m *Manifest) { m.Version = "1.5.9" }}, {"same", func(m *Manifest) { m.Version = "1.6.0" }}, {"wrong arch", func(m *Manifest) { m.Arch = "arm64" }}, {"schema migration", func(m *Manifest) { m.DataSchema = 2 }}, {"new bootstrap", func(m *Manifest) { m.MinBootstrap = 2 }}, {"huge", func(m *Manifest) { m.Size = MaxArchiveSize + 1 }}} {
		t.Run(tc.name, func(t *testing.T) {
			copy := m
			tc.change(&copy)
			if _, e := verifyManifest(signed(copy, key), c); e == nil {
				t.Fatal("accepted incompatible release")
			}
		})
	}
	tampered := signed(m, key)
	tampered.Payload = base64.StdEncoding.EncodeToString([]byte(`{}`))
	if _, e := verifyManifest(tampered, c); e == nil {
		t.Fatal("accepted altered payload")
	}
	unknown := signed(m, key)
	unknown.KeyID = "remote-key"
	if _, e := verifyManifest(unknown, c); e == nil {
		t.Fatal("accepted unknown key")
	}
}
func TestExtractRejectsUnsafePackages(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra testEntry
	}{{"traversal", testEntry{name: "../credentials.key"}}, {"absolute", testEntry{name: "/tmp/pwn"}}, {"backslash", testEntry{name: "web\\evil"}}, {"symlink", testEntry{name: "web/link", kind: tar.TypeSymlink, link: "/etc/passwd"}}, {"hardlink", testEntry{name: "web/hard", kind: tar.TypeLink, link: "runtime.json"}}, {"duplicate", testEntry{name: "web/index.html"}}, {"database", testEntry{name: "data/cloud.db"}}, {"bootstrap", testEntry{name: "bin/update-helper.py"}}, {"unpaired login", testEntry{name: "auth/login.cjs"}}} {
		t.Run(tc.name, func(t *testing.T) {
			data := testArchive(t, append(validEntries(), tc.extra))
			archive := filepath.Join(t.TempDir(), "archive.gz")
			os.WriteFile(archive, data, 0600)
			if e := extractArchive(archive, t.TempDir(), fixtureManifest(data)); e == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
	data := testArchive(t, validEntries())
	archive := filepath.Join(t.TempDir(), "archive.gz")
	os.WriteFile(archive, data, 0600)
	dest := t.TempDir()
	if e := extractArchive(archive, dest, fixtureManifest(data)); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(filepath.Join(dest, "bin/seekbox"))
	if info.Mode().Perm() != 0755 {
		t.Fatal("backend lost executable mode")
	}
	entries := validEntries()
	entries[0].data = elfFixture("arm64")
	data = testArchive(t, entries)
	os.WriteFile(archive, data, 0600)
	if e := extractArchive(archive, t.TempDir(), fixtureManifest(data)); e == nil {
		t.Fatal("accepted wrong ELF architecture")
	}
}
func setupServer(t *testing.T, c *Config, key ed25519.PrivateKey) (*httptest.Server, []byte) {
	t.Helper()
	archive := testArchive(t, validEntries())
	m := fixtureManifest(archive)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/latest") {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"available": true, "release_id": "release-test", "manifest": signed(m, key), "download_url": server.URL + "/package"}})
			return
		}
		w.Write(archive)
	}))
	c.BaseURL = server.URL
	c.HealthURL = "http://localhost/api/health"
	c.HelperPath = filepath.Join(t.TempDir(), "helper.py")
	os.WriteFile(c.HelperPath, []byte("raise RuntimeError('cannot initialize')"), 0600)
	c.BootstrapCmd = filepath.Join(t.TempDir(), "bootstrap.sh")
	os.WriteFile(c.BootstrapCmd, []byte("exit 0"), 0600)
	t.Cleanup(server.Close)
	return server, archive
}
func TestCheckAndDisabledTrust(t *testing.T) {
	c, key := testConfig(t)
	setupServer(t, &c, key)
	m, e := New(c, Hooks{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s, e := m.Check(context.Background())
	if e != nil || !s.Available || s.Release.Manifest.Version != "1.6.1" {
		t.Fatalf("check: %+v %v", s, e)
	}
	c.PublicKeys = nil
	disabled, e := New(c, Hooks{})
	if e != nil {
		t.Fatal(e)
	}
	defer disabled.Close()
	if disabled.Status().Enabled {
		t.Fatal("trust-free updater enabled")
	}
	if _, e = disabled.Check(context.Background()); e == nil {
		t.Fatal("trust-free check succeeded")
	}
	if e = disabled.SetAuto(false); e != nil {
		t.Fatal(e)
	}
}
func TestInstallCancellationAndMutualExclusion(t *testing.T) {
	c, key := testConfig(t)
	setupServer(t, &c, key)
	entered := make(chan struct{})
	var resumed atomic.Int32
	m, e := New(c, Hooks{Prepare: func(ctx context.Context) error { close(entered); <-ctx.Done(); resumed.Add(1); return ctx.Err() }, Resume: func() { resumed.Add(1) }})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := m.Install(context.Background()); done <- e }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("prepare not entered")
	}
	if _, e = m.Install(context.Background()); e == nil {
		t.Fatal("concurrent install accepted")
	}
	m.Close()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel install")
	}
	if resumed.Load() != 1 {
		t.Fatal("maintenance not released exactly once")
	}
}
func TestHelperInitializationFailureResumes(t *testing.T) {
	c, key := testConfig(t)
	setupServer(t, &c, key)
	var resumed atomic.Int32
	m, e := New(c, Hooks{Prepare: func(context.Context) error { return nil }, Resume: func() { resumed.Add(1) }})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if _, e = m.Install(context.Background()); e == nil {
		t.Fatal("failed helper accepted")
	}
	if resumed.Load() != 1 {
		t.Fatal("maintenance left active")
	}
	if isPending(m.Status().Phase) {
		t.Fatal("failed transaction remained pending")
	}
	if _, e = m.Check(context.Background()); e != nil {
		t.Fatalf("cannot check after helper failure: %v", e)
	}
}
func TestDownloadFailureAndAutomaticSuppression(t *testing.T) {
	c, key := testConfig(t)
	server, archive := setupServer(t, &c, key)
	m, e := New(c, Hooks{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	manifest := fixtureManifest(archive)
	bad := manifest
	bad.SHA256 = strings.Repeat("0", 64)
	if _, e = m.downloadAndStage(context.Background(), Release{Manifest: bad, DownloadURL: server.URL + "/package"}); e == nil {
		t.Fatal("accepted mismatched download")
	}
	writeJSON(filepath.Join(c.Root, "failed-release.json"), map[string]string{"sha256": manifest.SHA256})
	if _, e = m.performInstall(context.Background(), true); e == nil || !strings.Contains(e.Error(), "suppressed") {
		t.Fatalf("bad version auto retried: %v", e)
	}
	server.Close()
	if _, e = m.Check(context.Background()); e == nil {
		t.Fatal("network failure not reported")
	}
}
func TestCrossOriginRedirectBlocked(t *testing.T) {
	c, _ := testConfig(t)
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer other.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer source.Close()
	c.BaseURL = source.URL
	m, e := New(c, Hooks{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if _, e = m.Check(context.Background()); e == nil {
		t.Fatal("cross-origin redirect accepted")
	}
	if hits.Load() != 0 {
		t.Fatal("untrusted origin contacted")
	}
}

func TestPrepareFailureDoesNotReleaseAnotherOwner(t *testing.T) {
	c, key := testConfig(t)
	setupServer(t, &c, key)
	var resumed atomic.Int32
	m, err := New(c, Hooks{Prepare: func(context.Context) error { return errors.New("maintenance already owned") }, Resume: func() { resumed.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err = m.Install(context.Background()); err == nil {
		t.Fatal("expected busy failure")
	}
	if resumed.Load() != 0 {
		t.Fatal("released someone else's maintenance")
	}
}
func TestCancellationAfterPrepareRestoresMaintenance(t *testing.T) {
	c, key := testConfig(t)
	setupServer(t, &c, key)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var resumed atomic.Int32
	m, err := New(c, Hooks{Prepare: func(context.Context) error { cancel(); return nil }, Resume: func() { resumed.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err = m.Install(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if resumed.Load() != 1 {
		t.Fatal("maintenance not restored after successful preparation")
	}
}
func TestWithdrawalDuringPrepareStopsInstall(t *testing.T) {
	c, key := testConfig(t)
	archive := testArchive(t, validEntries())
	manifest := fixtureManifest(archive)
	var withdrawn atomic.Bool
	var resumed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "latest") {
			if withdrawn.Load() {
				json.NewEncoder(w).Encode(map[string]bool{"available": false})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"available": true, "release_id": "r1", "manifest": signed(manifest, key), "download_url": "/package"})
			}
			return
		}
		w.Write(archive)
	}))
	defer server.Close()
	c.BaseURL = server.URL
	c.HealthURL = "http://localhost/api/health"
	c.HelperPath = filepath.Join(t.TempDir(), "helper")
	c.BootstrapCmd = filepath.Join(t.TempDir(), "bootstrap")
	os.WriteFile(c.HelperPath, nil, 0600)
	os.WriteFile(c.BootstrapCmd, nil, 0600)
	m, err := New(c, Hooks{Prepare: func(context.Context) error { withdrawn.Store(true); return nil }, Resume: func() { resumed.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err = m.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "withdrawn") {
		t.Fatalf("withdrawn release installed: %v", err)
	}
	if resumed.Load() != 1 {
		t.Fatal("maintenance not restored")
	}
	if _, err = os.Stat(filepath.Join(c.Root, "update-state.json")); !os.IsNotExist(err) {
		t.Fatal("withdrawn release created installation transaction")
	}
}

func TestStartupResumeRequiresMatchingTransactionAndRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, phase, version, from, current string
		want                                bool
	}{{"candidate committed", "committed", "1.6.1", "1.6.0", "1.6.1", true}, {"old restored", "rolled_back", "1.6.1", "1.6.0", "1.6.0", true}, {"candidate still checking", "installing", "1.6.1", "1.6.0", "1.6.1", false}, {"wrong running version", "committed", "1.6.1", "1.6.0", "1.6.0", false}, {"rollback not healthy", "recovery_failed", "1.6.1", "1.6.0", "1.6.0", false}} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testConfig(t)
			c.CurrentVersion = tc.current
			c.ResumeAfterRecovery = true
			initial := transaction{ID: "startup", Phase: "installing", Version: tc.version, FromVersion: tc.from}
			writeJSON(filepath.Join(c.Root, "update-state.json"), initial)
			var resumed atomic.Int32
			m, e := New(c, Hooks{Resume: func() { resumed.Add(1) }})
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			initial.Phase = tc.phase
			writeJSON(filepath.Join(c.Root, "update-state.json"), initial)
			m.resumeRecoveredStartup()
			m.resumeRecoveredStartup()
			want := int32(0)
			if tc.want {
				want = 1
			}
			if resumed.Load() != want {
				t.Fatalf("resume calls %d expected %d", resumed.Load(), want)
			}
		})
	}
	c, _ := testConfig(t)
	if NeedsStartupPause(c.Root, c.CurrentVersion) {
		t.Fatal("missing journal should not pause")
	}
	os.WriteFile(filepath.Join(c.Root, "update-state.json"), []byte("broken"), 0600)
	if !NeedsStartupPause(c.Root, c.CurrentVersion) {
		t.Fatal("corrupt journal must pause")
	}
}
