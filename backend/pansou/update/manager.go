package update

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	startupTransactionID string
	cfg                  Config
	hooks                Hooks
	client               *http.Client
	mu                   sync.Mutex
	operation            sync.Mutex
	status               Status
	ctx                  context.Context
	cancel               context.CancelFunc
	wg                   sync.WaitGroup
}

type transaction struct {
	SHA256      string    `json:"sha256"`
	ReleaseID   string    `json:"release_id"`
	ID          string    `json:"id"`
	Phase       string    `json:"phase"`
	Target      string    `json:"target"`
	Previous    string    `json:"previous"`
	Version     string    `json:"version"`
	FromVersion string    `json:"from_version"`
	Error       string    `json:"error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func New(c Config, h Hooks) (*Manager, error) {
	if c.Root == "" {
		return nil, errors.New("update runtime root is required")
	}
	root, err := filepath.Abs(c.Root)
	if err != nil {
		return nil, err
	}
	c.Root = root
	if c.App == "" {
		c.App = "fn-seekbox"
	}
	if c.Channel == "" {
		c.Channel = "stable"
	}
	if c.Arch == "" {
		c.Arch = runtime.GOARCH
	}
	if _, err := versionParts(c.CurrentVersion); err != nil {
		return nil, fmt.Errorf("current version: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(c.Root, "events"), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(c.Root, "releases"), 0755); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(c.Root)
	if err != nil {
		return nil, err
	}
	c.Root = canonical
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{cfg: c, hooks: h, ctx: ctx, cancel: cancel}
	if c.ResumeAfterRecovery {
		var initial transaction
		if b, e := os.ReadFile(filepath.Join(c.Root, "update-state.json")); e == nil && json.Unmarshal(b, &initial) == nil {
			m.startupTransactionID = initial.ID
		}
	}
	auto := true
	if c.AutoEnabled != nil {
		auto = *c.AutoEnabled
	}
	var prefs struct {
		AutoEnabled bool `json:"auto_enabled"`
	}
	if b, err := os.ReadFile(filepath.Join(c.Root, "settings.json")); err == nil && json.Unmarshal(b, &prefs) == nil {
		auto = prefs.AutoEnabled
	}
	m.status = Status{CurrentVersion: c.CurrentVersion, AutoEnabled: auto, Phase: "idle", UpdatedAt: time.Now().UTC()}
	source, sourceErr := checkedURL(c.BaseURL, c.AllowLoopbackHTTP)
	if len(c.PublicKeys) == 0 || c.BaseURL == "" {
		m.status.Phase = "disabled"
		m.status.Error = "尚未配置可信更新源和发布公钥"
	} else if sourceErr != nil {
		m.status.Phase = "disabled"
		m.status.Error = sourceErr.Error()
	} else {
		m.status.Enabled = true
	}
	client := http.Client{Timeout: 2 * time.Minute}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many update redirects")
		}
		if source == nil || !sameOrigin(source, req.URL) {
			return errors.New("cross-origin update redirect forbidden")
		}
		_, err := checkedURL(req.URL.String(), c.AllowLoopbackHTTP)
		return err
	}
	m.client = &client
	if !c.DisableScheduler {
		m.wg.Add(1)
		go m.schedule()
	}
	return m, nil
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	s := m.status
	if s.Release != nil {
		r := *s.Release
		s.Release = &r
	}
	m.mu.Unlock()
	var t transaction
	if b, err := os.ReadFile(filepath.Join(m.cfg.Root, "update-state.json")); err == nil && json.Unmarshal(b, &t) == nil {
		if t.Phase == "prepared" || t.Phase == "installing" || t.Phase == "rolling_back" || t.Phase == "recovery_failed" {
			s.Phase = t.Phase
			s.Error = t.Error
			s.UpdatedAt = t.UpdatedAt
		}
		if (t.Phase == "rolled_back" || t.Phase == "committed") && t.UpdatedAt.After(s.UpdatedAt) {
			s.Phase = t.Phase
			s.Error = t.Error
			s.UpdatedAt = t.UpdatedAt
		}
	}
	return s
}
func (m *Manager) SetAuto(enabled bool) error {
	if err := writeJSON(filepath.Join(m.cfg.Root, "settings.json"), map[string]bool{"auto_enabled": enabled}); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.AutoEnabled = enabled
	m.mu.Unlock()
	return nil
}
func (m *Manager) Close() { m.cancel(); m.operation.Lock(); m.operation.Unlock(); m.wg.Wait() }
func (m *Manager) setPhase(phase string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Phase = phase
	m.status.Error = ""
	if err != nil {
		m.status.Error = err.Error()
	}
	m.status.UpdatedAt = time.Now().UTC()
}
func (m *Manager) operationContext(ctx context.Context) (context.Context, func()) {
	c, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	return c, func() { stop(); cancel() }
}
func isPending(phase string) bool {
	return phase == "prepared" || phase == "installing" || phase == "rolling_back" || phase == "recovery_failed"
}
func (m *Manager) Check(ctx context.Context) (Status, error) {
	if !m.operation.TryLock() {
		return m.Status(), errors.New("update operation already running")
	}
	defer m.operation.Unlock()
	ctx, cancel := m.operationContext(ctx)
	defer cancel()
	err := m.check(ctx)
	return m.Status(), err
}
func (m *Manager) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := m.Status()
	if !s.Enabled {
		return errors.New(s.Error)
	}
	if isPending(s.Phase) {
		return errors.New("update transaction is pending")
	}
	m.setPhase("checking", nil)
	fail := func(e error) error { m.setPhase("error", e); return e }
	base, _ := url.Parse(m.cfg.BaseURL)
	endpoint := *base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/client/releases/latest"
	q := endpoint.Query()
	q.Set("app", m.cfg.App)
	q.Set("channel", m.cfg.Channel)
	q.Set("arch", m.cfg.Arch)
	q.Set("version", m.cfg.CurrentVersion)
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fail(err)
	}
	res, err := m.client.Do(req)
	if err != nil {
		return fail(errors.New("unable to contact update server"))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("update server returned HTTP %d", res.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return fail(errors.New("invalid update server response"))
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return fail(errors.New("invalid update response JSON"))
	}
	if len(envelope.Data) > 0 {
		if envelope.Code != 0 {
			return fail(errors.New("update server rejected request"))
		}
		data = envelope.Data
	}
	var response struct {
		Available   bool           `json:"available"`
		ID          string         `json:"release_id"`
		Manifest    SignedManifest `json:"manifest"`
		DownloadURL string         `json:"download_url"`
	}
	if err = json.Unmarshal(data, &response); err != nil {
		return fail(err)
	}
	var release *Release
	if response.Available {
		manifest, err := verifyManifest(response.Manifest, m.cfg)
		if err != nil {
			return fail(err)
		}
		download, err := url.Parse(response.DownloadURL)
		if err != nil {
			return fail(errors.New("invalid package URL"))
		}
		download = base.ResolveReference(download)
		if _, err = checkedURL(download.String(), m.cfg.AllowLoopbackHTTP); err != nil || !sameOrigin(base, download) {
			return fail(errors.New("package download must remain on the trusted origin"))
		}
		release = &Release{ID: response.ID, Manifest: manifest, DownloadURL: download.String()}
	}
	now := time.Now().UTC()
	m.mu.Lock()
	m.status.Available = response.Available
	m.status.Release = release
	m.status.LastCheckedAt = &now
	m.mu.Unlock()
	phase := "idle"
	if response.Available {
		phase = "available"
	}
	m.setPhase(phase, nil)
	return nil
}
func (m *Manager) Install(ctx context.Context) (Status, error) { return m.performInstall(ctx, false) }
func (m *Manager) performInstall(ctx context.Context, automatic bool) (Status, error) {
	if !m.operation.TryLock() {
		return m.Status(), errors.New("update operation already running")
	}
	defer m.operation.Unlock()
	ctx, cancel := m.operationContext(ctx)
	defer cancel()
	err := m.install(ctx, automatic)
	return m.Status(), err
}
func (m *Manager) install(ctx context.Context, automatic bool) (result error) {
	if err := m.check(ctx); err != nil {
		return err
	}
	s := m.Status()
	if s.Release == nil || !s.Available {
		return errors.New("no newer compatible release")
	}
	r := *s.Release
	if automatic {
		var failed struct {
			SHA256 string `json:"sha256"`
		}
		if b, e := os.ReadFile(filepath.Join(m.cfg.Root, "failed-release.json")); e == nil && json.Unmarshal(b, &failed) == nil && failed.SHA256 == r.Manifest.SHA256 {
			return errors.New("this release previously failed; automatic retries suppressed")
		}
	}
	id := randomID()
	prepared := false
	handedOff := false
	defer func() {
		if prepared && !handedOff && m.hooks.Resume != nil {
			m.hooks.Resume()
		}
		if result != nil {
			var pending transaction
			if b, e := os.ReadFile(filepath.Join(m.cfg.Root, "update-state.json")); e == nil && json.Unmarshal(b, &pending) == nil && pending.ID == id && pending.Phase == "prepared" {
				pending.Phase = "failed"
				pending.Error = result.Error()
				pending.UpdatedAt = time.Now().UTC()
				_ = writeJSON(filepath.Join(m.cfg.Root, "update-state.json"), pending)
			}
			m.setPhase("error", result)
			m.emit(Event{ReleaseID: r.ID, EventID: id, Version: r.Manifest.Version, FromVersion: m.cfg.CurrentVersion, Status: "failed", ErrorCode: "INSTALL_FAILED", OccurredAt: time.Now().UTC()})
		}
	}()
	lock, err := os.OpenFile(filepath.Join(m.cfg.Root, "update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another installer is running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	// Fixed bootstrap and helper live outside any downloaded release.
	for _, file := range []string{m.cfg.HelperPath, m.cfg.BootstrapCmd} {
		if !filepath.IsAbs(file) {
			return errors.New("bootstrap/helper path must be absolute")
		}
		info, e := os.Stat(file)
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("fixed update bootstrap is unavailable")
		}
	}
	if m.cfg.HealthURL == "" {
		return errors.New("update health probe is not configured")
	}
	m.setPhase("downloading", nil)
	target, err := m.downloadAndStage(ctx, r)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	m.setPhase("waiting_idle", nil)
	if m.hooks.Prepare == nil {
		return errors.New("update maintenance hooks are not configured")
	}
	if err = m.hooks.Prepare(ctx); err != nil {
		return fmt.Errorf("application could not become idle: %w", err)
	}
	prepared = true
	if err = ctx.Err(); err != nil {
		return err
	}
	// Preparation can wait for a long task. Revalidate publication immediately
	// before the transaction so a withdrawn/replaced release is not installed.
	if err = m.check(ctx); err != nil {
		return err
	}
	latest := m.Status().Release
	if latest == nil || latest.ID != r.ID || latest.Manifest.SHA256 != r.Manifest.SHA256 || latest.Manifest.Version != r.Manifest.Version {
		return errors.New("release was withdrawn or replaced while waiting")
	}
	previous := ""
	current := filepath.Join(m.cfg.Root, "current")
	if _, err = os.Lstat(current); err == nil {
		previous, err = filepath.EvalSymlinks(current)
		if err != nil {
			return errors.New("invalid current runtime pointer")
		}
		if !withinReleases(m.cfg.Root, previous) {
			return errors.New("current runtime is outside release storage")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	t := transaction{SHA256: r.Manifest.SHA256, ReleaseID: r.ID, ID: id, Phase: "prepared", Target: target, Previous: previous, Version: r.Manifest.Version, FromVersion: m.cfg.CurrentVersion, UpdatedAt: time.Now().UTC()}
	if err = writeJSON(filepath.Join(m.cfg.Root, "update-state.json"), t); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(m.cfg.Root, "updater.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	args := []string{m.cfg.HelperPath, "run", "--root", m.cfg.Root, "--bootstrap", m.cfg.BootstrapCmd, "--health-url", m.cfg.HealthURL}
	if m.cfg.HealthSocket != "" {
		args = append(args, "--health-socket", m.cfg.HealthSocket)
	}
	command := exec.Command("python3", args...)
	command.Stdout = log
	command.Stderr = log
	command.Stdin = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Helper acknowledges ownership of the durable transaction before stopping us.
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		return err
	}
	if err = command.Start(); err != nil {
		t.Phase = "failed"
		t.Error = "unable to start update helper"
		t.UpdatedAt = time.Now().UTC()
		_ = writeJSON(filepath.Join(m.cfg.Root, "update-state.json"), t)
		return errors.New(t.Error)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	acknowledged := func() bool {
		var current transaction
		b, e := os.ReadFile(filepath.Join(m.cfg.Root, "update-state.json"))
		return e == nil && json.Unmarshal(b, &current) == nil && current.ID == id && current.Phase != "prepared" && current.Phase != "failed"
	}
	for {
		if acknowledged() {
			handedOff = true
			m.setPhase("installing", nil)
			return nil
		}
		select {
		case <-done:
			if acknowledged() {
				handedOff = true
				return nil
			}
			return errors.New("update helper exited before taking ownership")
		case <-ctx.Done():
			if acknowledged() {
				handedOff = true
				return nil
			}
			_ = command.Process.Kill()
			return ctx.Err()
		case <-deadline.C:
			if acknowledged() {
				handedOff = true
				return nil
			}
			_ = command.Process.Kill()
			return errors.New("update helper acknowledgement timed out")
		case <-poll.C:
		}
	}
}
func withinReleases(root, target string) bool {
	rel, e := filepath.Rel(filepath.Join(root, "releases"), target)
	return e == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func (m *Manager) downloadAndStage(ctx context.Context, r Release) (string, error) {
	f, err := os.CreateTemp(m.cfg.Root, ".download-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.DownloadURL, nil)
	if err != nil {
		return "", err
	}
	res, err := m.client.Do(req)
	if err != nil {
		return "", errors.New("package download failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("package download HTTP %d", res.StatusCode)
	}
	if res.ContentLength > 0 && res.ContentLength != r.Manifest.Size {
		return "", errors.New("package size does not match manifest")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(res.Body, r.Manifest.Size+1))
	if err != nil {
		return "", errors.New("package download interrupted")
	}
	if n != r.Manifest.Size || hex.EncodeToString(hash.Sum(nil)) != r.Manifest.SHA256 {
		return "", errors.New("package hash or size mismatch")
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	m.setPhase("verifying", nil)
	staging, err := os.MkdirTemp(filepath.Join(m.cfg.Root, "releases"), ".staging-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err = extractArchive(f.Name(), staging, r.Manifest); err != nil {
		return "", err
	}
	target := filepath.Join(m.cfg.Root, "releases", r.Manifest.Version+"-"+r.Manifest.Arch+"-"+r.Manifest.SHA256[:16])
	if _, err = os.Lstat(target); err == nil {
		// Never trust/reuse old extracted content; only remove a non-current release with this identity.
		active, _ := filepath.EvalSymlinks(filepath.Join(m.cfg.Root, "current"))
		var previous transaction
		if b, e := os.ReadFile(filepath.Join(m.cfg.Root, "update-state.json")); e == nil {
			_ = json.Unmarshal(b, &previous)
		}
		if previous.Previous == target {
			return "", errors.New("target release is retained for rollback")
		}
		if active == target {
			return "", errors.New("target release is already active")
		}
		if err = os.RemoveAll(target); err != nil {
			return "", err
		}
	}
	if err = os.Rename(staging, target); err != nil {
		return "", err
	}
	if err = syncDirectory(filepath.Dir(target)); err != nil {
		return "", err
	}
	return target, nil
}
func writeJSON(file string, value any) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(bytes)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), file); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(file))
}
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure randomness unavailable")
	}
	return hex.EncodeToString(b[:])
}
func (m *Manager) emit(e Event) {
	if writeJSON(filepath.Join(m.cfg.Root, "events", e.EventID+".json"), e) == nil {
		m.flushEvents()
	}
}
func (m *Manager) flushEvents() {
	if m.hooks.Report == nil {
		return
	}
	files, _ := filepath.Glob(filepath.Join(m.cfg.Root, "events", "*.json"))
	for _, file := range files {
		data, e := os.ReadFile(file)
		if e != nil {
			continue
		}
		var event Event
		if json.Unmarshal(data, &event) != nil {
			continue
		}
		if m.hooks.Report(event) == nil {
			_ = os.Remove(file)
		}
	}
}
func (m *Manager) schedule() {
	defer m.wg.Done()
	if s := m.Status(); s.Enabled && s.AutoEnabled {
		_, _ = m.Check(m.ctx)
	}
	next := nextAutomatic(time.Now())
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			m.resumeRecoveredStartup()
			m.flushEvents()
			if now.Before(next) {
				continue
			}
			next = nextAutomatic(now)
			s := m.Status()
			if !s.Enabled || !s.AutoEnabled || isPending(s.Phase) {
				continue
			}
			// Busy/temporary failures are retried later, not by interrupting user operations.
			c, cancel := context.WithTimeout(m.ctx, 15*time.Minute)
			_, err := m.performInstall(c, true)
			cancel()
			if err != nil && m.Status().Available {
				next = now.Add(10 * time.Minute)
			}
		}
	}
}
func nextAutomatic(now time.Time) time.Time {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	local := now.In(zone)
	next := time.Date(local.Year(), local.Month(), local.Day(), 3, 0, 0, 0, zone)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	var b [2]byte
	_, _ = rand.Read(b[:])
	return next.Add(time.Duration((int(b[0])<<8|int(b[1]))%1800) * time.Second)
}

// NeedsStartupPause is fail-closed for a corrupt journal: starting business work
// in an uncommitted runtime would make automatic rollback unsafe.
func NeedsStartupPause(root, version string) bool {
	b, err := os.ReadFile(filepath.Join(root, "update-state.json"))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	var state transaction
	if json.Unmarshal(b, &state) != nil || state.ID == "" {
		return true
	}
	if isPending(state.Phase) {
		return true
	}
	switch state.Phase {
	case "committed", "rolled_back", "failed":
		return false
	default:
		return true
	}
}
func (m *Manager) resumeRecoveredStartup() {
	if !m.cfg.ResumeAfterRecovery || m.startupTransactionID == "" {
		return
	}
	var state transaction
	b, e := os.ReadFile(filepath.Join(m.cfg.Root, "update-state.json"))
	if e != nil || json.Unmarshal(b, &state) != nil || state.ID != m.startupTransactionID {
		return
	}
	committed := state.Phase == "committed" && state.Version == m.cfg.CurrentVersion
	rolledBack := state.Phase == "rolled_back" && state.FromVersion == m.cfg.CurrentVersion
	if (committed || rolledBack) && m.hooks.Resume != nil {
		m.hooks.Resume()
		m.startupTransactionID = ""
	}
}
