package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

var stages = []string{"fetch_source", "validate_account", "prepare_target", "transfer", "verify_transfer", "delete", "verify_cleanup"}

func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

type Stage struct {
	Stage    string  `json:"stage"`
	Status   string  `json:"status"`
	Duration int64   `json:"duration_ms"`
	Error    *string `json:"error_code,omitempty"`
}
type EventData struct {
	RunID          string     `json:"run_id"`
	AccountRef     string     `json:"account_ref,omitempty"`
	Provider       string     `json:"provider,omitempty"`
	Trigger        string     `json:"trigger,omitempty"`
	ScheduledFor   *time.Time `json:"scheduled_for,omitempty"`
	Attempt        int        `json:"attempt,omitempty"`
	SourceID       string     `json:"source_id,omitempty"`
	SourceVersion  string     `json:"source_version,omitempty"`
	AuthStatus     string     `json:"auth_status,omitempty"`
	Result         string     `json:"result,omitempty"`
	FailedStage    *string    `json:"failed_stage,omitempty"`
	Error          *string    `json:"error_code,omitempty"`
	Duration       int64      `json:"duration_ms"`
	CleanupPending bool       `json:"cleanup_pending"`
	DeletionMode   string     `json:"deletion_mode,omitempty"`
	Stages         []Stage    `json:"stages,omitempty"`
	Stage          string     `json:"stage,omitempty"`
	Status         string     `json:"status,omitempty"`
}
type Event struct {
	ID         string          `json:"event_id"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	AppVersion string          `json:"app_version"`
	Data       json.RawMessage `json:"data"`
}
type StoredEvent struct {
	Event
	InstallationID string    `json:"installation_id"`
	ReceivedAt     time.Time `json:"received_at"`
	Hash           string    `json:"hash,omitempty"`
}
type Run struct {
	ReportedPendingAt *time.Time           `json:"reported_pending_at"`
	TransferFailed    bool                 `json:"transfer_failed"`
	Unknown           bool                 `json:"unknown"`
	InstallationID    string               `json:"installation_id"`
	ID                string               `json:"run_id"`
	AccountRef        string               `json:"account_ref"`
	Provider          string               `json:"provider"`
	Trigger           string               `json:"trigger"`
	ScheduledFor      *time.Time           `json:"scheduled_for"`
	Attempt           int                  `json:"attempt"`
	SourceID          string               `json:"source_id"`
	SourceVersion     string               `json:"source_version"`
	AppVersion        string               `json:"app_version"`
	CreatedAt         time.Time            `json:"received_at"`
	StartedAt         *time.Time           `json:"started_at"`
	FinishedAt        *time.Time           `json:"finished_at"`
	OriginalResult    string               `json:"result"`
	FailedStage       *string              `json:"failed_stage"`
	Error             *string              `json:"error_code"`
	TerminalHash      string               `json:"terminal_hash,omitempty"`
	Stages            map[string]Stage     `json:"stages"`
	StageTimes        map[string]time.Time `json:"stage_times"`
	ExplicitCleanupAt *time.Time           `json:"explicit_cleanup_at"`
	FallbackCleanupAt *time.Time           `json:"fallback_cleanup_at"`
	RecoveryAt        *time.Time           `json:"recovery_at"`
	DeletionMode      string               `json:"deletion_mode"`
	TransferConfirmed bool                 `json:"transfer_confirmed"`
	CycleCompleted    bool                 `json:"cycle_completed"`
	CycleCompletedAt  *time.Time           `json:"cycle_completed_at"`
	CompletionMethod  string               `json:"completion_method"`
	CountedValue      int                  `json:"counted_value"`
	NotCountedReason  string               `json:"not_counted_reason"`
	CleanupPending    bool                 `json:"cleanup_pending"`
	DetailsExpired    bool                 `json:"details_expired"`
}

func allowedError(s *string) bool {
	return s == nil || oneOf(*s, "SOURCE_NOT_CONFIGURED", "SOURCE_DISABLED", "SOURCE_EXPIRED", "SOURCE_UNAVAILABLE", "SOURCE_INVALID_RESPONSE", "TEST_FILE_NOT_FOUND", "TEST_FILE_TOO_LARGE", "AUTH_EXPIRED", "AUTH_IDENTITY_MISMATCH", "RATE_LIMITED", "NETWORK_TIMEOUT", "PROVIDER_UNAVAILABLE", "INSUFFICIENT_SPACE", "TARGET_PREPARE_FAILED", "TRANSFER_FAILED", "TRANSFER_UNCONFIRMED", "DELETE_FAILED", "CLEANUP_UNCONFIRMED", "CLIENT_INTERRUPTED", "CONFIG_DISABLED", "CLOCK_SKEW", "LOCAL_STORAGE_FAILED")
}
func validateEvent(e Event, now time.Time, age int) (EventData, string, string) {
	var d EventData
	if !validID(e.ID) || !validVersion(e.AppVersion) || !validTime(e.OccurredAt) {
		return d, "INVALID_FIELD", "event_id/app_version/occurred_at"
	}
	if e.OccurredAt.Before(now.Add(-time.Duration(age) * 24 * time.Hour)) {
		return d, "EVENT_TOO_OLD", "occurred_at"
	}
	if e.OccurredAt.After(now.Add(5 * time.Minute)) {
		return d, "CLOCK_SKEW", "occurred_at"
	}
	fields := map[string][]string{
		"account_check_started":          {"run_id", "account_ref", "provider", "trigger", "scheduled_for", "attempt"},
		"account_check_stage_finished":   {"run_id", "stage", "status", "duration_ms", "error_code"},
		"account_check_finished":         {"run_id", "account_ref", "provider", "trigger", "scheduled_for", "attempt", "source_id", "source_version", "auth_status", "result", "failed_stage", "error_code", "duration_ms", "cleanup_pending", "deletion_mode", "stages"},
		"account_check_cleanup_finished": {"run_id", "result", "duration_ms", "error_code", "deletion_mode"},
	}
	allowed, ok := fields[e.Type]
	if !ok {
		return d, "EVENT_TYPE_NOT_SUPPORTED", "type"
	}
	if len(e.Data) > 16384 {
		return d, "EVENT_TOO_LARGE", "data"
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(e.Data, &raw) != nil {
		return d, "INVALID_FIELD", "data"
	}
	for k := range raw {
		if !oneOf(k, allowed...) {
			return d, "INVALID_FIELD", "data." + k
		}
	}
	for _, k := range allowed {
		if value, ok := raw[k]; !ok || (string(value) == "null" && !oneOf(k, "scheduled_for", "failed_stage", "error_code")) {
			return d, "INVALID_FIELD", "data." + k
		}
	}
	if decodeStrict(bytes.NewReader(e.Data), &d) != nil {
		return d, "INVALID_FIELD", "data"
	}
	if !validID(d.RunID) || d.Duration < 0 || d.Duration > 7*24*3600*1000 || !allowedError(d.Error) {
		return d, "INVALID_FIELD", "data"
	}
	if e.Type == "account_check_started" || e.Type == "account_check_finished" {
		if !validID(d.AccountRef) || !validProvider(d.Provider) || !oneOf(d.Trigger, "manual", "daily", "catch_up") || d.Attempt < 1 || d.Attempt > 1000 {
			return d, "INVALID_FIELD", "data.account_ref/provider/trigger/attempt"
		}
		if d.Trigger == "manual" {
			if d.ScheduledFor != nil {
				return d, "INVALID_FIELD", "data.scheduled_for"
			}
		} else {
			if d.ScheduledFor == nil || !d.ScheduledFor.Equal(midnight(*d.ScheduledFor)) || d.ScheduledFor.After(e.OccurredAt.Add(5*time.Minute)) {
				return d, "INVALID_FIELD", "data.scheduled_for"
			}
		}
	}
	if e.Type == "account_check_stage_finished" {
		if !oneOf(d.Stage, stages...) || !oneOf(d.Status, "passed", "failed", "unknown", "skipped") {
			return d, "INVALID_FIELD", "data.stage/status"
		}
	}
	if e.Type == "account_check_cleanup_finished" {
		if !oneOf(d.Result, "passed", "failed", "unknown") || !oneOf(d.DeletionMode, "recycle_bin", "permanent", "unknown") {
			return d, "INVALID_FIELD", "data.result/deletion_mode"
		}
	}
	if e.Type == "account_check_finished" {
		if !oneOf(d.Result, "passed", "failed", "incomplete", "skipped") || !oneOf(d.AuthStatus, "valid", "expired", "unknown") || !oneOf(d.DeletionMode, "recycle_bin", "permanent", "unknown") || len(d.Stages) != len(stages) || (d.SourceID != "" && !validID(d.SourceID)) || len(d.SourceVersion) > 32 || (d.FailedStage != nil && !oneOf(*d.FailedStage, stages...)) {
			return d, "INVALID_FIELD", "data"
		}
		seen := map[string]bool{}
		allPassed := true
		for _, s := range d.Stages {
			if !oneOf(s.Stage, stages...) || seen[s.Stage] || !oneOf(s.Status, "passed", "failed", "unknown", "skipped") || s.Duration < 0 || s.Duration > 7*24*3600*1000 || !allowedError(s.Error) {
				return d, "INVALID_FIELD", "data.stages"
			}
			seen[s.Stage] = true
			allPassed = allPassed && s.Status == "passed"
		}
		if d.Result == "passed" && (!allPassed || d.CleanupPending) {
			return d, "INCONSISTENT_RESULT", "data.result"
		}
	}
	return d, "", ""
}
func earlier(dst **time.Time, t time.Time) {
	if *dst == nil || t.Before(**dst) {
		v := t
		*dst = &v
	}
}
func stageMerge(r *Run, s Stage, t time.Time) {
	prior, ok := r.Stages[s.Stage]
	if !ok || s.Status == "passed" || (prior.Status != "passed" && t.After(r.StageTimes[s.Stage])) {
		r.Stages[s.Stage] = s
		r.StageTimes[s.Stage] = t
	}
}
func mergeEvent(r *Run, e Event, d EventData) error {
	if r.Stages == nil {
		r.Stages = map[string]Stage{}
		r.StageTimes = map[string]time.Time{}
	}
	if d.AccountRef != "" {
		if r.AccountRef != "" && (r.AccountRef != d.AccountRef || r.Provider != d.Provider || r.Trigger != d.Trigger || !sameTime(r.ScheduledFor, d.ScheduledFor) || r.Attempt != d.Attempt) {
			return errors.New("RUN_IDENTITY_CONFLICT")
		}
		r.AccountRef = d.AccountRef
		r.Provider = d.Provider
		r.Trigger = d.Trigger
		r.ScheduledFor = d.ScheduledFor
		r.Attempt = d.Attempt
		if r.AppVersion == "" {
			r.AppVersion = e.AppVersion
		}
	}
	switch e.Type {
	case "account_check_started":
		earlier(&r.StartedAt, e.OccurredAt)
	case "account_check_stage_finished":
		stageMerge(r, Stage{d.Stage, d.Status, d.Duration, d.Error}, e.OccurredAt)
		if d.Stage == "verify_cleanup" && d.Status == "passed" {
			earlier(&r.ExplicitCleanupAt, e.OccurredAt)
		}
	case "account_check_finished":
		semantic, _ := json.Marshal(d)
		h := digest(string(semantic))
		if r.TerminalHash != "" && r.TerminalHash != h {
			return errors.New("TERMINAL_CONFLICT")
		}
		r.TerminalHash = h
		if d.CleanupPending {
			earlier(&r.ReportedPendingAt, e.OccurredAt)
		}
		r.OriginalResult = d.Result
		r.FailedStage = d.FailedStage
		r.Error = d.Error
		r.SourceID = d.SourceID
		r.SourceVersion = d.SourceVersion
		r.DeletionMode = d.DeletionMode
		earlier(&r.FinishedAt, e.OccurredAt)
		for _, s := range d.Stages {
			stageMerge(r, s, e.OccurredAt)
			if s.Stage == "verify_cleanup" && s.Status == "passed" && !d.CleanupPending {
				earlier(&r.FallbackCleanupAt, e.OccurredAt)
			}
		}
	case "account_check_cleanup_finished":
		if d.Result == "passed" {
			earlier(&r.RecoveryAt, e.OccurredAt)
			r.DeletionMode = d.DeletionMode
			stageMerge(r, Stage{Stage: "delete", Status: "passed"}, e.OccurredAt)
			stageMerge(r, Stage{Stage: "verify_cleanup", Status: "passed", Duration: d.Duration}, e.OccurredAt)
		}
	}
	refreshRunOutcome(r)
	return nil
}

func refreshRunOutcome(r *Run) {
	r.TransferConfirmed = r.TransferConfirmed || (r.Stages["transfer"].Status == "passed" && r.Stages["verify_transfer"].Status == "passed")
	var completion *time.Time
	if r.ExplicitCleanupAt != nil {
		v := *r.ExplicitCleanupAt
		completion = &v
	} else if r.FallbackCleanupAt != nil {
		v := *r.FallbackCleanupAt
		completion = &v
	}
	method := "initial"
	if r.RecoveryAt != nil && (completion == nil || r.RecoveryAt.Before(*completion)) {
		v := *r.RecoveryAt
		completion = &v
		method = "cleanup_recovery"
	}
	pending := r.ReportedPendingAt != nil && (completion == nil || !completion.After(*r.ReportedPendingAt))
	if r.RecoveryAt != nil && r.ReportedPendingAt != nil && !r.RecoveryAt.Before(*r.ReportedPendingAt) {
		pending = false
	}
	clean := !pending && r.Stages["delete"].Status == "passed" && r.Stages["verify_cleanup"].Status == "passed" && completion != nil
	if r.AccountRef != "" && r.TransferConfirmed && clean {
		r.CycleCompleted = true
		r.CountedValue = 1
		r.CycleCompletedAt = completion
		r.CompletionMethod = method
		r.NotCountedReason = ""
		r.CleanupPending = false
	} else if !r.CycleCompleted {
		r.CountedValue = 0
		r.CleanupPending = (r.TransferConfirmed || pending) && !clean
		if r.AccountRef == "" {
			r.NotCountedReason = "WAITING_RUN_METADATA"
		} else if !r.TransferConfirmed {
			r.NotCountedReason = "TRANSFER_UNCONFIRMED"
		} else {
			r.NotCountedReason = "CLEANUP_UNCONFIRMED"
		}
	}
	r.TransferFailed = r.Stages["transfer"].Status == "failed"
	r.Unknown = !r.CycleCompleted && !r.TransferFailed && (r.Stages["verify_transfer"].Status != "passed" || r.Stages["verify_cleanup"].Status != "passed")
}
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

type Rejection struct {
	EventID   string `json:"event_id"`
	Code      string `json:"code"`
	Field     string `json:"field"`
	Retryable bool   `json:"retryable"`
}

func (a *app) events(c *gin.Context) {
	if !clientJSON(c) {
		return
	}
	var s Settings
	if err := a.store.db.View(func(tx *bolt.Tx) error { s = settings(tx); return nil }); err != nil {
		a.dbError(c, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, int64(s.BatchBytes)))
	if err != nil {
		fail(c, 413, "PAYLOAD_TOO_LARGE", "事件批次超过字节上限")
		return
	}
	var in struct {
		Schema  int     `json:"schema_version"`
		BatchID string  `json:"batch_id"`
		Events  []Event `json:"events"`
	}
	if decodeStrict(bytes.NewReader(raw), &in) != nil || in.Schema != 1 || !validID(in.BatchID) || len(in.Events) == 0 || len(in.Events) > s.BatchItems {
		fail(c, 400, "INVALID_BATCH", "批次格式、版本或事件数量不合法")
		return
	}
	ids := map[string]bool{}
	for _, e := range in.Events {
		if e.ID == "" || ids[e.ID] {
			fail(c, 400, "DUPLICATE_EVENT_ID", "同批次事件必须带非空且不重复的event_id")
			return
		}
		ids[e.ID] = true
	}
	accepted, duplicates := []string{}, []string{}
	rejected := []Rejection{}
	now := a.now()
	inst := c.GetString("installation_id")
	err = a.store.db.Update(func(tx *bolt.Tx) error {
		var identity Instance
		if !get(tx, "instances", inst, &identity) || identity.Revoked {
			return errors.New("instance unavailable")
		}
		for _, e := range in.Events {
			d, code, field := validateEvent(e, now, s.EventAge)
			if code != "" {
				rejected = append(rejected, Rejection{e.ID, code, field, false})
				continue
			}
			e.OccurredAt = e.OccurredAt.UTC()
			if d.ScheduledFor != nil {
				t := d.ScheduledFor.UTC()
				d.ScheduledFor = &t
			}
			sort.Slice(d.Stages, func(i, j int) bool { return d.Stages[i].Stage < d.Stages[j].Stage })
			normalized, _ := json.Marshal(d)
			e.Data = normalized
			canonical, _ := json.Marshal(e)
			hash := digest(string(canonical))
			key := inst + "|" + e.ID
			var old StoredEvent
			if get(tx, "events", key, &old) {
				if old.Hash == hash {
					duplicates = append(duplicates, e.ID)
				} else {
					rejected = append(rejected, Rejection{e.ID, "IDEMPOTENCY_CONFLICT", "event_id", false})
				}
				continue
			}
			keyRun := inst + "|" + d.RunID
			run := Run{InstallationID: inst, ID: d.RunID, CreatedAt: now}
			get(tx, "runs", keyRun, &run)
			if err := mergeEvent(&run, e, d); err != nil {
				rejected = append(rejected, Rejection{e.ID, err.Error(), "data.run_id", false})
				continue
			}
			if err := put(tx, "runs", keyRun, run); err != nil {
				return err
			}
			if err := put(tx, "events", key, StoredEvent{e, inst, now, hash}); err != nil {
				return err
			}
			if err := markRunning(tx, inst, e.OccurredAt); err != nil {
				return err
			}
			accepted = append(accepted, e.ID)
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 200, 0, "ok", gin.H{"accepted_event_ids": accepted, "duplicate_event_ids": duplicates, "rejected_events": rejected, "server_time": now})
}
func runEvidenceTime(r Run) time.Time {
	if r.StartedAt != nil {
		return *r.StartedAt
	}
	if r.FinishedAt != nil {
		return *r.FinishedAt
	}
	return r.CreatedAt
}
func (r Run) safe() Run { r.TerminalHash = ""; return r }
func sortRuns(rs []Run) {
	sort.Slice(rs, func(i, j int) bool {
		return strings.Compare(rs[i].InstallationID+"|"+rs[i].ID, rs[j].InstallationID+"|"+rs[j].ID) < 0
	})
}

var _ = fmt.Sprintf
