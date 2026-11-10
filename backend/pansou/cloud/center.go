package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"pansou/util/json"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func centerHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type centerError struct {
	Code       string
	Status     int
	RetryAfter time.Duration
}

func (e *centerError) Error() string {
	return fmt.Sprintf("管理中心返回 %s（HTTP %d）", e.Code, e.Status)
}
func (s *Service) centerRequest(ctx context.Context, method, route, token string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, s.centerBase+route, bytes.NewReader(data))
	if err != nil {
		return errors.New("管理中心请求地址无效")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.centerClient.Do(req)
	if err != nil {
		return errors.New("无法连接管理中心，请稍后重试")
	}
	defer resp.Body.Close()
	var envelope struct {
		Code json.RawMessage `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope) != nil {
		return errors.New("管理中心响应格式无效")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || string(envelope.Code) != "0" {
		code := "INVALID_RESPONSE"
		var parsed string
		if json.Unmarshal(envelope.Code, &parsed) == nil && len(parsed) < 80 {
			valid := true
			for _, ch := range parsed {
				if !(ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
					valid = false
				}
			}
			if valid {
				code = parsed
			}
		}
		return &centerError{Code: code, Status: resp.StatusCode, RetryAfter: centerRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	if out != nil {
		if err = json.Unmarshal(envelope.Data, out); err != nil {
			return errors.New("管理中心数据格式无效")
		}
	}
	return nil
}

// A remote limit postpones reporting; it never causes a burst of retries.
func centerRetryAfter(value string, at time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return time.Duration(min(seconds, 86400)) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil && until.After(at) {
		return min(until.Sub(at), 24*time.Hour)
	}
	return 0
}

func (s *Service) CenterStatus(owner string) (CenterStatus, error) {
	st, err := s.store.read(owner)
	if err != nil {
		return CenterStatus{}, err
	}
	c := st.Center
	secret, err := s.centerSecret(owner, c)
	if err != nil {
		return CenterStatus{}, err
	}
	return CenterStatus{Config: c.Config, Connected: secret["report_token"] != "", PendingEvents: len(c.Events), LastReportAt: c.LastReportAt, LastError: c.LastError, Sites: c.Sites, SitesUpdatedAt: c.SitesUpdatedAt, RejectedEvents: c.RejectedEvents, ReportError: c.ReportError}, nil
}
func (s *Service) SetCenterConfig(owner string, daily, reporting *bool) (CenterStatus, error) {
	s.centerMu.Lock()
	defer s.centerMu.Unlock()
	err := s.store.update(owner, func(st *userState) error {
		c := &st.Center
		if daily != nil && *daily != c.Config.DailyEnabled {
			c.Config.DailyEnabled = *daily
			if *daily {
				c.Config.EnabledSince = now()
			}
		}
		if reporting != nil {
			c.Config.ReportingEnabled = *reporting
			if !*reporting {
				c.Events = nil
			}
			c.LastReportAt = 0
			c.LastAttemptAt = 0
		}
		return nil
	})
	if err != nil {
		return CenterStatus{}, err
	}
	s.notify()
	return s.CenterStatus(owner)
}
func (s *Service) centerSecret(owner string, c centerRecord) (Credentials, error) {
	if c.Secret == "" {
		return Credentials{}, nil
	}
	return s.store.decrypt(owner, storedAccount{Account: Account{ID: "center"}, Secret: c.Secret})
}
func (s *Service) saveCenterSecret(owner string, secret Credentials) error {
	encrypted, err := s.store.encrypt(owner, "center", secret)
	if err != nil {
		return err
	}
	return s.store.update(owner, func(st *userState) error { st.Center.Secret = encrypted; return nil })
}
func (s *Service) centerRegister(ctx context.Context, owner string, c centerRecord) (Credentials, error) {
	secret, err := s.centerSecret(owner, c)
	if err != nil {
		return nil, err
	}
	if secret["report_token"] != "" {
		return secret, nil
	}
	if secret["client_instance_id"] == "" {
		nonce := make([]byte, 32)
		if _, err = rand.Read(nonce); err != nil {
			return nil, err
		}
		secret = Credentials{"client_instance_id": newID(), "registration_nonce": hex.EncodeToString(nonce)}
		if err = s.saveCenterSecret(owner, secret); err != nil {
			return nil, err
		}
	}
	var result struct {
		ID    string `json:"installation_id"`
		Token string `json:"report_token"`
	}
	err = s.centerRequest(ctx, "POST", "/api/client/register", "", map[string]any{"schema_version": 1, "client_instance_id": secret["client_instance_id"], "registration_nonce": secret["registration_nonce"], "app_version": centerAppVersion, "platform": "fnos", "arch": runtime.GOARCH}, &result)
	if err != nil {
		return nil, err
	}
	if result.ID == "" || result.Token == "" {
		return nil, errors.New("登记响应缺少凭证")
	}
	secret["installation_id"], secret["report_token"] = result.ID, result.Token
	if err = s.saveCenterSecret(owner, secret); err != nil {
		return nil, err
	}
	return secret, nil
}
func (s *Service) RefreshCenterSites(ctx context.Context, owner string) ([]CenterSite, error) {
	var sites []CenterSite
	if err := s.centerRequest(ctx, "GET", "/api/search-sites", "", nil, &sites); err != nil {
		return nil, err
	}
	if len(sites) > 200 {
		return nil, errors.New("站点配置超出限制")
	}
	seen := map[string]bool{}
	for _, site := range sites {
		if site.ID == "" || seen[site.ID] || len(site.ID) > 80 || len(site.Name) > 200 || len(site.URL) > 2048 {
			return nil, errors.New("站点配置无效")
		}
		seen[site.ID] = true
	}
	err := s.store.update(owner, func(st *userState) error { st.Center.Sites = sites; st.Center.SitesUpdatedAt = now(); return nil })
	return sites, err
}

// Read the public catalog independently of anonymous reporting. Keep the last
// valid catalog through an outage, including intentionally empty configurations.
func (s *Service) CenterSites(ctx context.Context, owner string) ([]CenterSite, bool, error) {
	st, err := s.store.read(owner)
	if err != nil {
		return nil, false, err
	}
	if st.Center.SitesUpdatedAt > 0 && now()-st.Center.SitesUpdatedAt < 60000 {
		return st.Center.Sites, false, nil
	}
	sites, err := s.RefreshCenterSites(ctx, owner)
	if err == nil {
		return sites, false, nil
	}
	if st.Center.SitesUpdatedAt > 0 {
		return st.Center.Sites, true, nil
	}
	return nil, false, err
}

func (s *Service) centerLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if s.ctx.Err() != nil {
			return
		}
		owners, err := s.store.owners()
		if err == nil {
			for _, owner := range owners {
				if s.ctx.Err() != nil {
					return
				}
				s.syncCenter(owner)
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) syncCenter(owner string) {
	s.centerMu.Lock()
	defer s.centerMu.Unlock()
	state, err := s.store.read(owner)
	if err != nil {
		return
	}
	c := state.Center
	if !c.Config.ReportingEnabled || now() < c.NextAttemptAt || now()-c.LastAttemptAt < 60000 {
		return
	}
	if err = s.store.update(owner, func(st *userState) error { st.Center.LastAttemptAt = now(); return nil }); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
	defer cancel()
	err = func() error {
		secret, err := s.centerRegister(ctx, owner, c)
		if err != nil {
			return err
		}
		token := secret["report_token"]
		policy := c.Policy
		if c.PolicyUpdatedAt == 0 || now()-c.PolicyUpdatedAt > 6*3600000 {
			var remote centerPolicy
			if e := s.centerRequest(ctx, "GET", "/api/client/config", token, nil, &remote); e != nil {
				var remoteErr *centerError
				if c.PolicyUpdatedAt == 0 || (errors.As(e, &remoteErr) && (remoteErr.Status == 401 || remoteErr.Status == 403 || remoteErr.Status == 429)) {
					return e
				}
			} else {
				compatible := false
				for _, v := range remote.Schemas {
					if v == 1 {
						compatible = true
					}
				}
				if !compatible {
					return errors.New("管理中心暂不支持当前上报协议")
				}
				remote.HeartbeatSeconds = max(60, min(3600, remote.HeartbeatSeconds))
				remote.BatchItems = max(1, min(100, remote.BatchItems))
				remote.BatchBytes = max(4096, min(262144, remote.BatchBytes))
				policy = remote
				if err = s.store.update(owner, func(st *userState) error { st.Center.Policy = policy; st.Center.PolicyUpdatedAt = now(); return nil }); err != nil {
					return err
				}
			}
		}
		if c.SitesUpdatedAt == 0 || now()-c.SitesUpdatedAt > 24*3600000 {
			_, _ = s.RefreshCenterSites(ctx, owner)
		}
		if now()-c.LastReportAt >= int64(max(60, policy.HeartbeatSeconds))*1000 {
			var snapshot userState
			if err = s.store.update(owner, func(st *userState) error { st.Center.SnapshotSeq++; snapshot = *st; return nil }); err != nil {
				return err
			}
			var receipt struct {
				AcceptedSeq *uint64 `json:"accepted_seq"`
				Applied     *bool   `json:"applied"`
			}
			if err = s.centerRequest(ctx, "POST", "/api/client/heartbeat", token, heartbeat(snapshot), &receipt); err != nil {
				return err
			}
			if receipt.AcceptedSeq == nil || *receipt.AcceptedSeq < snapshot.Center.SnapshotSeq {
				return errors.New("管理中心未确认当前心跳序号")
			}
			if *receipt.AcceptedSeq > snapshot.Center.SnapshotSeq || (receipt.Applied != nil && !*receipt.Applied) {
				if err = s.store.update(owner, func(st *userState) error {
					st.Center.SnapshotSeq = max(st.Center.SnapshotSeq, *receipt.AcceptedSeq)
					return nil
				}); err != nil {
					return err
				}
				return errors.New("管理中心已有更新的心跳序号，将以上一确认序号继续上报")
			}
			if err = s.store.update(owner, func(st *userState) error { st.Center.LastReportAt = now(); return nil }); err != nil {
				return err
			}
		}
		return s.sendCenterEvents(ctx, owner, token, policy)
	}()
	_ = s.store.update(owner, func(st *userState) error {
		st.Center.LastError = ""
		st.Center.NextAttemptAt = 0
		if err != nil {
			st.Center.LastError = err.Error()
			var remoteErr *centerError
			if errors.As(err, &remoteErr) && remoteErr.RetryAfter > 0 {
				st.Center.NextAttemptAt = now() + remoteErr.RetryAfter.Milliseconds()
			}
		}
		return nil
	})
}
func stamp(ms int64) any {
	if ms == 0 {
		return nil
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}
func checkAuth(a Account) string {
	if a.AuthCheckedAt == 0 {
		return "unknown"
	}
	if a.Status == "ready" {
		return "valid"
	}
	if a.Status == "expired" {
		return "expired"
	}
	return "unknown"
}
func heartbeat(st userState) map[string]any {
	accounts := []map[string]any{}
	ids := []string{}
	for id := range st.Accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	complete := len(ids) <= 30
	if len(ids) > 30 {
		ids = ids[:30]
	}
	for _, id := range ids {
		a := st.Accounts[id]
		var latest functionRecord
		pending := 0
		for _, r := range st.Checks {
			if r.AccountID != id {
				continue
			}
			if r.CleanupPending {
				pending++
			}
			if r.FinishedAt > latest.FinishedAt {
				latest = r
			}
		}
		var lastID, lastResult any
		if latest.FinishedAt > 0 {
			lastID = latest.ID
			lastResult = latest.Status
		}
		var next any
		since := max(st.Center.Config.EnabledSince, a.CreatedAt)
		if st.Center.Config.DailyEnabled {
			next = stamp(nextFunctionCheckAt(time.Now()).UnixMilli())
		}
		accounts = append(accounts, map[string]any{"account_ref": a.ID, "provider": a.Provider, "auth_status": checkAuth(a.Account), "auth_checked_at": stamp(a.AuthCheckedAt), "check_enabled": st.Center.Config.DailyEnabled, "check_enabled_since": stamp(since), "next_check_at": next, "last_check_run_id": lastID, "last_check_result": lastResult, "last_check_at": stamp(latest.FinishedAt), "cleanup_pending_count": pending})
	}
	enabled, activeJobs := 0, 0
	for _, sub := range st.Subscriptions {
		if sub.Enabled {
			enabled++
		}
	}
	for _, job := range st.Jobs {
		if active(job.Status) {
			activeJobs++
		}
	}
	for _, r := range st.Checks {
		if active(r.Status) {
			activeJobs++
		}
	}
	return map[string]any{"schema_version": 1, "snapshot_seq": st.Center.SnapshotSeq, "reported_at": stamp(now()), "app_version": centerAppVersion, "platform": "fnos", "arch": runtime.GOARCH, "accounts_complete": complete, "accounts": accounts, "subscriptions": map[string]int{"total": len(st.Subscriptions), "enabled": enabled}, "active_jobs": activeJobs}
}
func enqueueCenterEvent(st *userState, kind string, data map[string]any) {
	if !st.Center.Config.ReportingEnabled {
		return
	}
	if len(st.Center.Events) >= 1000 {
		st.Center.Events = st.Center.Events[1:]
		st.Center.RejectedEvents++
		st.Center.ReportError = "待报队列已满，最早事件已丢弃；本地检查记录保留"
	}
	st.Center.Events = append(st.Center.Events, centerEvent{ID: newID(), Type: kind, OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), AppVersion: centerAppVersion, Data: data})
}
func (s *Service) sendCenterEvents(ctx context.Context, owner, token string, policy centerPolicy) error {
	state, err := s.store.read(owner)
	if err != nil {
		return err
	}
	events := []centerEvent{}
	bytesUsed := 128
	for _, e := range state.Center.Events {
		encoded, _ := json.Marshal(e)
		if len(events) >= max(1, policy.BatchItems) || bytesUsed+len(encoded) > max(4096, policy.BatchBytes) {
			break
		}
		events = append(events, e)
		bytesUsed += len(encoded) + 1
	}
	if len(events) == 0 {
		return nil
	}
	var result struct {
		Accepted   []string `json:"accepted_event_ids"`
		Duplicates []string `json:"duplicate_event_ids"`
		Rejected   []struct {
			ID        string `json:"event_id"`
			Code      string `json:"code"`
			Field     string `json:"field"`
			Retryable bool   `json:"retryable"`
		} `json:"rejected_events"`
	}
	if err = s.centerRequest(ctx, "POST", "/api/client/events/batch", token, map[string]any{"schema_version": 1, "batch_id": newID(), "events": events}, &result); err != nil {
		return err
	}
	sent := map[string]bool{}
	for _, e := range events {
		sent[e.ID] = true
	}
	ack := map[string]bool{}
	seen := map[string]bool{}
	add := func(id string) bool {
		if !sent[id] || seen[id] {
			return false
		}
		seen[id] = true
		return true
	}
	for _, id := range append(result.Accepted, result.Duplicates...) {
		if !add(id) {
			return errors.New("上报确认列表无效，保留待报记录")
		}
		ack[id] = true
	}
	rejected := 0
	reason := ""
	for _, r := range result.Rejected {
		if !add(r.ID) {
			return errors.New("上报拒收列表无效，保留待报记录")
		}
		if !r.Retryable {
			ack[r.ID] = true
			rejected++
		}
		reason = fmt.Sprintf("事件被拒收：%.80s（%.100s）", r.Code, r.Field)
	}
	if len(seen) != len(sent) {
		return errors.New("管理中心未完整确认本批事件，保留待报记录")
	}
	return s.store.update(owner, func(st *userState) error {
		kept := []centerEvent{}
		for _, e := range st.Center.Events {
			if !ack[e.ID] {
				kept = append(kept, e)
			}
		}
		st.Center.Events = kept
		st.Center.RejectedEvents += rejected
		if reason != "" {
			st.Center.ReportError = reason
		}
		return nil
	})
}
