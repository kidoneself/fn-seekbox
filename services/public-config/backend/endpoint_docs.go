package main

import (
	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
	"strings"
)

type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

func enrichEndpoint(e *Endpoint) {
	e.Parameters = []Parameter{}
	if e.Method == "DELETE" || strings.HasSuffix(e.Path, "/revoke") || strings.HasSuffix(e.Path, "/rotate") || strings.HasSuffix(e.Path, "/logout") {
		e.Request = gin.H{}
	}
	if strings.HasSuffix(e.Path, "/client-settings") && e.Method == "PATCH" {
		e.Request = gin.H{"heartbeat_interval_seconds": 600, "event_batch_max_items": 100, "event_batch_max_bytes": 262144, "event_max_age_days": 7, "raw_retention_days": 90, "summary_retention_days": 365}
	}
	if strings.Contains(e.Path, "/api-keys") && oneOf(e.Method, "POST", "PUT") && !strings.HasSuffix(e.Path, "/rotate") {
		e.Request = gin.H{"name": "<调用方名称>", "enabled": true, "permissions": []string{}, "expires_at": nil}
	}

	for _, s := range strings.Split(e.Path, "/") {
		if strings.HasPrefix(s, ":") {
			e.Parameters = append(e.Parameters, Parameter{s[1:], "path", true, "string", "稳定标识；使用此接口的对应ID"})
		}
	}
	if e.Auth == "admin" {
		if strings.Contains(e.Path, "/analytics") {
			e.Group = "统计查询"
		} else if strings.Contains(e.Path, "/check-links") || strings.Contains(e.Path, "/sites") || strings.Contains(e.Path, "/config") {
			e.Group = "配置管理"
		} else {
			e.Group = "管理与文档"
		}
	}
	field := func(name, typ, desc string, required bool) {
		e.Parameters = append(e.Parameters, Parameter{name, "body", required, typ, desc})
	}
	query := func(name, typ, desc string, required bool) {
		e.Parameters = append(e.Parameters, Parameter{name, "query", required, typ, desc})
	}
	switch e.Path {
	case "/api/check-link":
		query("provider", "string", "baidu / quark / xunlei；只允许一个值", true)
		e.Response = gin.H{"code": 0, "message": "ok", "request_id": "<request_id>", "data": gin.H{"provider": "baidu", "source_id": "<配置的稳定ID>", "source_version": "<配置版本>", "share_url": "<管理员实际配置的分享URL>", "password": "<提取码或空字符串>", "file_path": "<配置的相对路径>", "max_file_size_bytes": 1048576, "expires_at": nil}}
		e.Errors = append(e.Errors, "SOURCE_NOT_CONFIGURED", "SOURCE_DISABLED", "SOURCE_EXPIRED")
	case "/api/search-sites":
		e.Response = gin.H{"code": 0, "message": "ok", "request_id": "<request_id>", "data": []any{gin.H{"id": "<真实插件稳定ID>", "name": "<配置名称>", "url": "<配置网址>", "enabled": true, "order": 1}}}
	case "/api/client/register":
		field("schema_version", "integer", "固定1", true)
		field("client_instance_id", "string", "持久随机匿名ID，1–64字母数字/下划线/短横线", true)
		field("registration_nonce", "string", "至少32随机字节base64url/hex；客户端安全持久化；相同ID+秘密10分钟内可重试", true)
		field("app_version", "string", "客户端版本，1–48字符", true)
		field("platform", "string", "固定fnos", true)
		field("arch", "string", "amd64 / arm64", true)
		e.Response = gin.H{"code": 0, "message": "ok", "request_id": "<request_id>", "data": gin.H{"installation_id": "<匿名实例ID>", "report_token": "<仅登记返回的凭证>", "token_expires_at": nil}}
		e.Errors = append(e.Errors, "REGISTRATION_CONFLICT", "REGISTRATION_RETRY_EXPIRED", "CREDENTIAL_REVOKED")
	case "/api/client/config":
		e.Description = "Bearer使用登记返回的report_token。配置版本变化后客户端更新上报间隔和批次限制。检查仍由客户端在北京时间00:00执行；服务端不操作网盘。"
	case "/api/client/heartbeat":
		for _, p := range []Parameter{{"schema_version", "body", true, "integer", "固定1"}, {"snapshot_seq", "body", true, "integer", "持久单调递增，至少1；旧序号不覆盖，同序号不同内容409"}, {"reported_at", "body", true, "string", "RFC3339发生时间；仅当前±5分钟心跳更新在线"}, {"app_version", "body", true, "string", "客户端版本"}, {"platform", "body", true, "string", "fnos"}, {"arch", "body", true, "string", "amd64/arm64"}, {"accounts_complete", "body", true, "boolean", "true移除缺失账号；false保留缺失账号"}, {"accounts", "body", true, "array", "最多30；account_ref/provider/auth_status/check_enabled及可选时间、最近run、cleanup_pending_count；禁止真实账号标识"}, {"subscriptions", "body", true, "object", "total、enabled非负且enabled<=total"}, {"active_jobs", "body", true, "integer", "当前任务数，0–10000"}} {
			e.Parameters = append(e.Parameters, p)
		}
		e.Errors = append(e.Errors, "SNAPSHOT_CONFLICT", "ACCOUNT_CONFLICT")
	case "/api/client/events/batch":
		field("schema_version", "integer", "固定1", true)
		field("batch_id", "string", "随机匿名批次ID，不作为业务计次键", true)
		field("events", "array", "1–100条，整个请求最多256KiB；事件公共字段event_id/type/occurred_at/app_version/data；单条data<=16KiB", true)
		e.Description = "四类account_check事件。started: run_id/account_ref/provider/trigger/scheduled_for/attempt；stage_finished: run_id/stage/status/duration_ms/error_code；finished: 完整身份、source_id/source_version/auth_status/result/failed_stage/error_code/duration_ms/cleanup_pending/deletion_mode及7阶段stages；cleanup_finished: run_id/result/duration_ms/error_code/deletion_mode。完整字段、枚举和实际闭环计次规则见随包 requirements-v1.1.md 第9节。去重按实例+event_id；同run终态换ID仍只计一次。最多补报7天，未来超过5分钟拒收；按条accepted/duplicate/rejected，应只重试未确认或retryable=true项目。"
		e.Response = gin.H{"code": 0, "message": "ok", "request_id": "<request_id>", "data": gin.H{"accepted_event_ids": []string{}, "duplicate_event_ids": []string{}, "rejected_events": []any{gin.H{"event_id": "<拒收ID>", "code": "<受控错误码>", "field": "<错误字段>", "retryable": false}}, "server_time": "<RFC3339>"}}
		e.Errors = append(e.Errors, "INVALID_BATCH", "EVENT_TOO_OLD", "CLOCK_SKEW", "EVENT_TYPE_NOT_SUPPORTED", "IDEMPOTENCY_CONFLICT", "RUN_IDENTITY_CONFLICT", "TERMINAL_CONFLICT")
	}
	if strings.Contains(e.Path, "/analytics/") {
		if strings.Contains(e.Path, ":run_id") {
			query("installation_id", "string", "run所属匿名实例；用于避免不同实例同run_id冲突", true)
		} else {
			query("timezone", "string", "只支持Asia/Shanghai", false)
			query("installation_id", "string", "按匿名实例筛选", false)
			query("app_version", "string", "运行按当前快照版本、run按执行版本、留存按首次登记版本", false)
			query("provider", "string", "baidu/quark/xunlei；运行留存不支持此筛选", false)
			if !strings.HasSuffix(e.Path, "/instances") && !strings.HasSuffix(e.Path, "/accounts") {
				query("from", "string", "RFC3339区间起点（含）；趋势<=366天，明细<=31天", false)
				query("to", "string", "RFC3339区间终点（不含）", false)
			}
			if strings.HasSuffix(e.Path, "/trends") {
				query("metric", "string", "successful_cycles/daily_successful_slots/running_instances/continuous_running_instances_7d/continuous_running_instances_30d/retention_d1/retention_d7", true)
				query("granularity", "string", "day/hour；运行留存只允许day", false)
				query("group_by", "string", "provider/source_id/app_version/result/error_code；运行留存只允许app_version，每日计划槽只允许provider", false)
			} else if !strings.HasSuffix(e.Path, "/overview") {
				query("limit", "integer", "1–100，默认20", false)
				query("cursor", "string", "上一页next_cursor；保持筛选不变；新记录以as_of限制，当前快照可更新", false)
			}
			if strings.HasSuffix(e.Path, "/checks") || strings.HasSuffix(e.Path, "/events") {
				for _, k := range []string{"result", "error_code", "trigger", "source_id"} {
					query(k, "string", "按检查原始结果、受控错误码、daily/catch_up/manual或测试源筛选", false)
				}
			}
		}
	}
	if e.Request != nil {
		if m, ok := e.Request.(gin.H); ok {
			for k := range m {
				exists := false
				for _, p := range e.Parameters {
					if p.Name == k && p.In == "body" {
						exists = true
					}
				}
				if !exists {
					typ := "string"
					switch m[k].(type) {
					case bool:
						typ = "boolean"
					case int, int64, float64:
						typ = "integer"
					case []string, []any:
						typ = "array"
					case gin.H:
						typ = "object"
					}
					field(k, typ, "参照JSON示例及协议；严格拒绝未知字段", true)
				}
			}
		}
	}
}
func (a *app) visibleEndpoints(c *gin.Context) []Endpoint {
	actor := c.GetString("actor")
	if !strings.HasPrefix(actor, "key:") {
		return a.registry
	}
	var key AdminKey
	a.store.db.View(func(tx *bolt.Tx) error { get(tx, "admin_keys", strings.TrimPrefix(actor, "key:"), &key); return nil })
	out := []Endpoint{}
	for _, e := range a.registry {
		if e.Auth == "public" || oneOf(e.ID, key.Permissions...) {
			out = append(out, e)
		}
	}
	return out
}
func enrichOpenAPI(e Endpoint, op gin.H) {
	params := []any{}
	for _, p := range e.Parameters {
		if p.In != "body" {
			params = append(params, gin.H{"name": p.Name, "in": p.In, "required": p.Required, "description": p.Description, "schema": gin.H{"type": p.Type}})
		}
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	switch e.Auth {
	case "admin":
		op["security"] = []any{gin.H{"bearerAuth": []string{}}, gin.H{"adminSession": []string{}}}
	case "admin_session":
		op["security"] = []any{gin.H{"adminSession": []string{}}}
	}
}
