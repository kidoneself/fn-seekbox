// Package update installs signed, versioned application releases without touching user data.
package update

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"time"
)

const BootstrapVersion = 1
const DataSchema = 1
const MaxArchiveSize int64 = 256 << 20
const MaxExpandedSize int64 = 512 << 20

type Config struct {
	Root, CurrentVersion, App, Channel, Arch, BaseURL string
	PublicKeys                                        map[string]ed25519.PublicKey
	HelperPath, BootstrapCmd, HealthURL, HealthSocket string
	AllowLoopbackHTTP                                 bool
	HTTPClient                                        *http.Client
	AutoEnabled                                       *bool
	// DisableScheduler is for isolated tests; production starts checks automatically.
	DisableScheduler bool
	// Set only when this new backend instance started with cloud workers paused.
	ResumeAfterRecovery bool
}
type Hooks struct {
	// Prepare must undo its own partial maintenance state when returning an error.
	Prepare func(context.Context) error
	Resume  func()
	// Report must durably enqueue before returning; the helper's event file is then removed.
	Report func(Event) error
}
type Event struct {
	ReleaseID   string    `json:"release_id"`
	EventID     string    `json:"event_id"`
	Version     string    `json:"version"`
	FromVersion string    `json:"from_version"`
	Status      string    `json:"status"`
	ErrorCode   string    `json:"error_code,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}
type Manifest struct {
	Schema       int    `json:"schema"`
	App          string `json:"app"`
	Version      string `json:"version"`
	Channel      string `json:"channel"`
	Arch         string `json:"arch"`
	MinBootstrap int    `json:"min_bootstrap"`
	DataSchema   int    `json:"data_schema"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Notes        string `json:"notes"`
}
type SignedManifest struct {
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type Release struct {
	ID          string   `json:"release_id"`
	Manifest    Manifest `json:"manifest"`
	DownloadURL string   `json:"download_url"`
}
type Status struct {
	Enabled        bool       `json:"enabled"`
	AutoEnabled    bool       `json:"auto_enabled"`
	CurrentVersion string     `json:"current_version"`
	Phase          string     `json:"phase"`
	Error          string     `json:"error,omitempty"`
	Available      bool       `json:"available"`
	Release        *Release   `json:"release,omitempty"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
