package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"pansou/buildinfo"
	"pansou/update"
)

type UpdateManager interface {
	Status() update.Status
	Check(context.Context) (update.Status, error)
	Install(context.Context) (update.Status, error)
	SetAuto(bool) error
}

// Updating the running application is instance-wide. A valid fnOS user alone
// does not grant this permission; deployment must explicitly nominate owners.
func RegisterUpdateRoutes(router *gin.Engine, manager UpdateManager, adminIDs []string) {
	admins := map[string]bool{}
	for _, id := range adminIDs {
		if id = strings.TrimSpace(id); id != "" {
			admins[id] = true
		}
	}
	var installing, checking atomic.Bool
	g := router.Group("/api/update", cloudIdentity)
	g.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if c.Request.Method != http.MethodGet && !admins[cloudOwner(c)] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前账号没有管理应用更新的权限", "code": "UPDATE_ADMIN_REQUIRED"})
			return
		}
		c.Next()
	})
	respond := func(c *gin.Context, code int, status update.Status) {
		// Explicitly enumerate public fields; never serialize updater configuration,
		// trusted key material, executable paths or raw filesystem errors.
		var release any
		if status.Release != nil {
			m := status.Release.Manifest
			release = gin.H{"release_id": status.Release.ID, "version": m.Version, "notes": m.Notes, "size": m.Size, "arch": m.Arch}
		}
		publicError := ""
		if status.Error != "" {
			publicError = "本次更新未完成，请重试或查看应用日志"
			if !status.Enabled {
				publicError = "在线更新未启用，请检查发布源和公钥配置"
			}
		}
		c.JSON(code, gin.H{"enabled": status.Enabled, "auto_enabled": status.AutoEnabled, "current_version": status.CurrentVersion, "phase": status.Phase, "error": publicError, "available": status.Available, "release": release, "last_checked_at": status.LastCheckedAt, "updated_at": status.UpdatedAt, "can_manage": manager != nil && admins[cloudOwner(c)]})
	}
	unavailable := func(c *gin.Context) bool {
		if manager != nil {
			return false
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "在线更新尚未配置，请联系应用管理员", "code": "UPDATE_UNAVAILABLE"})
		return true
	}
	g.GET("/status", func(c *gin.Context) {
		if manager == nil {
			respond(c, 200, update.Status{CurrentVersion: buildinfo.Version, Phase: "disabled"})
			return
		}
		respond(c, 200, manager.Status())
	})
	g.POST("/check", func(c *gin.Context) {
		if unavailable(c) || !updateJSON(c, &struct{}{}) {
			return
		}
		if !checking.CompareAndSwap(false, true) {
			c.JSON(409, gin.H{"error": "正在检查新版本", "code": "UPDATE_BUSY"})
			return
		}
		go func() {
			defer checking.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			_, _ = manager.Check(ctx)
		}()
		respond(c, http.StatusAccepted, manager.Status())
	})
	g.POST("/install", func(c *gin.Context) {
		if unavailable(c) || !updateJSON(c, &struct{}{}) {
			return
		}
		if !manager.Status().Enabled {
			c.JSON(503, gin.H{"error": "在线更新尚未配置可信发布源", "code": "UPDATE_DISABLED"})
			return
		}
		if !installing.CompareAndSwap(false, true) {
			c.JSON(http.StatusConflict, gin.H{"error": "已有更新正在处理", "code": "UPDATE_BUSY"})
			return
		}
		// Closing the browser must not interrupt a staged update. Manager.Close
		// cancels this operation during normal service shutdown.
		go func() {
			defer installing.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			_, _ = manager.Install(ctx)
		}()
		respond(c, http.StatusAccepted, manager.Status())
	})
	g.PATCH("/settings", func(c *gin.Context) {
		if unavailable(c) {
			return
		}
		var body struct {
			AutoUpdate *bool `json:"auto_update"`
		}
		if !updateJSON(c, &body) {
			return
		}
		if body.AutoUpdate == nil {
			c.JSON(400, gin.H{"error": "缺少 auto_update 设置"})
			return
		}
		if err := manager.SetAuto(*body.AutoUpdate); err != nil {
			c.JSON(503, gin.H{"error": "保存更新设置失败，请稍后重试"})
			return
		}
		respond(c, 200, manager.Status())
	})
}

func updateJSON(c *gin.Context, value any) bool {
	body, _ := c.Get("cloud_body")
	data, _ := body.([]byte)
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		c.JSON(400, gin.H{"error": "请求内容必须是 JSON 对象"})
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		c.JSON(400, gin.H{"error": "更新参数无效"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		c.JSON(400, gin.H{"error": "更新参数无效"})
		return false
	}
	return true
}
