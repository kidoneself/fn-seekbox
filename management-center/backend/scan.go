package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var scanSessionPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func (a *app) loadScanConfig() error {
	a.scanURL = strings.TrimRight(os.Getenv("SEEKBOX_SCAN_INTERNAL_URL"), "/")
	file := os.Getenv("SEEKBOX_SCAN_SECRET_FILE")
	if a.scanURL == "" && file == "" {
		return nil
	}
	u, err := url.Parse(a.scanURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("invalid internal scan service URL")
	}
	value, err := os.ReadFile(file)
	if err != nil {
		return errors.New("cannot read internal scan service key")
	}
	a.scanSecret = strings.TrimSpace(string(value))
	if len(a.scanSecret) < 32 {
		return errors.New("invalid internal scan service key")
	}
	return nil
}

func (a *app) proxyScan(c *gin.Context, route string, admin bool) {
	if a.scanURL == "" || a.scanSecret == "" {
		fail(c, 503, "SCAN_UNAVAILABLE", "扫码中心尚未配置")
		return
	}
	if !admin && c.GetHeader("Origin") != "" {
		fail(c, 403, "SERVER_ONLY", "仅允许 NAS 后端调用扫码服务")
		return
	}
	var raw []byte
	if c.Request.Method != "GET" {
		if !clientJSON(c) {
			return
		}
		var err error
		raw, err = io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8192))
		if err != nil {
			fail(c, 413, "BODY_TOO_LARGE", "扫码请求过大")
			return
		}
		var body map[string]any
		if decodeStrict(bytes.NewReader(raw), &body) != nil || body == nil {
			fail(c, 400, "INVALID_FIELD", "请求需要 JSON 对象")
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, c.Request.Method, a.scanURL+route, bytes.NewReader(raw))
	if err != nil {
		fail(c, 503, "SCAN_UNAVAILABLE", "扫码中心暂时不可用")
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.scanSecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scan-Client", c.GetString("installation_id"))
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		fail(c, 503, "SCAN_UNAVAILABLE", "扫码中心暂时不可用，请稍后重试")
		return
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		fail(c, 502, "SCAN_INVALID_RESPONSE", "扫码中心响应异常")
		return
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result == nil {
		fail(c, 502, "SCAN_INVALID_RESPONSE", "扫码中心响应异常")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, _ := result["code"].(string)
		message, _ := result["message"].(string)
		if !releaseErrorPattern.MatchString(code) {
			code = "SCAN_UNAVAILABLE"
		}
		if len(message) > 200 || message == "" {
			message = "扫码请求未完成，请稍后重试"
		}
		if response.StatusCode == 429 {
			c.Header("Retry-After", "5")
		}
		status := response.StatusCode
		if status < 400 || status > 599 {
			status = 502
		}
		fail(c, status, code, message)
		return
	}
	reply(c, 200, 0, "ok", result)
}

func (a *app) registerScan(r *gin.Engine, admin *gin.RouterGroup) {
	client := r.Group("/api/client/scan", a.reportAuth)
	client.POST("/sessions", func(c *gin.Context) { a.proxyScan(c, "/v1/sessions", false) })
	for _, method := range []string{"GET", "DELETE"} {
		client.Handle(method, "/sessions/:id", func(c *gin.Context) {
			if !scanSessionPattern.MatchString(c.Param("id")) {
				fail(c, 400, "INVALID_FIELD", "会话标识无效")
				return
			}
			a.proxyScan(c, "/v1/sessions/"+c.Param("id"), false)
		})
	}
	for _, action := range []string{"claim", "ack"} {
		action := action
		client.POST("/sessions/:id/"+action, func(c *gin.Context) {
			if !scanSessionPattern.MatchString(c.Param("id")) {
				fail(c, 400, "INVALID_FIELD", "会话标识无效")
				return
			}
			a.proxyScan(c, "/v1/sessions/"+c.Param("id")+"/"+action, false)
		})
	}
	admin.GET("/scan/status", func(c *gin.Context) { a.proxyScan(c, "/internal/status", true) })
}

func enrichScanEndpoint(e *Endpoint) {
	if e.Path == adminPrefix+"/scan/status" {
		e.Name = "扫码中心运行状态"
		e.Group = "扫码中心"
		e.Description = "当前进程的并发、排队、内存及四种网盘的计数；计数在服务重启后归零。不会返回 Cookie、二维码或网盘账号。"
		e.Response = gin.H{"code": 0, "data": gin.H{"version": "1.0.0", "active": 0, "queued": 0, "providers": []any{}, "limits": gin.H{}}}
		return
	}
	if !strings.HasPrefix(e.Path, "/api/client/scan/") {
		return
	}
	e.Name = "网盘扫码授权"
	e.Group = "扫码中心"
	e.Description = "仅供 NAS 后端调用，使用原实例 report_token；每次校验撤销状态，不接受浏览器 Origin、管理 Cookie 或普通 API Key。支持 baidu/quark/uc/xunlei。凭据仅由 claim 返回；ack 在 NAS 验证并保存后调用。"
	if e.Method == "POST" && strings.HasSuffix(e.Path, "/sessions") {
		e.Request = gin.H{"provider": "xunlei", "account_id": "<随机账号关联标识>", "request_id": "<随机幂等请求标识>"}
	}
	if strings.HasSuffix(e.Path, "/claim") || strings.HasSuffix(e.Path, "/ack") {
		e.Request = gin.H{"account_id": "<创建时的账号关联标识>", "claim_id": "<固定随机领取标识>"}
	}
	e.Errors = append(e.Errors, "SCAN_UNAVAILABLE", "RATE_LIMITED", "NOT_FOUND", "NOT_READY", "CLAIM_CONFLICT", "SERVER_ONLY")
	e.Response = gin.H{"code": 0, "data": gin.H{"id": "<会话 UUID>", "status": "queued", "expires_at": 0, "poll_after_ms": 3000, "queue_position": 1}}
	if strings.HasSuffix(e.Path, "/claim") {
		e.Response = gin.H{"code": 0, "data": gin.H{"provider": "xunlei", "account_id": "<关联标识>", "credentials": gin.H{"web_session": "<仅服务端接收的登录凭据>"}}}
	}
	if strings.HasSuffix(e.Path, "/ack") {
		e.Response = gin.H{"code": 0, "data": gin.H{"acknowledged": true}}
	}
	if e.Method == "DELETE" {
		e.Response = gin.H{"code": 0, "data": gin.H{"cancelled": true}}
	}
}
