package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
)

type Endpoint struct {
	Parameters  []Parameter `json:"parameters"`
	ID          string      `json:"id"`
	Method      string      `json:"method"`
	Path        string      `json:"path"`
	Group       string      `json:"group"`
	Name        string      `json:"name"`
	Auth        string      `json:"auth"`
	Description string      `json:"description"`
	Request     any         `json:"request_example"`
	Response    any         `json:"response_example"`
	Errors      []string    `json:"errors"`
}
type AdminKey struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Hash        string     `json:"token_hash,omitempty"`
	Permissions []string   `json:"permissions"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
}
type Audit struct {
	ID         string    `json:"request_id"`
	ReceivedAt time.Time `json:"received_at"`
	Actor      string    `json:"actor"`
	Endpoint   string    `json:"endpoint_id"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"duration_ms"`
}

func endpointID(c *gin.Context) string { return c.Request.Method + " " + c.FullPath() }
func (a *app) requestContext(c *gin.Context) {
	if c.GetHeader("Authorization") != "" {
		c.Set("actor", "unverified_bearer")
	}
	start := time.Now()
	id := randomID("req_")
	c.Set("request_id", id)
	c.Header("X-Request-ID", id)
	c.Next()
	if strings.Contains(c.FullPath(), "/api/") {
		actor := c.GetString("actor")
		if actor == "" {
			if a.authenticated(c) {
				actor = "admin"
			} else {
				actor = "anonymous"
			}
		}
		a.store.db.Update(func(tx *bolt.Tx) error {
			return put(tx, "audit", a.now().UTC().Format(time.RFC3339Nano)+"|"+id, Audit{id, a.now(), actor, endpointID(c), c.Writer.Status(), time.Since(start).Milliseconds()})
		})
	}
}
func (a *app) authorizeAdminKey(c *gin.Context) {
	token := bearer(c)
	var key AdminKey
	err := a.store.db.View(func(tx *bolt.Tx) error {
		id := bucket(tx, "admin_key_hashes").Get([]byte(digest(token)))
		if id != nil {
			get(tx, "admin_keys", string(id), &key)
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if token == "" || key.ID == "" || !key.Enabled || (key.ExpiresAt != nil && !a.now().Before(*key.ExpiresAt)) {
		fail(c, 401, "UNAUTHORIZED", "API Key 无效、停用或已到期")
		return
	}
	if !oneOf(endpointID(c), key.Permissions...) {
		fail(c, 403, "FORBIDDEN", "API Key 未获此操作权限")
		return
	}
	if a.limited("key:"+key.ID, 600, time.Minute) {
		c.Header("Retry-After", "60")
		fail(c, 429, "RATE_LIMITED", "调用方请求过于频繁")
		return
	}
	c.Set("actor", "key:"+key.ID)
	c.Next()
}
func (a *app) sessionOnly(c *gin.Context) {
	if c.GetHeader("Authorization") != "" || !a.authenticated(c) {
		fail(c, 403, "ADMIN_SESSION_REQUIRED", "此操作仅允许后台管理员登录身份执行")
		return
	}
	c.Next()
}
func (a *app) apiKeys(c *gin.Context) {
	if c.Request.Method == "GET" {
		items := []AdminKey{}
		err := a.store.db.View(func(tx *bolt.Tx) error {
			for _, k := range rows[AdminKey](tx, "admin_keys") {
				k.Hash = ""
				items = append(items, k)
			}
			return nil
		})
		if err != nil {
			a.dbError(c, err)
			return
		}
		reply(c, 200, 0, "ok", items)
		return
	}
	var in struct {
		Name        string     `json:"name"`
		Permissions []string   `json:"permissions"`
		Enabled     *bool      `json:"enabled"`
		ExpiresAt   *time.Time `json:"expires_at"`
	}
	if !readBody(c, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 100 {
		fail(c, 400, "INVALID_FIELD", "请填写调用方名称")
		return
	}
	known := map[string]bool{}
	for _, e := range a.registry {
		if e.Auth == "admin" {
			known[e.ID] = true
		}
	}
	for _, id := range in.Permissions {
		if !known[id] {
			fail(c, 400, "INVALID_PERMISSION", "权限必须是已注册且允许Key调用的具体操作")
			return
		}
	}
	if in.Permissions == nil {
		in.Permissions = []string{}
	}
	sort.Strings(in.Permissions)
	key := AdminKey{ID: c.Param("id"), Name: in.Name, Permissions: in.Permissions, Enabled: true, ExpiresAt: in.ExpiresAt, CreatedAt: a.now()}
	if in.Enabled != nil {
		key.Enabled = *in.Enabled
	}
	var token string
	var missing bool
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		if key.ID == "" {
			key.ID = randomID("key_")
			token = randomID("skc_")
			key.Hash = digest(token)
		} else {
			var old AdminKey
			if !get(tx, "admin_keys", key.ID, &old) {
				missing = true
				return nil
			}
			key.Hash = old.Hash
			key.CreatedAt = old.CreatedAt
		}
		if e := put(tx, "admin_keys", key.ID, key); e != nil {
			return e
		}
		return bucket(tx, "admin_key_hashes").Put([]byte(key.Hash), []byte(key.ID))
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if missing {
		fail(c, 404, "NOT_FOUND", "调用方不存在")
		return
	}
	key.Hash = ""
	out := gin.H{"key": key}
	if token != "" {
		out["api_key"] = token
	}
	reply(c, 200, 0, "ok", out)
}
func (a *app) rotateKey(c *gin.Context) {
	var input struct{}
	if !readBody(c, &input) {
		return
	}
	token := randomID("skc_")
	missing := false
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		var key AdminKey
		if !get(tx, "admin_keys", c.Param("id"), &key) {
			missing = true
			return nil
		}
		if err := bucket(tx, "admin_key_hashes").Delete([]byte(key.Hash)); err != nil {
			return err
		}
		key.Hash = digest(token)
		if err := put(tx, "admin_keys", key.ID, key); err != nil {
			return err
		}
		return bucket(tx, "admin_key_hashes").Put([]byte(key.Hash), []byte(key.ID))
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if missing {
		fail(c, 404, "NOT_FOUND", "调用方不存在")
		return
	}
	reply(c, 200, 0, "ok", gin.H{"api_key": token})
}
func (a *app) revokeInstance(c *gin.Context) {
	var input struct{}
	if !readBody(c, &input) {
		return
	}
	missing := false
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		var inst Instance
		if !get(tx, "instances", c.Param("id"), &inst) {
			missing = true
			return nil
		}
		inst.Revoked = true
		inst.CachedToken = ""
		if err := bucket(tx, "report_tokens").Delete([]byte(inst.TokenHash)); err != nil {
			return err
		}
		return put(tx, "instances", inst.ID, inst)
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if missing {
		fail(c, 404, "NOT_FOUND", "实例不存在")
		return
	}
	reply(c, 200, 0, "ok", gin.H{"revoked": true})
}
func (a *app) clientSettings(c *gin.Context) {
	var s Settings
	if c.Request.Method == "GET" {
		err := a.store.db.View(func(tx *bolt.Tx) error { s = settings(tx); return nil })
		if err != nil {
			a.dbError(c, err)
			return
		}
		reply(c, 200, 0, "ok", s)
		return
	}
	var in struct {
		Heartbeat        *int `json:"heartbeat_interval_seconds"`
		BatchItems       *int `json:"event_batch_max_items"`
		BatchBytes       *int `json:"event_batch_max_bytes"`
		EventAge         *int `json:"event_max_age_days"`
		Retention        *int `json:"raw_retention_days"`
		SummaryRetention *int `json:"summary_retention_days"`
	}
	if !readBody(c, &in) {
		return
	}
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		s = settings(tx)
		for _, v := range []struct {
			src *int
			dst *int
		}{{in.Heartbeat, &s.Heartbeat}, {in.BatchItems, &s.BatchItems}, {in.BatchBytes, &s.BatchBytes}, {in.EventAge, &s.EventAge}, {in.Retention, &s.Retention}, {in.SummaryRetention, &s.SummaryRetention}} {
			if v.src != nil {
				*v.dst = *v.src
			}
		}
		if s.Heartbeat < 60 || s.Heartbeat > 1800 || s.BatchItems < 1 || s.BatchItems > 100 || s.BatchBytes < 16384 || s.BatchBytes > 262144 || s.EventAge < 1 || s.EventAge > 7 || s.Retention < 30 || s.Retention > 365 || s.SummaryRetention < 365 || s.SummaryRetention > 730 {
			return errors.New("invalid settings")
		}
		s.ConfigVersion = nextVersion(s.ConfigVersion)
		return put(tx, "settings", "client", s)
	})
	if err != nil {
		if err.Error() == "invalid settings" {
			fail(c, 400, "INVALID_FIELD", "参数范围：心跳60–1800秒，批次1–100条/16–256KiB，补报1–7天，原始保留30–365天，汇总365–730天")
		} else {
			a.dbError(c, err)
		}
		return
	}
	reply(c, 200, 0, "ok", s)
}
func (a *app) deleteSite(c *gin.Context) {
	var in struct{}
	if !readBody(c, &in) {
		return
	}
	id := c.Param("id")
	err := a.store.db.Update(func(tx *bolt.Tx) error {
		cfg, err := readConfigTx(tx)
		if err != nil {
			return err
		}
		idx := -1
		for i, v := range cfg.SearchSites {
			if v.ID == id {
				idx = i
			}
		}
		if idx < 0 {
			return errNotFound
		}
		cfg.SearchSites = append(cfg.SearchSites[:idx], cfg.SearchSites[idx+1:]...)
		raw, _ := json.Marshal(cfg)
		if err = bucket(tx, "site_tombstones").Put([]byte(id), []byte("deleted")); err != nil {
			return err
		}
		return tx.Bucket(configBucket).Put(configKey, raw)
	})
	if errors.Is(err, errNotFound) {
		fail(c, 404, "NOT_FOUND", "站点不存在")
		return
	}
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 200, 0, "ok", nil)
}
func (a *app) registerTelemetry(r *gin.Engine, admin *gin.RouterGroup) {
	r.POST("/api/client/register", a.registerClient)
	client := r.Group("/api/client", a.reportAuth)
	client.GET("/config", a.clientConfig)
	client.POST("/heartbeat", a.heartbeat)
	client.POST("/events/batch", a.events)
	admin.GET("/analytics/overview", a.overview)
	admin.GET("/analytics/trends", a.trends)
	for _, resource := range []string{"instances", "accounts", "checks", "events", "slots"} {
		resource := resource
		admin.GET("/analytics/"+resource, func(c *gin.Context) { a.listAnalytics(c, resource) })
	}
	admin.GET("/analytics/checks/:run_id", a.checkDetail)
	admin.GET("/client-settings", a.clientSettings)
	admin.PATCH("/client-settings", a.clientSettings)
	admin.POST("/instances/:id/revoke", a.revokeInstance)
	admin.DELETE("/sites/:id", a.deleteSite)
	admin.GET("/endpoints", func(c *gin.Context) { reply(c, 200, 0, "ok", a.visibleEndpoints(c)) })
	admin.GET("/openapi", a.openapi)
	admin.GET("/audit", func(c *gin.Context) { a.listAnalytics(c, "audit") })
	keyAdmin := admin.Group("", a.sessionOnly)
	keyAdmin.GET("/api-keys", a.apiKeys)
	keyAdmin.POST("/api-keys", a.apiKeys)
	keyAdmin.PUT("/api-keys/:id", a.apiKeys)
	keyAdmin.POST("/api-keys/:id/rotate", a.rotateKey)
}
func describeRoutes(routes gin.RoutesInfo) []Endpoint {
	out := []Endpoint{}
	for _, route := range routes {
		if !strings.Contains(route.Path, "/api/") || route.Method == "OPTIONS" {
			continue
		}
		auth := "public"
		group := "公共配置"
		if strings.HasPrefix(route.Path, "/api/client") {
			group = "客户端上报"
			auth = "report_token"
			if strings.HasSuffix(route.Path, "/register") || route.Path == "/api/client/releases/latest" || strings.HasSuffix(route.Path, "/artifact") {
				auth = "public"
			}
		}
		if strings.HasPrefix(route.Path, adminPrefix) {
			group = "后台管理"
			auth = "admin"
			if strings.Contains(route.Path, "/api-keys") || strings.HasSuffix(route.Path, "/logout") || strings.HasSuffix(route.Path, "/session") || (strings.HasPrefix(route.Path, adminPrefix+"/releases") && route.Method != "GET") {
				auth = "admin_session"
			}
			if strings.HasSuffix(route.Path, "/login") {
				auth = "password"
			}
		}
		name := strings.TrimPrefix(route.Path, adminPrefix)
		body := any(nil)
		description := "真实接口；字段与协议详见随包 requirements-v1.1.md。所有响应带 request_id；错误 data=null。"
		switch {
		case strings.HasSuffix(route.Path, "/check-link"):
			name = "获取网盘测试源"
			description = "provider 必填且只能出现一次：baidu / quark / xunlei。未配置404、停用/到期503；每次读取最新配置。"
		case strings.HasSuffix(route.Path, "/search-sites"):
			name = "获取全部搜索来源"
			description = "包括停用记录，按 order、id 升序；网址仅为配置，不自动新增搜索插件。"
		case strings.HasSuffix(route.Path, "/register"):
			name = "登记匿名安装实例"
			body = gin.H{"schema_version": 1, "client_instance_id": "<持久随机UUID>", "registration_nonce": "<至少32随机字节的base64url>", "app_version": "1.5.13", "platform": "fnos", "arch": "amd64"}
		case strings.HasSuffix(route.Path, "/events/batch"):
			name = "批量上报检查事件"
			body = gin.H{"schema_version": 1, "batch_id": "<随机ID>", "events": []any{}}
			description = "最多100条/256KiB；四类account_check事件，按实例+event_id幂等、实例+run_id计次。业务冲突按条拒收；永久拒收不重试。"
		case strings.HasSuffix(route.Path, "/heartbeat"):
			name = "上报运行快照"
			body = gin.H{"schema_version": 1, "snapshot_seq": 1, "reported_at": "<RFC3339>", "app_version": "1.5.13", "platform": "fnos", "arch": "amd64", "accounts_complete": true, "accounts": []any{}, "subscriptions": gin.H{"total": 0, "enabled": 0}, "active_jobs": 0}
		case strings.HasSuffix(route.Path, "/login"):
			name = "后台登录"
			body = gin.H{"password": "<管理员密码>"}
		case strings.Contains(route.Path, "/check-links/"):
			name = "维护网盘测试源"
			body = gin.H{"source_id": "probe_baidu_01", "share_url": "<真实HTTPS分享>", "password": "", "file_path": "功能检查.txt", "enabled": false, "expires_at": nil}
		case strings.Contains(route.Path, "/sites"):
			name = "管理搜索来源"
			body = gin.H{"name": "<名称>", "url": "<实际配置网址>", "enabled": true, "order": 1}
			if route.Method == "POST" {
				body.(gin.H)["id"] = "<稳定插件ID>"
			}
		case strings.Contains(route.Path, "/analytics/"):
			name = "统计与运行明细"
			description = "支持from/to RFC3339左闭右开、Asia/Shanghai、provider、app_version、installation_id等；详情筛选及分页见协议。搜索/转存扩展指标尚未启用，明确返回METRIC_NOT_SUPPORTED。"
		}
		out = append(out, Endpoint{nil, route.Method + " " + route.Path, route.Method, route.Path, group, name, auth, description, body, gin.H{"code": 0, "message": "ok", "request_id": "<请求ID>", "data": "<取决于接口；此处为结构说明，非真实结果>"}, []string{"INVALID_FIELD", "UNAUTHORIZED", "FORBIDDEN", "NOT_FOUND", "CONFLICT", "RATE_LIMITED", "SERVICE_UNAVAILABLE"}})
	}
	for i := range out {
		enrichEndpoint(&out[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (a *app) openapi(c *gin.Context) {
	paths := gin.H{}
	for _, e := range a.visibleEndpoints(c) {
		p := e.Path
		for _, seg := range strings.Split(p, "/") {
			if strings.HasPrefix(seg, ":") {
				p = strings.ReplaceAll(p, seg, "{"+seg[1:]+"}")
			}
		}
		if paths[p] == nil {
			paths[p] = gin.H{}
		}
		op := gin.H{"operationId": e.ID, "summary": e.Name, "description": e.Description, "responses": gin.H{"200": gin.H{"description": "统一code/message/request_id/data响应"}, "400": gin.H{"description": "参数错误"}, "401": gin.H{"description": "凭证无效"}, "403": gin.H{"description": "无操作权限"}}}
		if e.Request != nil {
			op["requestBody"] = gin.H{"required": true, "content": gin.H{"application/json": gin.H{"example": e.Request}}}
		}
		if e.Auth != "public" && e.Auth != "password" {
			op["security"] = []any{gin.H{"bearerAuth": []string{}}}
		}
		enrichOpenAPI(e, op)
		paths[p].(gin.H)[strings.ToLower(e.Method)] = op
	}
	reply(c, 200, 0, "ok", gin.H{"openapi": "3.0.3", "info": gin.H{"title": "小白搜盘管理服务", "version": "1.1"}, "servers": []any{gin.H{"url": a.origin}}, "paths": paths, "components": gin.H{"securitySchemes": gin.H{"bearerAuth": gin.H{"type": "http", "scheme": "bearer"}, "adminSession": gin.H{"type": "apiKey", "in": "cookie", "name": cookieName}}}})
}
func (a *app) signCursor(raw []byte) string {
	m := hmac.New(sha256.New, a.passwordHash)
	m.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (a *app) verifyCursor(s string) ([]byte, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid cursor")
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	sig, e2 := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || e2 != nil {
		return nil, errors.New("invalid cursor")
	}
	m := hmac.New(sha256.New, a.passwordHash)
	m.Write(raw)
	if !hmac.Equal(m.Sum(nil), sig) {
		return nil, errors.New("invalid cursor")
	}
	return raw, nil
}

var _ = fmt.Sprintf
