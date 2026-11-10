package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
)

var telemetryBuckets = []string{"instances", "instance_clients", "report_tokens", "events", "runs", "accounts", "running_days", "slots", "settings", "admin_keys", "admin_key_hashes", "audit", "site_tombstones", "releases", "release_reports", "release_observations"}

func (s *Store) initTelemetry() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range telemetryBuckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		// Earlier versions omitted cleanup-pending runs before transfer confirmation.
		// Repair that derived flag once; preserve every event and counted cycle.
		const migration = "migration_cleanup_pending_v1"
		if bucket(tx, "settings").Get([]byte(migration)) == nil {
			if err := walk[Run](tx, "runs", func(key string, r Run) error {
				if r.CycleCompleted || r.DetailsExpired {
					return nil
				}
				updated := r
				refreshRunOutcome(&updated)
				if updated.CleanupPending == r.CleanupPending {
					return nil
				}
				r.CleanupPending = updated.CleanupPending
				return put(tx, "runs", key, r)
			}); err != nil {
				return err
			}
			return put(tx, "settings", migration, true)
		}
		return nil
	})
}
func bucket(tx *bolt.Tx, name string) *bolt.Bucket { return tx.Bucket([]byte(name)) }
func put(tx *bolt.Tx, b, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return bucket(tx, b).Put([]byte(key), raw)
}
func get(tx *bolt.Tx, b, key string, value any) bool {
	raw := bucket(tx, b).Get([]byte(key))
	return raw != nil && json.Unmarshal(raw, value) == nil
}
func rows[T any](tx *bolt.Tx, b string) []T {
	out := []T{}
	bucket(tx, b).ForEach(func(k, v []byte) error {
		var item T
		if v != nil && json.Unmarshal(v, &item) == nil {
			out = append(out, item)
		}
		return nil
	})
	return out
}
func randomID(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable")
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func day(t time.Time) string { return t.In(beijing).Format("2006-01-02") }
func midnight(t time.Time) time.Time {
	t = t.In(beijing)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, beijing)
}
func nextVersion(s string) string { n, _ := strconv.Atoi(s); return strconv.Itoa(n + 1) }
func validID(s string) bool       { return len(s) > 0 && len(s) <= 80 && siteIDPattern.MatchString(s) }
func validVersion(s string) bool {
	return len(s) > 0 && len(s) <= 48 && !strings.ContainsAny(s, " /\\:@\n\r\t")
}
func validTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 2020 && t.Year() <= 2200 }
func legacyErrorCode(n int) string {
	m := map[int]string{40001: "INVALID_FIELD", 40101: "UNAUTHORIZED", 40301: "FORBIDDEN", 40401: "NOT_FOUND", 40501: "METHOD_NOT_ALLOWED", 40901: "CONFLICT", 41501: "UNSUPPORTED_MEDIA_TYPE", 42901: "RATE_LIMITED", 50001: "INTERNAL_ERROR", 50301: "SOURCE_NOT_CONFIGURED", 50302: "SERVICE_UNAVAILABLE"}
	if s := m[n]; s != "" {
		return s
	}
	return "INTERNAL_ERROR"
}

type Settings struct {
	ConfigVersion    string `json:"config_version"`
	Heartbeat        int    `json:"heartbeat_interval_seconds"`
	BatchItems       int    `json:"event_batch_max_items"`
	BatchBytes       int    `json:"event_batch_max_bytes"`
	EventAge         int    `json:"event_max_age_days"`
	Retention        int    `json:"raw_retention_days"`
	SummaryRetention int    `json:"summary_retention_days"`
}

func settings(tx *bolt.Tx) Settings {
	s := Settings{ConfigVersion: "1", Heartbeat: 600, BatchItems: 100, BatchBytes: 262144, EventAge: 7, Retention: 90, SummaryRetention: 365}
	get(tx, "settings", "client", &s)
	return s
}

