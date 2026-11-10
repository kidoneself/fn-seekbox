package cloud

import "context"

const checkFolderPrefix = ".seekbox-check-"
const centerAppVersion = "1.5.17"
const defaultCenterURL = "https://hermes.smartloan.cn"

type CheckStage struct {
	Stage      string `json:"stage"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	ErrorCode  string `json:"error_code,omitempty"`
}
type FunctionCheck struct {
	ID             string       `json:"id"`
	AccountID      string       `json:"account_id"`
	Provider       string       `json:"provider"`
	Status         string       `json:"status"`
	Trigger        string       `json:"trigger"`
	ScheduledFor   int64        `json:"scheduled_for"`
	CreatedAt      int64        `json:"created_at"`
	StartedAt      int64        `json:"started_at"`
	FinishedAt     int64        `json:"finished_at"`
	Stages         []CheckStage `json:"stages"`
	Error          string       `json:"error"`
	ErrorCode      string       `json:"error_code"`
	AuthStatus     string       `json:"auth_status"`
	SourceID       string       `json:"source_id"`
	SourceVersion  string       `json:"source_version"`
	CleanupPending bool         `json:"cleanup_pending"`
	TargetName     string       `json:"target_name,omitempty"`
	CleanupStatus  string       `json:"cleanup_status,omitempty"`
}
type checkObject struct {
	ID    string
	Name  string
	Size  int64
	IsDir bool
}
type checkFileManager interface {
	checkFiles(context.Context, Credentials, string) ([]checkObject, error)
	deleteCheckObject(context.Context, Credentials, string, checkObject) error
}
type functionRecord struct {
	DeleteSubmitted bool `json:"delete_submitted"`

	FunctionCheck
	TargetID          string      `json:"target_id"`
	TargetObject      checkObject `json:"target_object"`
	SavedObject       checkObject `json:"saved_object"`
	Submitted         bool        `json:"submitted"`
	TransferConfirmed bool        `json:"transfer_confirmed"`
	CleanupRequested  bool        `json:"cleanup_requested"`
}
type CheckConfig struct {
	DailyEnabled     bool  `json:"daily_enabled"`
	ReportingEnabled bool  `json:"reporting_enabled"`
	EnabledSince     int64 `json:"enabled_since"`
}
type CenterSite struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Order   int    `json:"order"`
}
type CenterStatus struct {
	Config         CheckConfig  `json:"config"`
	Connected      bool         `json:"connected"`
	PendingEvents  int          `json:"pending_events"`
	LastReportAt   int64        `json:"last_report_at"`
	LastError      string       `json:"last_error"`
	Sites          []CenterSite `json:"sites"`
	SitesUpdatedAt int64        `json:"sites_updated_at"`
	RejectedEvents int          `json:"rejected_events"`
	ReportError    string       `json:"report_error"`
}
type centerEvent struct {
	ID         string         `json:"event_id"`
	Type       string         `json:"type"`
	OccurredAt string         `json:"occurred_at"`
	AppVersion string         `json:"app_version"`
	Data       map[string]any `json:"data"`
}
type centerRecord struct {
	Config          CheckConfig      `json:"config"`
	Secret          string           `json:"encrypted_secret"`
	LastReportAt    int64            `json:"last_report_at"`
	LastError       string           `json:"last_error"`
	SnapshotSeq     uint64           `json:"snapshot_seq"`
	Events          []centerEvent    `json:"events"`
	Sites           []CenterSite     `json:"sites"`
	SitesUpdatedAt  int64            `json:"sites_updated_at"`
	LastSlots       map[string]int64 `json:"last_slots"`
	Policy          centerPolicy     `json:"policy"`
	PolicyUpdatedAt int64            `json:"policy_updated_at"`
	NextAttemptAt   int64            `json:"next_attempt_at"`
	LastAttemptAt   int64            `json:"last_attempt_at"`
	RejectedEvents  int              `json:"rejected_events"`
	ReportError     string           `json:"report_error"`
}
type centerPolicy struct {
	Schemas          []int `json:"accepted_schema_versions"`
	HeartbeatSeconds int   `json:"heartbeat_interval_seconds"`
	BatchItems       int   `json:"event_batch_max_items"`
	BatchBytes       int   `json:"event_batch_max_bytes"`
}
