package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
)

const releaseMaxBytes int64 = 256 << 20
const manifestMaxBytes int64 = 64 << 10

var releaseVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var releaseChannelPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var releaseHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var releaseErrorPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// The payload's original bytes are signed; never re-marshal before verifying.
type SignedReleaseManifest struct {
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type ReleasePayload struct {
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
type Release struct {
	ID          string                `json:"id"`
	Manifest    SignedReleaseManifest `json:"manifest"`
	Payload     ReleasePayload        `json:"payload"`
	Status      string                `json:"status"`
	CreatedAt   time.Time             `json:"created_at"`
	PublishedAt *time.Time            `json:"published_at"`
	WithdrawnAt *time.Time            `json:"withdrawn_at"`
}
type ReleaseReportInput struct {
	ReleaseID   string    `json:"release_id,omitempty"`
	EventID     string    `json:"event_id"`
	Version     string    `json:"version"`
	FromVersion string    `json:"from_version"`
	Status      string    `json:"status"`
	ErrorCode   string    `json:"error_code"`
	OccurredAt  time.Time `json:"occurred_at"`
}
type ReleaseReport struct {
	ReleaseReportInput
	InstallationID string    `json:"installation_id"`
	ReceivedAt     time.Time `json:"received_at"`
}
type releaseObservation struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
}
type ReleaseCounts struct {
	Success    int `json:"success"`
	Failed     int `json:"failed"`
	RolledBack int `json:"rolled_back"`
}
type ReleaseWithCounts struct {
	Release
	Results ReleaseCounts `json:"results"`
}

func loadReleaseKeys(path, env string) (map[string]ed25519.PublicKey, error) {
	keys := map[string]ed25519.PublicKey{}
	raw := []byte(env)
	if path != "" {
		var err error
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read release public keys: %w", err)
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return keys, nil
	}
	var encoded map[string]string
	if err := decodeStrict(bytes.NewReader(raw), &encoded); err != nil {
		return nil, errors.New("release public keys must be a JSON object: key_id to base64 Ed25519 public key")
	}
	if len(encoded) > 32 {
		return nil, errors.New("too many release signing keys")
	}
	for id, value := range encoded {
		key, err := base64.StdEncoding.Strict().DecodeString(value)
		if !validID(id) || err != nil || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid release key ID or Ed25519 public key")
		}
		keys[id] = ed25519.PublicKey(key)
	}
	return keys, nil
}
func releaseVersionCompare(left, right string) int {
	a, b := strings.Split(left, "."), strings.Split(right, ".")
	for i := 0; i < 3; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
func (a *app) verifyReleaseManifest(m SignedReleaseManifest) (ReleasePayload, error) {
	var p ReleasePayload
	key, ok := a.releaseKeys[m.KeyID]
	if !ok || len(key) != ed25519.PublicKeySize {
		return p, errors.New("签名密钥未受信任，请先在服务器配置发布公钥")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(m.Payload)
	if err != nil || len(raw) > 32<<10 {
		return p, errors.New("签名清单 payload 必须是有效 base64 JSON")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, raw, sig) {
		return p, errors.New("更新清单签名校验失败")
	}
	if err = decodeStrict(bytes.NewReader(raw), &p); err != nil {
		return p, errors.New("更新清单字段不符合协议")
	}
	if p.Schema != 1 || p.App != "fn-seekbox" || !releaseVersionPattern.MatchString(p.Version) || !releaseChannelPattern.MatchString(p.Channel) || !oneOf(p.Arch, "amd64", "arm64") || p.MinBootstrap < 1 || p.MinBootstrap > 1000000 || p.DataSchema < 1 || p.DataSchema > 1000000 || !releaseHashPattern.MatchString(p.SHA256) || p.Size < 1 || p.Size > releaseMaxBytes || len(p.Notes) > 16384 || strings.ContainsRune(p.Notes, '\x00') {
		return p, errors.New("更新清单需包含正确的应用、三段版本号、渠道、架构、兼容版本、SHA256 和包大小（最多 256 MiB）")
	}
	return p, nil
}
func (a *app) artifactPath(id string) (string, error) {
	if !strings.HasPrefix(id, "rel_") || !validID(id) {
		return "", errors.New("invalid release ID")
	}
	return filepath.Join(a.releaseDir, id+".tar.gz"), nil
}
func (a *app) verifyArtifact(r Release) error {
	path, err := a.artifactPath(r.ID)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != r.Payload.Size {
		return errors.New("artifact missing or invalid size")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, releaseMaxBytes+1))
	if err != nil || n != r.Payload.Size || hex.EncodeToString(h.Sum(nil)) != r.Payload.SHA256 {
		return errors.New("artifact hash mismatch")
	}
	return nil
}

func (a *app) uploadRelease(c *gin.Context) {
	if len(a.releaseKeys) == 0 {
		fail(c, 503, "SIGNING_KEYS_NOT_CONFIGURED", "尚未配置发布公钥，不能接收更新包")
		return
	}
	if err := os.MkdirAll(a.releaseDir, 0700); err != nil {
		a.dbError(c, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, releaseMaxBytes+manifestMaxBytes+65536)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		fail(c, 400, "INVALID_UPLOAD", "请上传 manifest JSON 和 artifact tar.gz 两个文件")
		return
	}
	var manifestRaw []byte
	var artifactName string
	defer func() {
		if artifactName != "" {
			os.Remove(artifactName)
		}
	}()
	var size int64
	var checksum string
	for count := 0; ; count++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 2 {
			fail(c, 400, "INVALID_UPLOAD", "上传内容不完整或包含多余字段")
			return
		}
		switch part.FormName() {
		case "manifest":
			if manifestRaw != nil {
				fail(c, 400, "INVALID_UPLOAD", "签名清单重复")
				return
			}
			manifestRaw, err = io.ReadAll(io.LimitReader(part, manifestMaxBytes+1))
			if err != nil || int64(len(manifestRaw)) > manifestMaxBytes {
				fail(c, 413, "PAYLOAD_TOO_LARGE", "签名清单超过上限")
				return
			}
		case "artifact":
			if artifactName != "" {
				fail(c, 400, "INVALID_UPLOAD", "更新包重复")
				return
			}
			temp, err := os.CreateTemp(a.releaseDir, ".upload-*")
			if err != nil {
				a.dbError(c, err)
				return
			}
			artifactName = temp.Name()
			h := sha256.New()
			size, err = io.Copy(io.MultiWriter(temp, h), io.LimitReader(part, releaseMaxBytes+1))
			syncErr := temp.Sync()
			closeErr := temp.Close()
			if err != nil || syncErr != nil || closeErr != nil {
				fail(c, 400, "INVALID_UPLOAD", "更新包上传未完成")
				return
			}
			if size > releaseMaxBytes {
				fail(c, 413, "PAYLOAD_TOO_LARGE", "更新包不能超过 256 MiB")
				return
			}
			checksum = hex.EncodeToString(h.Sum(nil))
		default:
			fail(c, 400, "INVALID_UPLOAD", "仅接受 manifest 和 artifact 字段")
			return
		}
		part.Close()
	}
	if len(manifestRaw) == 0 || artifactName == "" {
		fail(c, 400, "INVALID_UPLOAD", "请同时上传签名清单和更新包")
		return
	}
	var manifest SignedReleaseManifest
	if decodeStrict(bytes.NewReader(manifestRaw), &manifest) != nil {
		fail(c, 400, "INVALID_MANIFEST", "签名清单 JSON 不正确")
		return
	}
	payload, err := a.verifyReleaseManifest(manifest)
	if err != nil {
		fail(c, 400, "INVALID_MANIFEST", err.Error())
		return
	}
	if payload.Size != size || payload.SHA256 != checksum {
		fail(c, 400, "ARTIFACT_MISMATCH", "更新包大小或 SHA256 与签名清单不一致")
		return
	}
	release := Release{ID: randomID("rel_"), Manifest: manifest, Payload: payload, Status: "draft", CreatedAt: a.now()}
	target, _ := a.artifactPath(release.ID)
	duplicate := false
	err = a.store.db.Update(func(tx *bolt.Tx) error {
		for _, old := range rows[Release](tx, "releases") {
			if old.Payload.App == payload.App && old.Payload.Channel == payload.Channel && old.Payload.Arch == payload.Arch && old.Payload.Version == payload.Version {
				duplicate = true
				return nil
			}
		}
		if e := os.Rename(artifactName, target); e != nil {
			return e
		}
		return put(tx, "releases", release.ID, release)
	})
	if duplicate {
		fail(c, 409, "RELEASE_EXISTS", "此版本、渠道和架构已存在；版本内容不可覆盖，请使用新版本号")
		return
	}
	if err != nil {
		os.Remove(target)
		a.dbError(c, err)
		return
	}
	// Persist the directory entry before acknowledging the upload.
	if dir, err := os.Open(a.releaseDir); err == nil {
		dir.Sync()
		dir.Close()
	}
	reply(c, 201, 0, "ok", release)
}
func (a *app) changeReleaseState(c *gin.Context) {
	var input struct{}
	if !readBody(c, &input) {
		return
	}
	var release Release
	found := false
	if err := a.store.db.View(func(tx *bolt.Tx) error { found = get(tx, "releases", c.Param("id"), &release); return nil }); err != nil {
		a.dbError(c, err)
		return
	}
	if !found {
		fail(c, 404, "NOT_FOUND", "版本不存在")
		return
	}
	publish := strings.HasSuffix(c.FullPath(), "/publish")
	if publish {
		if _, err := a.verifyReleaseManifest(release.Manifest); err != nil {
			fail(c, 409, "INVALID_MANIFEST", err.Error())
			return
		}
		if a.verifyArtifact(release) != nil {
			fail(c, 409, "ARTIFACT_MISMATCH", "更新包已缺失或损坏，不能发布")
			return
		}
	}
	invalidState := false
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		if !get(tx, "releases", c.Param("id"), &release) {
			return errors.New("release disappeared")
		}
		now := a.now()
		if publish {
			if release.Status == "published" {
				return nil
			}
			if release.Status != "draft" {
				invalidState = true
				return nil
			}
			release.Status = "published"
			release.PublishedAt = &now
		} else {
			if release.Status == "withdrawn" {
				return nil
			}
			if release.Status != "published" {
				invalidState = true
				return nil
			}
			release.Status = "withdrawn"
			release.WithdrawnAt = &now
		}
		return put(tx, "releases", release.ID, release)
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if invalidState {
		fail(c, 409, "INVALID_RELEASE_STATE", "只能发布草稿或撤回已发布版本；撤回版本不能重新发布")
		return
	}
	reply(c, 200, 0, "ok", release)
}
func (a *app) latestRelease(c *gin.Context) {
	query := c.Request.URL.Query()
	for key, vals := range query {
		if !oneOf(key, "app", "channel", "arch", "version") || len(vals) != 1 {
			fail(c, 400, "INVALID_FIELD", "版本参数不正确")
			return
		}
	}
	appName, channel, arch, version := c.Query("app"), c.Query("channel"), c.Query("arch"), c.Query("version")
	if appName != "fn-seekbox" || !releaseChannelPattern.MatchString(channel) || !oneOf(arch, "amd64", "arm64") || !releaseVersionPattern.MatchString(version) {
		fail(c, 400, "INVALID_FIELD", "需要 app、channel、arch 和三段 version")
		return
	}
	var selected Release
	err := a.store.db.View(func(tx *bolt.Tx) error {
		for _, item := range rows[Release](tx, "releases") {
			p := item.Payload
			if item.Status != "published" || p.App != appName || p.Channel != channel || p.Arch != arch || releaseVersionCompare(p.Version, version) <= 0 {
				continue
			}
			if selected.ID == "" || releaseVersionCompare(p.Version, selected.Payload.Version) > 0 {
				selected = item
			}
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if selected.ID == "" {
		reply(c, 200, 0, "ok", gin.H{"available": false})
		return
	}
	reply(c, 200, 0, "ok", gin.H{"available": true, "release_id": selected.ID, "manifest": selected.Manifest, "download_url": a.origin + "/api/client/releases/" + selected.ID + "/artifact"})
}
func (a *app) downloadRelease(c *gin.Context) {
	var release Release
	found := false
	if err := a.store.db.View(func(tx *bolt.Tx) error { found = get(tx, "releases", c.Param("id"), &release); return nil }); err != nil {
		a.dbError(c, err)
		return
	}
	if !found || release.Status != "published" {
		fail(c, 404, "NOT_FOUND", "更新包不存在或未发布")
		return
	}
	path, err := a.artifactPath(release.ID)
	if err != nil {
		fail(c, 404, "NOT_FOUND", "更新包不可用")
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != release.Payload.Size {
		fail(c, 503, "ARTIFACT_UNAVAILABLE", "更新包不可用")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		fail(c, 503, "ARTIFACT_UNAVAILABLE", "更新包不可用")
		return
	}
	defer file.Close()
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", `attachment; filename="fn-seekbox-`+release.Payload.Version+"-"+release.Payload.Arch+`.tar.gz"`)
	c.Header("ETag", `"`+release.Payload.SHA256+`"`)
	// Keep no-store: withdrawal must stop subsequent downloads, including CDN/browser requests.
	http.ServeContent(c.Writer, c.Request, "update.tar.gz", info.ModTime(), file)
}
func (a *app) reportRelease(c *gin.Context) {
	if !clientJSON(c) {
		return
	}
	var input ReleaseReportInput
	if !readBody(c, &input) {
		return
	}
	if (input.ReleaseID != "" && !validID(input.ReleaseID)) || !validID(input.EventID) || !releaseVersionPattern.MatchString(input.Version) || !releaseVersionPattern.MatchString(input.FromVersion) || !oneOf(input.Status, "success", "failed", "rolled_back") || !validTime(input.OccurredAt) || input.OccurredAt.After(a.now().Add(5*time.Minute)) || input.OccurredAt.Before(a.now().Add(-90*24*time.Hour)) || (input.ErrorCode != "" && !releaseErrorPattern.MatchString(input.ErrorCode)) || (input.Status == "success" && input.ErrorCode != "") {
		fail(c, 400, "INVALID_FIELD", "更新结果字段不正确；错误原因只接受受控错误码")
		return
	}
	installation := c.GetString("installation_id")
	report := ReleaseReport{ReleaseReportInput: input, InstallationID: installation, ReceivedAt: a.now()}
	duplicate, conflict, missing, revoked, ambiguous := false, false, false, false, false
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		var inst Instance
		if !get(tx, "instances", installation, &inst) || inst.Revoked {
			revoked = true
			return nil
		}
		var previous ReleaseReport
		key := installation + "|" + input.EventID
		if get(tx, "release_reports", key, &previous) {
			if input.ReleaseID == "" {
				input.ReleaseID = previous.ReleaseID
			}
			left, _ := json.Marshal(previous.ReleaseReportInput)
			right, _ := json.Marshal(input)
			duplicate = bytes.Equal(left, right)
			conflict = !duplicate
			return nil
		}
		report.ReleaseID = ""
		for _, release := range rows[Release](tx, "releases") {
			if release.Payload.Version == input.Version && release.Payload.Arch == inst.Arch && release.PublishedAt != nil && (input.ReleaseID == "" || input.ReleaseID == release.ID) {
				if report.ReleaseID != "" && input.ReleaseID == "" {
					ambiguous = true
					return nil
				}
				report.ReleaseID = release.ID
			}
		}
		if report.ReleaseID == "" {
			missing = true
			return nil
		}
		if err := put(tx, "release_reports", key, report); err != nil {
			return err
		}
		var observation releaseObservation
		get(tx, "release_observations", installation, &observation)
		if !input.OccurredAt.After(observation.At) {
			return nil
		}
		observation.At = input.OccurredAt
		observation.Version = input.FromVersion
		if input.Status == "success" {
			observation.Version = input.Version
		}
		return put(tx, "release_observations", installation, observation)
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if revoked {
		fail(c, 401, "UNAUTHORIZED", "上报凭证无效或已撤销")
		return
	}
	if conflict {
		fail(c, 409, "IDEMPOTENCY_CONFLICT", "同一 event_id 的内容不能改变")
		return
	}
	if ambiguous {
		fail(c, 409, "AMBIGUOUS_RELEASE", "同版本存在多个渠道发布，请提供 release_id")
		return
	}
	if missing {
		fail(c, 400, "RELEASE_NOT_FOUND", "目标版本没有对应架构的发布记录")
		return
	}
	reply(c, 200, 0, "ok", gin.H{"event_id": input.EventID, "accepted": true, "duplicate": duplicate})
}
func (a *app) listReleases(c *gin.Context) {
	items := []ReleaseWithCounts{}
	reports := []ReleaseReport{}
	distribution := map[string]int{}
	err := a.store.db.View(func(tx *bolt.Tx) error {
		reports = rows[ReleaseReport](tx, "release_reports")
		counts := map[string]ReleaseCounts{}
		for _, report := range reports {
			count := counts[report.ReleaseID]
			switch report.Status {
			case "success":
				count.Success++
			case "failed":
				count.Failed++
			case "rolled_back":
				count.RolledBack++
			}
			counts[report.ReleaseID] = count
		}
		for _, release := range rows[Release](tx, "releases") {
			items = append(items, ReleaseWithCounts{release, counts[release.ID]})
		}
		for _, inst := range rows[Instance](tx, "instances") {
			if inst.Revoked {
				continue
			}
			version := inst.AppVersion
			var observation releaseObservation
			if get(tx, "release_observations", inst.ID, &observation) && (inst.ReportedAt == nil || observation.At.After(*inst.ReportedAt)) {
				version = observation.Version
			}
			distribution[version]++
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt) || (items[i].CreatedAt.Equal(items[j].CreatedAt) && items[i].ID < items[j].ID)
	})
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].ReceivedAt.After(reports[j].ReceivedAt) || (reports[i].ReceivedAt.Equal(reports[j].ReceivedAt) && reports[i].EventID < reports[j].EventID)
	})
	total := len(reports)
	if len(reports) > 100 {
		reports = reports[:100]
	}
	keys := []string{}
	for id := range a.releaseKeys {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	reply(c, 200, 0, "ok", gin.H{"items": items, "version_distribution": distribution, "recent_reports": reports, "total_reports": total, "signing_key_ids": keys, "upload_limit_bytes": releaseMaxBytes})
}
func (a *app) registerReleases(r *gin.Engine, admin *gin.RouterGroup) {
	r.GET("/api/client/releases/latest", a.publicLimit, a.latestRelease)
	r.GET("/api/client/releases/:id/artifact", a.publicLimit, a.downloadRelease)
	r.POST("/api/client/releases/report", a.reportAuth, a.reportRelease)
	admin.GET("/releases", a.listReleases)
	writes := admin.Group("", a.sessionOnly)
	writes.POST("/releases", a.uploadRelease)
	writes.POST("/releases/:id/publish", a.changeReleaseState)
	writes.POST("/releases/:id/withdraw", a.changeReleaseState)
}

// Only large authenticated upload / public artifact routes get longer transfer deadlines.
func releaseTransferTimeouts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == adminPrefix+"/releases" || r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/client/releases/") && strings.HasSuffix(r.URL.Path, "/artifact") {
			controller := http.NewResponseController(w)
			controller.SetReadDeadline(time.Now().Add(15 * time.Minute))
			controller.SetWriteDeadline(time.Now().Add(15 * time.Minute))
		}
		next.ServeHTTP(w, r)
	})
}