type Instance struct {
	ID            string        `json:"installation_id"`
	ClientID      string        `json:"client_instance_id"`
	NonceHash     string        `json:"nonce_hash,omitempty"`
	TokenHash     string        `json:"token_hash,omitempty"`
	CachedToken   string        `json:"cached_token,omitempty"`
	RetryUntil    time.Time     `json:"retry_until"`
	RegisteredAt  time.Time     `json:"first_registered_at"`
	FirstVersion  string        `json:"first_app_version"`
	AppVersion    string        `json:"app_version"`
	Platform      string        `json:"platform"`
	Arch          string        `json:"arch"`
	Revoked       bool          `json:"revoked"`
	Seq           uint64        `json:"snapshot_seq"`
	SnapshotHash  string        `json:"snapshot_hash,omitempty"`
	ReportedAt    *time.Time    `json:"reported_at"`
	ReceivedAt    *time.Time    `json:"received_at"`
	LastOnline    *time.Time    `json:"last_valid_heartbeat_at"`
	Subscriptions Subscriptions `json:"subscriptions"`
	ActiveJobs    int           `json:"active_jobs"`
	ClockWarning  bool          `json:"clock_warning"`
}
type Subscriptions struct {
	Total   int `json:"total"`
	Enabled int `json:"enabled"`
}
type AccountSnapshot struct {
	Ref            string     `json:"account_ref"`
	Provider       string     `json:"provider"`
	AuthStatus     string     `json:"auth_status"`
	AuthCheckedAt  *time.Time `json:"auth_checked_at"`
	CheckEnabled   bool       `json:"check_enabled"`
	EnabledSince   *time.Time `json:"check_enabled_since"`
	NextCheckAt    *time.Time `json:"next_check_at"`
	LastRunID      string     `json:"last_check_run_id"`
	LastResult     string     `json:"last_check_result"`
	LastCheckAt    *time.Time `json:"last_check_at"`
	CleanupPending int        `json:"cleanup_pending_count"`
}
type AccountChange struct {
	At      time.Time `json:"effective_at"`
	Enabled bool      `json:"enabled"`
	Removed bool      `json:"removed"`
}
type Account struct {
	AccountSnapshot
	InstallationID string          `json:"installation_id"`
	FirstSeen      time.Time       `json:"first_seen_at"`
	ObservedAt     time.Time       `json:"observed_at"`
	Removed        bool            `json:"removed"`
	History        []AccountChange `json:"check_history"`
}
type RunningDay struct {
	InstallationID string `json:"installation_id"`
	Date           string `json:"date"`
}
type Slot struct {
	InstallationID string    `json:"installation_id"`
	AccountRef     string    `json:"account_ref"`
	Provider       string    `json:"provider"`
	ScheduledFor   time.Time `json:"scheduled_for"`
	Estimated      bool      `json:"estimated"`
}

