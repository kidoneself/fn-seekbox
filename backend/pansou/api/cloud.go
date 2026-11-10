package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"pansou/cloud"
	"pansou/util/json"
)

// RegisterCloudRoutes relies on the local fnOS gateway to supply the user ID.
// Mutations additionally require a header that cannot be sent by a cross-origin
// form or by the existing CORS policy, which intentionally does not allow it.
func RegisterCloudRoutes(router *gin.Engine, service *cloud.Service) {
	g := router.Group("/api/cloud", cloudIdentity)
	g.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if service == nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "网盘模块未能启动，请检查应用日志和数据目录"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	g.GET("/center", func(c *gin.Context) {
		status, err := service.CenterStatus(cloudOwner(c))
		if !cloudError(c, err) {
			c.JSON(200, status)
		}
	})
	g.PUT("/center", func(c *gin.Context) {
		var req struct {
			Daily     *bool `json:"daily_enabled"`
			Reporting *bool `json:"reporting_enabled"`
		}
		if !cloudJSON(c, &req) {
			return
		}
		status, err := service.SetCenterConfig(cloudOwner(c), req.Daily, req.Reporting)
		if !cloudError(c, err) {
			c.JSON(200, status)
		}
	})
	g.GET("/center/sites", func(c *gin.Context) {
		sites, stale, err := service.CenterSites(c.Request.Context(), cloudOwner(c))
		if !cloudError(c, err) {
			c.JSON(200, gin.H{"sites": sites, "stale": stale})
		}
	})
	g.GET("/accounts/:id/check-source", func(c *gin.Context) {
		result, err := service.FunctionSourcePreview(c.Request.Context(), cloudOwner(c), c.Param("id"))
		if !cloudError(c, err) {
			c.JSON(200, result)
		}
	})
	g.GET("/function-checks", func(c *gin.Context) {
		checks, err := service.FunctionChecks(cloudOwner(c))
		if !cloudError(c, err) {
			c.JSON(200, gin.H{"checks": checks})
		}
	})
	g.POST("/accounts/:id/function-check", func(c *gin.Context) {
		var req struct{}
		if !cloudJSON(c, &req) {
			return
		}
		check, err := service.QueueFunctionCheck(cloudOwner(c), c.Param("id"))
		if !cloudError(c, err) {
			c.JSON(202, gin.H{"check": check})
		}
	})
	g.POST("/function-checks/:id/cleanup", func(c *gin.Context) {
		var req struct{}
		if !cloudJSON(c, &req) {
			return
		}
		if !cloudError(c, service.QueueFunctionCleanup(cloudOwner(c), c.Param("id"))) {
			c.JSON(202, gin.H{"ok": true})
		}
	})
	g.GET("/providers", func(c *gin.Context) { c.JSON(200, gin.H{"providers": service.Providers()}) })
	g.POST("/qr", func(c *gin.Context) {
		var req struct {
			Provider  string `json:"provider"`
			AccountID string `json:"account_id"`
			Name      string `json:"name"`
		}
		if !cloudJSON(c, &req) {
			return
		}
		state, err := service.StartQR(cloudOwner(c), req.Provider, req.AccountID, req.Name)
		if !cloudError(c, err) {
			c.JSON(202, gin.H{"session": state})
		}
	})
	g.GET("/qr/:id", func(c *gin.Context) {
		state, err := service.QRStatus(cloudOwner(c), c.Param("id"))
		if !cloudError(c, err) {
			c.JSON(200, gin.H{"session": state})
		}
	})
	g.POST("/qr/:id/complete", func(c *gin.Context) {
		var req struct {
			Name string `json:"name"`
		}
		if !cloudJSON(c, &req) {
			return
		}
		state, err := service.CompleteQR(c.Request.Context(), cloudOwner(c), c.Param("id"), req.Name)
		if !cloudError(c, err) {
			c.JSON(200, gin.H{"session": state})
		}
	})
	g.DELETE("/qr/:id", func(c *gin.Context) {
		if !cloudError(c, service.CancelQR(cloudOwner(c), c.Param("id"))) {
			c.JSON(200, gin.H{"ok": true})
		}
	})
	g.GET("/accounts", func(c *gin.Context) {
		accounts, err := service.Accounts(cloudOwner(c))
		if cloudError(c, err) {
			return
		}
		c.JSON(200, gin.H{"accounts": accounts})
	})
	connect := func(c *gin.Context) {
		var body struct {
			Provider    string            `json:"provider"`
			Name        string            `json:"name"`
			Credentials cloud.Credentials `json:"credentials"`
		}
		if !cloudJSON(c, &body) {
			return
		}
		account, err := service.Connect(c.Request.Context(), cloudOwner(c), c.Param("id"), body.Provider, body.Name, body.Credentials)
		if cloudError(c, err) {
			return
		}
		c.JSON(200, gin.H{"account": account})
	}
	g.POST("/accounts", connect)
	g.PUT("/accounts/:id", connect)
	g.POST("/accounts/:id/check", func(c *gin.Context) {
		account, err := service.CheckAccount(c.Request.Context(), cloudOwner(c), c.Param("id"))
		if cloudError(c, err) {
			return
		}
		c.JSON(200, gin.H{"account": account})
	})
	g.DELETE("/accounts/:id", func(c *gin.Context) {
		if cloudError(c, service.DeleteAccount(cloudOwner(c), c.Param("id"))) {
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
	g.GET("/accounts/:id/folders", func(c *gin.Context) {
		parent := c.Query("parent")
		if len(parent) > 2048 || strings.ContainsAny(parent, "\x00\r\n") {
			c.JSON(400, gin.H{"error": "目录参数无效"})
			return
		}
		folders, err := service.Folders(c.Request.Context(), cloudOwner(c), c.Param("id"), parent)
		if cloudError(c, err) {
			return
		}
		c.JSON(200, gin.H{"folders": folders})
	})
	g.POST("/transfers", func(c *gin.Context) {
		var req cloud.TransferRequest
		if !cloudJSON(c, &req) {
			return
		}
		job, err := service.Transfer(cloudOwner(c), req)
		if cloudError(c, err) {
			return
		}
		c.JSON(202, gin.H{"job": job})
	})
	g.GET("/jobs", func(c *gin.Context) {
		jobs, err := service.Jobs(cloudOwner(c))
		if cloudError(c, err) {
			return
		}
		c.JSON(200, gin.H{"jobs": jobs})
	})
	g.GET("/jobs/:id", func(c *gin.Context) {
		job, err := service.Job(cloudOwner(c), c.Param("id"))
		if cloudError(c, err) {
			return
		}
		c.JSON(200, gin.H{"job": job})
	})
	g.GET("/subscriptions", func(c *gin.Context) {
		subs, err := service.Subscriptions(cloudOwner(c))
		if cloudError(c, err) {
			return
		}
		c.JSON(200, gin.H{"subscriptions": subs})
	})
	g.POST("/subscriptions", func(c *gin.Context) {
		var req struct {
			cloud.TransferRequest
			IntervalMinutes int `json:"interval_minutes"`
		}
		if !cloudJSON(c, &req) {
			return
		}
		sub, job, err := service.Subscribe(cloudOwner(c), req.TransferRequest, req.IntervalMinutes)
		if cloudError(c, err) {
			return
		}
		c.JSON(202, gin.H{"subscription": sub, "job": job})
	})
	g.PATCH("/subscriptions/:id", func(c *gin.Context) {
		var req struct {
			Enabled         *bool `json:"enabled"`
			FilterAds       *bool `json:"filter_ads"`
			IntervalMinutes *int  `json:"interval_minutes"`
		}
		if !cloudJSON(c, &req) {
			return
		}
		sub, err := service.UpdateSubscription(cloudOwner(c), c.Param("id"), req.Enabled, req.IntervalMinutes, req.FilterAds)
		if cloudError(c, err) {
			return
		}
		c.JSON(200, gin.H{"subscription": sub})
	})
	g.POST("/subscriptions/:id/check", func(c *gin.Context) {
		job, err := service.CheckSubscription(cloudOwner(c), c.Param("id"))
		if cloudError(c, err) {
			return
		}
		c.JSON(202, gin.H{"job": job})
	})
	g.DELETE("/subscriptions/:id", func(c *gin.Context) {
		if cloudError(c, service.DeleteSubscription(cloudOwner(c), c.Param("id"))) {
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
}

func cloudIdentity(c *gin.Context) {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	ip := net.ParseIP(host)
	owner := strings.TrimSpace(c.GetHeader("X-Trim-Userid"))
	if err != nil || ip == nil || !ip.IsLoopback() || owner == "" || len(owner) > 256 || strings.ContainsAny(owner, "\x00\r\n") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请通过飞牛桌面登录后使用网盘功能"})
		return
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		mediaType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if c.GetHeader("X-Seekbox-Action") != "1" || (c.Request.ContentLength != 0 && mediaType != "application/json") || c.GetHeader("Sec-Fetch-Site") == "cross-site" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "请从应用页面发起网盘操作"})
			return
		}
		if c.Request.ContentLength > 64<<10 {
			c.AbortWithStatusJSON(400, gin.H{"error": "请求内容过长"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, (64<<10)+1))
		if err != nil || len(body) > 64<<10 {
			c.AbortWithStatusJSON(400, gin.H{"error": "请求格式无效或内容过长"})
			return
		}
		if len(bytes.TrimSpace(body)) > 0 {
			var raw json.RawMessage
			if json.Unmarshal(body, &raw) != nil {
				c.AbortWithStatusJSON(400, gin.H{"error": "请求格式无效"})
				return
			}
		}
		c.Set("cloud_body", body)
	}
	c.Set("cloud_owner", owner)
	c.Next()
}

func cloudOwner(c *gin.Context) string { return c.GetString("cloud_owner") }
func cloudJSON(c *gin.Context, value any) bool {
	body, _ := c.Get("cloud_body")
	raw, _ := body.([]byte)
	if err := json.Unmarshal(raw, value); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "请求格式无效或内容过长"})
		return false
	}
	return true
}
func cloudError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	status := http.StatusBadRequest
	if errors.Is(err, cloud.ErrMaintenance) {
		c.Header("Retry-After", "30")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "code": "UPDATE_MAINTENANCE"})
		return true
	}
	if errors.Is(err, cloud.ErrNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, cloud.ErrBusy) {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"error": err.Error()})
	return true
}
