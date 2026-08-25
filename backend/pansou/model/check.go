package model

type CheckItem struct {
	DiskType string `json:"disk_type" binding:"required"`
	URL      string `json:"url" binding:"required"`
	Password string `json:"password,omitempty"`
}

type CheckRequest struct {
	Items     []CheckItem `json:"items" binding:"required"`
	ViewToken string      `json:"view_token,omitempty"`
	Proxy     string      `json:"proxy,omitempty"`
	ProxyURL  string      `json:"proxy_url,omitempty"`
}

type CheckResult struct {
	DiskType      string `json:"disk_type"`
	URL           string `json:"url"`
	NormalizedURL string `json:"normalized_url,omitempty"`
	State         string `json:"state"`
	CacheHit      bool   `json:"cache_hit"`
	CheckedAt     int64  `json:"checked_at"`
	ExpiresAt     int64  `json:"expires_at"`
	Summary       string `json:"summary,omitempty"`
}

type CheckResponse struct {
	Results []CheckResult `json:"results"`
}
