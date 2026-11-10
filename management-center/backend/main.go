package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

const adminPrefix = "/seekbox-config/api"
const cookieName = "seekbox_config_session"
const sessionLifetime = 12 * time.Hour

var providers = []string{"baidu", "quark", "xunlei"}
var configBucket = []byte("public-config")
var configKey = []byte("v1")
var siteIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type CheckLink struct {
	SourceID      string     `json:"source_id"`
	SourceVersion string     `json:"source_version"`
	ExpiresAt     *time.Time `json:"expires_at"`
	Provider      string     `json:"provider"`
	ShareURL      string     `json:"share_url"`
	Password      string     `json:"password"`
	FilePath      string     `json:"file_path"`
	Enabled       bool       `json:"enabled"`
}
type CheckLinkInput struct {
	SourceID  string     `json:"source_id"`
	ExpiresAt *time.Time `json:"expires_at"`
	ShareURL  string     `json:"share_url"`
	Password  string     `json:"password"`
	FilePath  string     `json:"file_path"`
	Enabled   *bool      `json:"enabled"`
}
type Site struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Order   int    `json:"order"`
}
type SiteInput struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
	Order   *int   `json:"order"`
}
type SiteCreateInput struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
	Order   *int   `json:"order"`
}
type Config struct {
	CheckLinks  []CheckLink `json:"check_links"`
	SearchSites []Site      `json:"search_sites"`
}
type envelope struct {
	Code      any    `json:"code"`
	RequestID string `json:"request_id"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
}
type Store struct{ db *bolt.DB }

func validProvider(p string) bool { return p == "baidu" || p == "quark" || p == "xunlei" }
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
func validateURL(raw string) (*url.URL, error) {
	if raw == "" || raw != strings.TrimSpace(raw) || len(raw) > 4096 || hasControl(raw) {
		return nil, errors.New("URL 必须为有效的 http/https 地址")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.Contains(u.Host, "\\") {
		return nil, errors.New("URL 必须为有效的 http/https 地址，且不能包含登录凭据")
	}
	return u, nil
}
func validateCheckLink(c CheckLink) error {
	if !validProvider(c.Provider) {
		return errors.New("provider 仅支持 baidu、quark、xunlei")
	}
	if c.Enabled && (c.ShareURL == "" || c.FilePath == "") {
		return errors.New("启用测试源必须填写分享链接和测试文件相对路径")
	}
	if c.ShareURL != "" {
		u, err := validateURL(c.ShareURL)
		if err != nil {
			return err
		}
		host := strings.ToLower(u.Hostname())
		valid := (c.Provider == "baidu" && (host == "pan.baidu.com" || host == "yun.baidu.com")) || (c.Provider == "quark" && host == "pan.quark.cn") || (c.Provider == "xunlei" && host == "pan.xunlei.com")
		if !valid || u.Scheme != "https" || u.Port() != "" || !regexp.MustCompile(`^/s/[A-Za-z0-9_-]+/?$`).MatchString(u.Path) || u.Fragment != "" {
			return errors.New("分享链接必须使用所选网盘的官方分享域名")
		}
	}
	if len(c.Password) > 128 || hasControl(c.Password) {
		return errors.New("提取码不能包含控制字符或超过 128 字节")
	}
	if len(c.FilePath) > 1024 || hasControl(c.FilePath) || strings.HasPrefix(c.FilePath, "/") || strings.Contains(c.FilePath, "\\") || strings.Contains(c.FilePath, ":") || strings.ContainsAny(c.FilePath, "*?[]") {
		return errors.New("测试文件路径必须是安全的相对路径")
	}
	if c.FilePath != "" {
		for _, p := range strings.Split(c.FilePath, "/") {
			if p == "" || p == "." || p == ".." {
				return errors.New("测试文件路径不能包含空目录、. 或 ..")
			}
		}
	}
	if c.Enabled && strings.Contains(strings.ToUpper(c.ShareURL+" "+c.Password+" "+c.FilePath), "EXAMPLE_NOT_REAL") {
		return errors.New("占位测试源不能启用，请先替换为真实分享")
	}
	return nil
}
func validateSite(s Site) error {
	if !siteIDPattern.MatchString(s.ID) {
		return errors.New("id 须为 1–64 位字母、数字、下划线或连字符，且首位为字母或数字")
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 200 || hasControl(s.Name) {
		return errors.New("站点名称不能为空、包含控制字符或超过 200 字节")
	}
	if _, err := validateURL(s.URL); err != nil {
		return err
	}
	if s.Order < -1000000 || s.Order > 1000000 {
		return errors.New("order 须介于 -1000000 和 1000000 之间")
	}
	return nil
}
func normalizeConfig(c *Config) error {
	links := make(map[string]CheckLink)
	for _, link := range c.CheckLinks {
		if err := validateCheckLink(link); err != nil {
			return err
		}
		if _, ok := links[link.Provider]; ok {
			return errors.New("重复的 provider")
		}
		links[link.Provider] = link
	}
	c.CheckLinks = make([]CheckLink, 0, 3)
	for _, p := range providers {
		link, ok := links[p]
		if !ok {
			link = CheckLink{Provider: p}
		}
		if link.SourceID == "" {
			link.SourceID = "probe_" + p + "_01"
		}
		if link.SourceVersion == "" {
			link.SourceVersion = "1"
		}
		c.CheckLinks = append(c.CheckLinks, link)
	}
	ids := make(map[string]bool)
	if c.SearchSites == nil {
		c.SearchSites = []Site{}
	}
	for _, site := range c.SearchSites {
		if err := validateSite(site); err != nil {
			return err
		}
		if ids[site.ID] {
			return errors.New("重复的站点 id")
		}
		ids[site.ID] = true
	}
	sort.Slice(c.SearchSites, func(i, j int) bool {
		if c.SearchSites[i].Order == c.SearchSites[j].Order {
			return c.SearchSites[i].ID < c.SearchSites[j].ID
		}
		return c.SearchSites[i].Order < c.SearchSites[j].Order
	})
	return nil
}
func openStore(path, seedPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	err = db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(configBucket)
		if err != nil {
			return err
		}
		if bucket.Get(configKey) != nil {
			return nil
		}
		initial := Config{}
		if seedPath != "" {
			raw, err := os.ReadFile(seedPath)
			if err != nil {
				return fmt.Errorf("read initial seed: %w", err)
			}
			if err := decodeStrict(bytes.NewReader(raw), &initial); err != nil {
				return fmt.Errorf("decode initial seed: %w", err)
			}
		}
		if err := normalizeConfig(&initial); err != nil {
			return fmt.Errorf("invalid initial seed: %w", err)
		}
		data, err := json.Marshal(initial)
		if err != nil {
			return err
		}
		return bucket.Put(configKey, data)
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := store.initTelemetry(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}
func readConfigTx(tx *bolt.Tx) (Config, error) {
	c := Config{}
	bucket := tx.Bucket(configBucket)
	if bucket == nil {
		return c, errors.New("configuration bucket missing")
	}
	raw := bucket.Get(configKey)
	if raw == nil {
		return c, errors.New("configuration record missing")
	}
	err := json.Unmarshal(raw, &c)
	if err != nil {
		return c, err
	}
	err = normalizeConfig(&c)
	return c, err
}
func (s *Store) read() (Config, error) {
	var c Config
	err := s.db.View(func(tx *bolt.Tx) error { var err error; c, err = readConfigTx(tx); return err })
	return c, err
}
func (s *Store) update(fn func(*Config) error) (Config, error) {
	var c Config
	err := s.db.Update(func(tx *bolt.Tx) error {
		var err error
		c, err = readConfigTx(tx)
		if err != nil {
			return err
		}
		if err = fn(&c); err != nil {
			return err
		}
		if err = normalizeConfig(&c); err != nil {
			return err
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return tx.Bucket(configBucket).Put(configKey, raw)
	})
	return c, err
}
func decodeStrict(r io.Reader, dst any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

type session struct{ Expires time.Time }
type loginWindow struct {
	Start time.Time
	Count int
}
type app struct {
	scanURL        string
	scanSecret     string
	releaseDir     string
	releaseKeys    map[string]ed25519.PublicKey
	trustedProxies []string
	registry       []Endpoint
	store          *Store
	origin, web    string
	passwordHash   []byte
	mu             sync.Mutex
	sessions       map[[32]byte]session
	loginAttempts  map[string]loginWindow
	now            func() time.Time
}

func newApp(store *Store, origin, web string, hash []byte) *app {
	return &app{releaseDir: filepath.Join(filepath.Dir(store.db.Path()), "releases"), releaseKeys: map[string]ed25519.PublicKey{}, store: store, origin: origin, web: web, passwordHash: hash, sessions: make(map[[32]byte]session), loginAttempts: make(map[string]loginWindow), now: time.Now}
}
func reply(c *gin.Context, status int, code any, message string, data any) {
	if n, ok := code.(int); ok && n != 0 {
		code = legacyErrorCode(n)
	}
	c.JSON(status, envelope{Code: code, RequestID: c.GetString("request_id"), Message: message, Data: data})
}
func fail(c *gin.Context, status int, code any, message string) {
	reply(c, status, code, message, nil)
	c.Abort()
}
func (a *app) dbError(c *gin.Context, err error) {
	log.Printf("configuration database operation failed: %v", err)
	fail(c, 503, 50302, "配置服务暂时不可用，请稍后重试")
}
func readBody(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 262144)
	if err := decodeStrict(c.Request.Body, dst); err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			fail(c, 413, "PAYLOAD_TOO_LARGE", "请求体超过上限")
		} else {
			fail(c, 400, "INVALID_FIELD", "请求体须为有效 JSON，不能包含未知字段或多份内容")
		}
		return false
	}
	return true
}
func (a *app) writeGuard(c *gin.Context) {
	if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPatch || c.Request.Method == http.MethodDelete {
		if c.GetHeader("Authorization") == "" && c.GetHeader("Origin") != a.origin {
			fail(c, 403, 40301, "请求来源不被允许")
			return
		}
		typ, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		isUpload := c.Request.Method == http.MethodPost && c.FullPath() == adminPrefix+"/releases"
		if err != nil || (!isUpload && typ != "application/json") || (isUpload && typ != "multipart/form-data") {
			required := "application/json"
			if isUpload {
				required = "multipart/form-data"
			}
			fail(c, 415, 41501, "Content-Type 必须为 "+required)
			return
		}
	}
	c.Next()
}
func (a *app) authenticated(c *gin.Context) bool {
	cookie, err := c.Request.Cookie(cookieName)
	if err != nil || len(cookie.Value) != 64 {
		return false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[key]
	if !ok {
		return false
	}
	if !a.now().Before(s.Expires) {
		delete(a.sessions, key)
		return false
	}
	return true
}
func (a *app) requireSession(c *gin.Context) {
	if c.GetHeader("Authorization") != "" {
		a.authorizeAdminKey(c)
		return
	}
	if !a.authenticated(c) {
		fail(c, 401, 40101, "请先登录配置后台")
		return
	}
	c.Next()
}
func (a *app) cookie(c *gin.Context, value string, expires time.Time, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{Name: cookieName, Value: value, Path: "/seekbox-config/", HttpOnly: true, Secure: strings.HasPrefix(a.origin, "https://"), SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: maxAge})
}
func (a *app) login(c *gin.Context) {
	var input struct {
		Password string `json:"password"`
	}
	if !readBody(c, &input) {
		return
	}
	now := a.now()
	ip := c.ClientIP()
	a.mu.Lock()
	for key, window := range a.loginAttempts {
		if now.Sub(window.Start) >= 15*time.Minute {
			delete(a.loginAttempts, key)
		}
	}
	window := a.loginAttempts[ip]
	if window.Start.IsZero() {
		window.Start = now
	}
	window.Count++
	a.loginAttempts[ip] = window
	a.mu.Unlock()
	if window.Count > 10 {
		c.Header("Retry-After", "900")
		fail(c, 429, 42901, "登录尝试过于频繁，请 15 分钟后重试")
		return
	}
	if len(input.Password) == 0 || len(input.Password) > 72 || bcrypt.CompareHashAndPassword(a.passwordHash, []byte(input.Password)) != nil {
		fail(c, 401, 40101, "后台密码错误")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		fail(c, 500, 50001, "无法创建登录会话")
		return
	}
	token := hex.EncodeToString(raw)
	key := sha256.Sum256([]byte(token))
	expires := now.Add(sessionLifetime)
	a.mu.Lock()
	for k, s := range a.sessions {
		if !now.Before(s.Expires) {
			delete(a.sessions, k)
		}
	}
	if prior, err := c.Request.Cookie(cookieName); err == nil {
		delete(a.sessions, sha256.Sum256([]byte(prior.Value)))
	}
	a.sessions[key] = session{Expires: expires}
	a.mu.Unlock()
	a.cookie(c, token, expires, int(sessionLifetime.Seconds()))
	reply(c, 200, 0, "ok", gin.H{"authenticated": true})
}
func (a *app) logout(c *gin.Context) {
	var input struct{}
	if !readBody(c, &input) {
		return
	}
	if cookie, err := c.Request.Cookie(cookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, sha256.Sum256([]byte(cookie.Value)))
		a.mu.Unlock()
	}
	a.cookie(c, "", time.Unix(1, 0), -1)
	reply(c, 200, 0, "ok", nil)
}
func (a *app) checkLink(c *gin.Context) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	values := query["provider"]
	if err != nil || len(query) != 1 || len(values) != 1 || !validProvider(values[0]) {
		fail(c, 400, 40001, "provider 参数必须且只能出现一次，仅支持 baidu、quark、xunlei")
		return
	}
	config, err := a.store.read()
	if err != nil {
		a.dbError(c, err)
		return
	}
	for _, link := range config.CheckLinks {
		if link.Provider == values[0] {
			if link.ShareURL == "" || link.FilePath == "" {
				fail(c, 404, "SOURCE_NOT_CONFIGURED", "该网盘尚未配置测试分享")
				return
			}
			if !link.Enabled {
				fail(c, 503, "SOURCE_DISABLED", "该网盘测试源已停用")
				return
			}
			if link.ExpiresAt != nil && !a.now().Before(*link.ExpiresAt) {
				fail(c, 503, "SOURCE_EXPIRED", "测试源已到期")
				return
			}
			reply(c, 200, 0, "ok", gin.H{"provider": link.Provider, "source_id": link.SourceID, "source_version": link.SourceVersion, "share_url": link.ShareURL, "password": link.Password, "file_path": link.FilePath, "max_file_size_bytes": 1048576, "expires_at": link.ExpiresAt})
			return
		}
	}
	fail(c, 503, 50301, "该网盘测试源未配置或未启用")
}
func (a *app) searchSites(c *gin.Context) {
	config, err := a.store.read()
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 200, 0, "ok", config.SearchSites)
}
func (a *app) getConfig(c *gin.Context) {
	config, err := a.store.read()
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 200, 0, "ok", config)
}

var errSourceID = errors.New("source_id is immutable")

func (a *app) putCheckLink(c *gin.Context) {
	p := c.Param("provider")
	if !validProvider(p) {
		fail(c, 400, 40001, "provider 仅支持 baidu、quark、xunlei")
		return
	}
	var input CheckLinkInput
	if !readBody(c, &input) {
		return
	}
	if input.Enabled == nil {
		fail(c, 400, 40001, "enabled 为必填布尔值")
		return
	}
	link := CheckLink{SourceID: input.SourceID, ExpiresAt: input.ExpiresAt, Provider: p, ShareURL: input.ShareURL, Password: input.Password, FilePath: input.FilePath, Enabled: *input.Enabled}
	if err := validateCheckLink(link); err != nil {
		fail(c, 400, 40001, err.Error())
		return
	}
	_, err := a.store.update(func(config *Config) error {
		for i := range config.CheckLinks {
			if config.CheckLinks[i].Provider == p {
				old := config.CheckLinks[i]
				if link.SourceID == "" {
					link.SourceID = old.SourceID
				}
				if link.SourceID != old.SourceID {
					return errSourceID
				}
				link.SourceVersion = nextVersion(old.SourceVersion)
				config.CheckLinks[i] = link
				return nil
			}
		}
		config.CheckLinks = append(config.CheckLinks, link)
		return nil
	})
	if errors.Is(err, errSourceID) {
		fail(c, 409, "SOURCE_ID_IMMUTABLE", "source_id 创建后不可更改")
		return
	}
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 200, 0, "ok", link)
}

var errDuplicate = errors.New("duplicate site id")
var errNotFound = errors.New("site id not found")

func (a *app) createSite(c *gin.Context) {
	var input SiteCreateInput
	if !readBody(c, &input) {
		return
	}
	if input.Enabled == nil || input.Order == nil {
		fail(c, 400, 40001, "enabled 和 order 为必填项")
		return
	}
	site := Site{ID: input.ID, Name: input.Name, URL: input.URL, Enabled: *input.Enabled, Order: *input.Order}
	if err := validateSite(site); err != nil {
		fail(c, 400, 40001, err.Error())
		return
	}
	var tombstone bool
	a.store.db.View(func(tx *bolt.Tx) error {
		tombstone = bucket(tx, "site_tombstones").Get([]byte(site.ID)) != nil
		return nil
	})
	if tombstone {
		fail(c, 409, "ID_RETIRED", "该ID已删除并保留历史关联，请使用新ID")
		return
	}
	_, err := a.store.update(func(config *Config) error {
		for _, existing := range config.SearchSites {
			if existing.ID == site.ID {
				return errDuplicate
			}
		}
		if len(config.SearchSites) >= 10000 {
			return errors.New("site capacity reached")
		}
		config.SearchSites = append(config.SearchSites, site)
		return nil
	})
	if errors.Is(err, errDuplicate) {
		fail(c, 409, 40901, "站点 id 已存在，请使用其他 id")
		return
	}
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 201, 0, "ok", site)
}
func (a *app) updateSite(c *gin.Context) {
	var input SiteInput
	if !readBody(c, &input) {
		return
	}
	if input.Enabled == nil || input.Order == nil {
		fail(c, 400, 40001, "enabled 和 order 为必填项")
		return
	}
	site := Site{ID: c.Param("id"), Name: input.Name, URL: input.URL, Enabled: *input.Enabled, Order: *input.Order}
	if err := validateSite(site); err != nil {
		fail(c, 400, 40001, err.Error())
		return
	}
	_, err := a.store.update(func(config *Config) error {
		for i := range config.SearchSites {
			if config.SearchSites[i].ID == site.ID {
				config.SearchSites[i] = site
				return nil
			}
		}
		return errNotFound
	})
	if errors.Is(err, errNotFound) {
		fail(c, 404, 40401, "站点不存在")
		return
	}
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 200, 0, "ok", site)
}
func (a *app) static(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		fail(c, 405, 40501, "请求方法不被允许")
		return
	}
	p := strings.TrimPrefix(c.Request.URL.Path, "/seekbox-config/")
	if strings.Contains(p, "\\") {
		fail(c, 404, 40401, "页面不存在")
		return
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") {
			fail(c, 404, 40401, "页面不存在")
			return
		}
	}
	if p == "" {
		p = "index.html"
	}
	target := filepath.Join(a.web, filepath.FromSlash(p))
	rel, err := filepath.Rel(a.web, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		fail(c, 404, 40401, "页面不存在")
		return
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) && filepath.Ext(p) == "" {
		target = filepath.Join(a.web, "index.html")
		info, err = os.Lstat(target)
	}
	if err != nil || !info.Mode().IsRegular() {
		fail(c, 404, 40401, "页面不存在")
		return
	}
	// Resolve parents as well so a symlinked asset directory cannot escape the web root.
	actual, err := filepath.EvalSymlinks(target)
	if err != nil {
		fail(c, 404, 40401, "页面不存在")
		return
	}
	root, err := filepath.EvalSymlinks(a.web)
	if err != nil {
		fail(c, 404, 40401, "页面不存在")
		return
	}
	rel, err = filepath.Rel(root, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		fail(c, 404, 40401, "页面不存在")
		return
	}
	c.File(actual)
}
func (a *app) router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.RedirectTrailingSlash = false
	r.HandleMethodNotAllowed = true
	if err := r.SetTrustedProxies(a.trustedProxies); err != nil {
		panic("invalid trusted proxy CIDR")
	}
	r.Use(a.requestContext)
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Frame-Options", "DENY")
		c.Next()
	})
	r.Use(func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				log.Print("request handler panicked")
				fail(c, 500, 50001, "服务内部异常")
			}
		}()
		c.Next()
	})
	public := r.Group("/api")
	public.Use(a.countPublic, a.publicLimit)
	public.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.Status(204)
			c.Abort()
			return
		}
		c.Next()
	})
	public.GET("/check-link", a.checkLink)
	public.OPTIONS("/check-link", func(c *gin.Context) { c.Status(204) })
	public.GET("/search-sites", a.searchSites)
	public.OPTIONS("/search-sites", func(c *gin.Context) { c.Status(204) })
	admin := r.Group(adminPrefix)
	admin.Use(a.writeGuard)
	admin.POST("/login", a.login)
	secure := admin.Group("")
	secure.Use(a.requireSession)
	secure.GET("/session", func(c *gin.Context) { reply(c, 200, 0, "ok", gin.H{"authenticated": true}) })
	secure.POST("/logout", a.logout)
	secure.GET("/config", a.getConfig)
	secure.GET("/stats", a.getStats)
	secure.PUT("/check-links/:provider", a.putCheckLink)
	secure.POST("/sites", a.createSite)
	secure.PUT("/sites/:id", a.updateSite)
	a.registerTelemetry(r, secure)
	a.registerReleases(r, secure)
	a.registerScan(r, secure)
	r.GET("/healthz", func(c *gin.Context) {
		if _, err := a.store.read(); err != nil {
			fail(c, 503, 50302, "unhealthy")
			return
		}
		reply(c, 200, 0, "ok", nil)
	})
	r.GET("/seekbox-config", func(c *gin.Context) { c.Redirect(302, "/seekbox-config/") })
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/seekbox-config/") && !strings.HasPrefix(c.Request.URL.Path, adminPrefix) {
			a.static(c)
			return
		}
		fail(c, 404, 40401, "接口不存在")
	})
	r.NoMethod(func(c *gin.Context) { fail(c, 405, 40501, "请求方法不被允许") })
	a.registry = describeRoutes(r.Routes())
	return r
}
func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "/data/config.db", "bbolt data file")
	seed := flag.String("seed", "/app/seed.json", "first-start seed JSON file")
	web := flag.String("web", "/app/web", "admin UI directory")
	hashFile := flag.String("admin-password-hash-file", "/run/secrets/admin-password-hash", "bcrypt password hash file")
	allowLocalHTTP := flag.Bool("allow-insecure-loopback", false, "allow HTTP admin origin only on localhost or 127.0.0.1 for isolated development")
	origin := flag.String("origin", "https://hermes.smartloan.cn", "required admin Origin")
	trustedProxy := flag.String("trusted-proxy", "", "optional exact reverse proxy IP or CIDR; empty trusts none")
	hashPassword := flag.Bool("hash-password", false, "read password from stdin and output bcrypt hash")
	releaseDir := flag.String("release-dir", "", "update artifacts directory; defaults to releases beside database")
	releaseKeysFile := flag.String("release-public-keys-file", "", "JSON key_id to base64 Ed25519 public keys; alternatively SEEKBOX_RELEASE_PUBLIC_KEYS")
	healthcheck := flag.Bool("healthcheck", false, "check local HTTP and database readiness")
	flag.Parse()
	if *hashPassword {
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 1025))
		if err != nil {
			log.Fatal("cannot read password")
		}
		password = bytes.TrimSuffix(password, []byte("\n"))
		password = bytes.TrimSuffix(password, []byte("\r"))
		if len(password) < 12 || len(password) > 72 {
			log.Fatal("password must be 12–72 bytes")
		}
		hash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
		if err != nil {
			log.Fatal("cannot hash password")
		}
		fmt.Println(string(hash))
		return
	}
	if *healthcheck {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	parsed, err := url.Parse(*origin)
	localHTTP := err == nil && *allowLocalHTTP && parsed.Scheme == "http" && oneOf(parsed.Hostname(), "localhost", "127.0.0.1", "::1")
	if err != nil || (parsed.Scheme != "https" && !localHTTP) || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		log.Fatal("origin must be an HTTPS origin without path")
	}
	hash, err := os.ReadFile(*hashFile)
	if err != nil {
		log.Fatal("cannot read administrator password hash file")
	}
	hash = bytes.TrimSpace(hash)
	if _, err = bcrypt.Cost(hash); err != nil {
		log.Fatal("administrator password hash is not valid bcrypt")
	}
	store, err := openStore(*dbPath, *seed)
	if err != nil {
		log.Fatalf("cannot open configuration database: %v", err)
	}
	defer store.db.Close()
	a := newApp(store, *origin, *web, hash)
	if err = a.loadScanConfig(); err != nil {
		log.Fatal(err)
	}
	if *releaseDir != "" {
		a.releaseDir = *releaseDir
	}
	a.releaseKeys, err = loadReleaseKeys(*releaseKeysFile, os.Getenv("SEEKBOX_RELEASE_PUBLIC_KEYS"))
	if err != nil {
		log.Fatal(err)
	}
	if *trustedProxy != "" {
		a.trustedProxies = []string{*trustedProxy}
	}
	maintenanceCtx, stopMaintenance := context.WithCancel(context.Background())
	defer stopMaintenance()
	go a.maintenance(maintenanceCtx)
	server := &http.Server{Addr: *addr, Handler: releaseTransferTimeouts(a.router()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() {
		log.Printf("public configuration server listening on %s", *addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown failed: %v", err)
	}
}
