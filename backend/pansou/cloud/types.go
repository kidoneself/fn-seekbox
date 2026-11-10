// Package cloud manages personal cloud-drive transfers and subscriptions.
// Provider credentials stay on the NAS and are never included in API responses.
package cloud

import (
	"context"
	"errors"
)

var (
	ErrAuthExpired       = errors.New("网盘登录已失效，请重新连接账号")
	ErrDestinationExists = errors.New("目标目录已有同名文件，请选择其他目录")
	ErrNotFound          = errors.New("记录不存在")
	ErrBusy              = errors.New("任务正在运行，请稍后再试")
)

type Credentials map[string]string
type Identity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ShareLink struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}
type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Entry struct {
	ID, Name, Path, Token, ParentID, Revision string
	IsDir                                     bool
	Size                                      int64
}
type Share struct {
	ID      string
	Entries []Entry
	Data    map[string]string
}

type Provider interface {
	ID() string
	RootID() string
	Validate(context.Context, Credentials) (Identity, error)
	ListFolders(context.Context, Credentials, string) ([]Folder, error)
	ReadShare(context.Context, Credentials, ShareLink) (Share, error)
	CreateFolder(context.Context, Credentials, string, string) (Folder, error)
	Save(context.Context, Credentials, Share, Entry, string) error
}

// BatchSaver submits siblings together; Confirmed contains only positively
// confirmed source IDs. Submitted remains true after any uncertain write.
type BatchSaver interface {
	SaveBatch(context.Context, Credentials, Share, []Entry, string) (BatchResult, error)
}
type BatchResult struct {
	Submitted bool
	Confirmed []string
}

type CredentialField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Help     string `json:"help"`
	Optional bool   `json:"optional,omitempty"`
}
type ProviderInfo struct {
	QRLogin          bool              `json:"qr_login"`
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	RootID           string            `json:"root_id"`
	Website          string            `json:"website"`
	Help             string            `json:"help"`
	CredentialFields []CredentialField `json:"credential_fields"`
}

type Account struct {
	AuthCheckedAt  int64  `json:"auth_checked_at"`
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	ProviderUserID string `json:"provider_user_id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	LastError      string `json:"last_error"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

type TransferRequest struct {
	FilterAds       bool   `json:"filter_ads"`
	AccountID       string `json:"account_id"`
	Title           string `json:"title"`
	ShareURL        string `json:"share_url"`
	Password        string `json:"password"`
	DestinationID   string `json:"destination_id"`
	DestinationName string `json:"destination_name"`
	RequestID       string `json:"request_id"`
}

type Subscription struct {
	Progress *FollowProgress `json:"progress,omitempty"`
	ID       string          `json:"id"`
	TransferRequest
	Provider        string `json:"provider"`
	IntervalMinutes int    `json:"interval_minutes"`
	Enabled         bool   `json:"enabled"`
	Status          string `json:"status"`
	LastCheckedAt   int64  `json:"last_checked_at"`
	NextCheckAt     int64  `json:"next_check_at"`
	LastError       string `json:"last_error"`
	Transferred     int    `json:"transferred"`
	CreatedAt       int64  `json:"created_at"`
}

type Job struct {
	Progress       *FollowProgress `json:"progress,omitempty"`
	Filtered       int             `json:"filtered"`
	ID             string          `json:"id"`
	AccountID      string          `json:"account_id"`
	SubscriptionID string          `json:"subscription_id"`
	Provider       string          `json:"provider"`
	Title          string          `json:"title"`
	Status         string          `json:"status"`
	Saved          int             `json:"saved"`
	Skipped        int             `json:"skipped"`
	Total          int             `json:"total"`
	Error          string          `json:"error"`
	CreatedAt      int64           `json:"created_at"`
	FinishedAt     int64           `json:"finished_at"`
}