func markRunning(tx *bolt.Tx, id string, at time.Time) error {
	return put(tx, "running_days", day(at)+"|"+id, RunningDay{id, day(at)})
}
func safeInstance(i Instance) Instance {
	i.NonceHash = ""
	i.TokenHash = ""
	i.CachedToken = ""
	i.SnapshotHash = ""
	return i
}
func (a *app) seal(s string) (string, error) {
	key := sha256.Sum256(a.passwordHash)
	b, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return "", err
	}
	n := make([]byte, g.NonceSize())
	if _, err = io.ReadFull(rand.Reader, n); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(g.Seal(n, n, []byte(s), nil)), nil
}
func (a *app) unseal(s string) (string, error) {
	key := sha256.Sum256(a.passwordHash)
	b, _ := aes.NewCipher(key[:])
	g, _ := cipher.NewGCM(b)
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < g.NonceSize() {
		return "", errors.New("expired registration response")
	}
	out, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
	return string(out), err
}
func (a *app) limited(key string, max int, window time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for k, v := range a.loginAttempts {
		if now.Sub(v.Start) > 15*time.Minute {
			delete(a.loginAttempts, k)
		}
	}
	v := a.loginAttempts[key]
	if v.Start.IsZero() || now.Sub(v.Start) >= window {
		v = loginWindow{Start: now}
	}
	v.Count++
	a.loginAttempts[key] = v
	return v.Count > max
}
func (a *app) publicLimit(c *gin.Context) {
	if a.limited("public:"+c.ClientIP(), 600, time.Minute) {
		c.Header("Retry-After", "60")
		fail(c, 429, "RATE_LIMITED", "请求过于频繁")
		return
	}
	c.Next()
}
func clientJSON(c *gin.Context) bool {
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		fail(c, 415, "UNSUPPORTED_MEDIA_TYPE", "Content-Type 必须为 application/json")
		return false
	}
	return true
}
func (a *app) registerClient(c *gin.Context) {
	if !clientJSON(c) {
		return
	}
	if a.limited("register:"+c.ClientIP(), 30, time.Minute) {
		c.Header("Retry-After", "60")
		fail(c, 429, "RATE_LIMITED", "登记请求过于频繁")
		return
	}
	var in struct {
		Schema   int    `json:"schema_version"`
		ClientID string `json:"client_instance_id"`
		Nonce    string `json:"registration_nonce"`
		Version  string `json:"app_version"`
		Platform string `json:"platform"`
		Arch     string `json:"arch"`
	}
	if !readBody(c, &in) {
		return
	}
	nonce, err := base64.RawURLEncoding.DecodeString(in.Nonce)
	if err != nil {
		nonce, err = hex.DecodeString(in.Nonce)
	}
	if in.Schema != 1 || !validID(in.ClientID) || err != nil || len(nonce) < 32 || len(nonce) > 128 || !validVersion(in.Version) || in.Platform != "fnos" || (in.Arch != "amd64" && in.Arch != "arm64") {
		fail(c, 400, "INVALID_FIELD", "需要 schema_version=1、匿名实例ID、至少32随机字节的base64url/hex登记秘密、版本、fnos及amd64/arm64")
		return
	}
	now := a.now()
	var inst Instance
	var token string
	code := ""
	err = a.store.db.Update(func(tx *bolt.Tx) error {
		id := string(bucket(tx, "instance_clients").Get([]byte(in.ClientID)))
		if id != "" {
			if !get(tx, "instances", id, &inst) {
				return errors.New("instance index inconsistent")
			}
			if inst.NonceHash != digest(in.Nonce) {
				code = "REGISTRATION_CONFLICT"
				return nil
			}
			if inst.Revoked {
				code = "CREDENTIAL_REVOKED"
				return nil
			}
			if !now.Before(inst.RetryUntil) || inst.CachedToken == "" {
				code = "REGISTRATION_RETRY_EXPIRED"
				return nil
			}
			var e error
			token, e = a.unseal(inst.CachedToken)
			if e != nil {
				code = "REGISTRATION_RETRY_EXPIRED"
			}
			return nil
		}
		token = randomID("sri_")
		cached, e := a.seal(token)
		if e != nil {
			return e
		}
		inst = Instance{ID: randomID("inst_"), ClientID: in.ClientID, NonceHash: digest(in.Nonce), TokenHash: digest(token), CachedToken: cached, RetryUntil: now.Add(10 * time.Minute), RegisteredAt: now, FirstVersion: in.Version, AppVersion: in.Version, Platform: in.Platform, Arch: in.Arch}
		if e = put(tx, "instances", inst.ID, inst); e != nil {
			return e
		}
		if e = bucket(tx, "instance_clients").Put([]byte(in.ClientID), []byte(inst.ID)); e != nil {
			return e
		}
		return bucket(tx, "report_tokens").Put([]byte(inst.TokenHash), []byte(inst.ID))
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if code != "" {
		fail(c, 409, code, "登记冲突、凭证已撤销或重试窗口已过期；请保留原凭证或显式重置客户端匿名身份后重新登记")
		return
	}
	reply(c, 200, 0, "ok", gin.H{"installation_id": inst.ID, "report_token": token, "token_expires_at": nil})
}
func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || strings.ContainsAny(strings.TrimPrefix(h, "Bearer "), " \t\n\r") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}
func (a *app) reportAuth(c *gin.Context) {
	token := bearer(c)
	var inst Instance
	err := a.store.db.View(func(tx *bolt.Tx) error {
		id := string(bucket(tx, "report_tokens").Get([]byte(digest(token))))
		if id != "" {
			get(tx, "instances", id, &inst)
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if token == "" || inst.ID == "" || inst.Revoked {
		fail(c, 401, "UNAUTHORIZED", "上报凭证无效或已撤销")
		return
	}
	if a.limited("report:"+inst.ID, 120, time.Minute) {
		c.Header("Retry-After", "60")
		fail(c, 429, "RATE_LIMITED", "上报过于频繁")
		return
	}
	c.Set("installation_id", inst.ID)
	c.Set("actor", "instance:"+inst.ID)
	c.Next()
}
func (a *app) clientConfig(c *gin.Context) {
	var s Settings
	err := a.store.db.View(func(tx *bolt.Tx) error { s = settings(tx); return nil })
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 200, 0, "ok", gin.H{"config_version": s.ConfigVersion, "server_time": a.now(), "accepted_schema_versions": []int{1}, "heartbeat_interval_seconds": s.Heartbeat, "event_batch_max_items": s.BatchItems, "event_batch_max_bytes": s.BatchBytes, "event_max_age_days": s.EventAge, "check_schedule": gin.H{"timezone": "Asia/Shanghai", "local_time": "00:00", "catch_up": "latest_due_only"}})
}
func (a *app) heartbeat(c *gin.Context) {
	if !clientJSON(c) {
		return
	}
	var in struct {
		Schema        int               `json:"schema_version"`
		Seq           uint64            `json:"snapshot_seq"`
		ReportedAt    time.Time         `json:"reported_at"`
		Version       string            `json:"app_version"`
		Platform      string            `json:"platform"`
		Arch          string            `json:"arch"`
		Complete      *bool             `json:"accounts_complete"`
		Accounts      []AccountSnapshot `json:"accounts"`
		Subscriptions Subscriptions     `json:"subscriptions"`
		ActiveJobs    int               `json:"active_jobs"`
	}
	if !readBody(c, &in) {
		return
	}
	if in.Schema != 1 || in.Seq == 0 || !validTime(in.ReportedAt) || !validVersion(in.Version) || in.Platform != "fnos" || (in.Arch != "amd64" && in.Arch != "arm64") || in.Complete == nil || in.Accounts == nil || len(in.Accounts) > 30 || in.ActiveJobs < 0 || in.ActiveJobs > 10000 || in.Subscriptions.Total < 0 || in.Subscriptions.Enabled < 0 || in.Subscriptions.Enabled > in.Subscriptions.Total {
		fail(c, 400, "INVALID_FIELD", "心跳格式、序号、时间、账号或数量不合法")
		return
	}
	seen := map[string]bool{}
	for _, v := range in.Accounts {
		if !validID(v.Ref) || seen[v.Ref] || !validProvider(v.Provider) || (v.AuthStatus != "valid" && v.AuthStatus != "expired" && v.AuthStatus != "unknown") || v.CleanupPending < 0 || v.CleanupPending > 10000 || (v.LastRunID != "" && !validID(v.LastRunID)) || !oneOf(v.LastResult, "", "passed", "failed", "incomplete", "skipped") {
			fail(c, 400, "INVALID_FIELD", "账号字段、网盘或状态不合法")
			return
		}
		for _, t := range []*time.Time{v.AuthCheckedAt, v.EnabledSince, v.LastCheckAt} {
			if t != nil && (!validTime(*t) || t.After(in.ReportedAt.Add(5*time.Minute))) {
				fail(c, 400, "INVALID_FIELD", "账号历史时间不合法")
				return
			}
		}
		seen[v.Ref] = true
	}
	raw, _ := json.Marshal(in)
	hash := digest(string(raw))
	now := a.now()
	fresh := now.Sub(in.ReportedAt) <= 5*time.Minute && in.ReportedAt.Sub(now) <= 5*time.Minute
	id := c.GetString("installation_id")
	applied := false
	accepted := uint64(0)
	conflict := false
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		var inst Instance
		if !get(tx, "instances", id, &inst) || inst.Revoked {
			return errors.New("instance unavailable")
		}
		accepted = inst.Seq
		if in.Seq < inst.Seq {
			return nil
		}
		if in.Seq == inst.Seq {
			conflict = inst.SnapshotHash != hash
			return nil
		}
		inst.Seq = in.Seq
		inst.SnapshotHash = hash
		inst.ReportedAt = &in.ReportedAt
		inst.ReceivedAt = &now
		inst.ClockWarning = !fresh
		inst.AppVersion = in.Version
		inst.Platform = in.Platform
		inst.Arch = in.Arch
		inst.Subscriptions = in.Subscriptions
		inst.ActiveJobs = in.ActiveJobs
		if fresh {
			inst.LastOnline = &now
			if err := markRunning(tx, id, in.ReportedAt); err != nil {
				return err
			}
		}
		for _, v := range in.Accounts {
			key := id + "|" + v.Ref
			var acc Account
			exists := get(tx, "accounts", key, &acc)
			if exists && acc.Provider != v.Provider {
				return fmt.Errorf("account provider conflict")
			}
			if !exists {
				acc = Account{InstallationID: id, FirstSeen: now, History: []AccountChange{}}
			}
			if !exists || acc.CheckEnabled != v.CheckEnabled || acc.Removed {
				acc.History = append(acc.History, AccountChange{At: now, Enabled: v.CheckEnabled})
			}
			acc.AccountSnapshot = v
			acc.ObservedAt = now
			acc.Removed = false
			if err := put(tx, "accounts", key, acc); err != nil {
				return err
			}
		}
		if *in.Complete {
			for _, acc := range rows[Account](tx, "accounts") {
				if acc.InstallationID == id && !seen[acc.Ref] && !acc.Removed {
					acc.Removed = true
					acc.History = append(acc.History, AccountChange{At: now, Removed: true})
					if err := put(tx, "accounts", id+"|"+acc.Ref, acc); err != nil {
						return err
					}
				}
			}
		}
		applied = true
		accepted = in.Seq
		return put(tx, "instances", id, inst)
	})
	if err != nil {
		if strings.Contains(err.Error(), "provider conflict") {
			fail(c, 409, "ACCOUNT_CONFLICT", "同一匿名账号引用不能更换网盘")
		} else {
			a.dbError(c, err)
		}
		return
	}
	if conflict {
		fail(c, 409, "SNAPSHOT_CONFLICT", "相同快照序号不能携带不同内容")
		return
	}
	reply(c, 200, 0, "ok", gin.H{"accepted_seq": accepted, "applied": applied, "server_time": now, "clock_warning": !fresh})
}
func (a *app) maintenance(ctx context.Context) {
	a.maintain()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.maintain()
		}
	}
}
func (a *app) maintain() {
	now := a.now()
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		s := settings(tx)
		for _, inst := range rows[Instance](tx, "instances") {
			if inst.CachedToken != "" && !now.Before(inst.RetryUntil) {
				inst.CachedToken = ""
				if e := put(tx, "instances", inst.ID, inst); e != nil {
					return e
				}
			}
		}
		var last string
		get(tx, "settings", "last_maintenance_day", &last)
		if last == day(now) {
			return nil
		}
		start := midnight(now).AddDate(0, 0, -s.EventAge)
		if last != "" {
			if d, e := time.ParseInLocation("2006-01-02", last, beijing); e == nil && d.After(start) {
				start = d
			}
		}
		for _, acc := range rows[Account](tx, "accounts") {
			for d := start; !d.After(midnight(now)); d = d.AddDate(0, 0, 1) {
				if acc.FirstSeen.After(d) {
					continue
				}
				enabled, removed := false, false
				for _, change := range acc.History {
					if !change.At.After(d) {
						enabled = change.Enabled
						removed = change.Removed
					}
				}
				if enabled && !removed {
					key := day(d) + "|" + acc.InstallationID + "|" + acc.Ref
					if bucket(tx, "slots").Get([]byte(key)) == nil {
						if err := put(tx, "slots", key, Slot{acc.InstallationID, acc.Ref, acc.Provider, d, true}); err != nil {
							return err
						}
					}
				}
			}
		}
		for _, name := range []string{"events", "audit"} {
			b := bucket(tx, name)
			cur := b.Cursor()
			for k, v := cur.First(); k != nil; k, v = cur.Next() {
				var at struct {
					ReceivedAt time.Time `json:"received_at"`
				}
				json.Unmarshal(v, &at)
				if !at.ReceivedAt.IsZero() && at.ReceivedAt.Before(now.AddDate(0, 0, -s.Retention)) {
					if e := cur.Delete(); e != nil {
						return e
					}
				}
			}
		}
		if err := walk[Run](tx, "runs", func(k string, r Run) error {
			if !r.DetailsExpired && r.CreatedAt.Before(now.AddDate(0, 0, -s.Retention)) {
				r.Stages = nil
				r.StageTimes = nil
				r.DetailsExpired = true
				return put(tx, "runs", k, r)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, name := range []string{"running_days", "slots"} {
			cur := bucket(tx, name).Cursor()
			for k, _ := cur.First(); k != nil; k, _ = cur.Next() {
				if len(k) >= 10 && string(k[:10]) < day(now.AddDate(0, 0, -s.SummaryRetention)) {
					if e := cur.Delete(); e != nil {
						return e
					}
				}
			}
		}
		return put(tx, "settings", "last_maintenance_day", day(now))
	})
	if err != nil {
		log.Printf("maintenance failed: %v", err)
	}
}
