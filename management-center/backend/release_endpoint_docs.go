package main

import (
	"github.com/gin-gonic/gin"
	"strings"
)

func enrichReleaseEndpoint(e *Endpoint) {
	if !strings.Contains(e.Path, "/releases") {
		return
	}
	e.Group = "版本发布与更新"
	e.Errors = append(e.Errors, "INVALID_MANIFEST", "ARTIFACT_MISMATCH", "INVALID_RELEASE_STATE", "IDEMPOTENCY_CONFLICT")
	switch e.Path {
	case "/api/client/releases/latest":
		e.Name = "查询可用更新"
		e.Description = "公开只读。只返回相同应用、渠道、架构中版本高于客户端的最高已发布版本；三段数字按数值比较。客户端仍须验证签名、min_bootstrap和data_schema兼容性。无更新data={available:false}。"
		for _, p := range []Parameter{{"app", "query", true, "string", "固定fn-seekbox"}, {"channel", "query", true, "string", "stable等发布渠道"}, {"arch", "query", true, "string", "amd64/arm64"}, {"version", "query", true, "string", "当前版本，三段数字，例如1.6.0"}} {
			e.Parameters = append(e.Parameters, p)
		}
		e.Response = gin.H{"code": 0, "message": "ok", "request_id": "<request_id>", "data": gin.H{"available": true, "release_id": "<release_id>", "manifest": gin.H{"key_id": "<受信任公钥ID>", "payload": "<原始JSON字节的base64>", "signature": "<Ed25519签名base64>"}, "download_url": "<同服务HTTPS更新包地址>"}}
	case "/api/client/releases/:id/artifact":
		e.Name = "下载已发布更新包"
		e.Description = "公开下载application/gzip二进制，支持Range；草稿与撤回版本404，无缓存。不能通过此接口读取任意路径。已开始的下载不被撤回操作中断，客户端安装前应再次确认版本仍发布。"
		e.Response = "HTTP 200 application/gzip；Range请求可返回206；响应不是JSON envelope。"
	case "/api/client/releases/report":
		e.Name = "上报更新结果"
		e.Description = "复用匿名实例report_token。实例身份只从Token读取；同实例+event_id完全相同重复返回duplicate=true，内容冲突409。时间允许过去90天至未来5分钟。只接收曾发布、架构匹配的版本。success表示新版运行成功，failed与rolled_back的当前版本采用from_version；统计结合更新结果与心跳时间，避免迟到上报覆盖新版本。"
		e.Request = gin.H{"release_id": "<release_id>", "event_id": "<持久随机ID>", "version": "1.6.1", "from_version": "1.6.0", "status": "success", "error_code": "", "occurred_at": "<RFC3339>"}
		for _, p := range []Parameter{{"release_id", "body", false, "string", "查询更新时返回的版本ID；多渠道同版本时必填"}, {"event_id", "body", true, "string", "同一次更新报告持久ID，重试不更改"}, {"version", "body", true, "string", "尝试安装的三段目标版本"}, {"from_version", "body", true, "string", "更新前运行版本"}, {"status", "body", true, "string", "success/failed/rolled_back"}, {"error_code", "body", false, "string", "大写字母数字下划线受控错误码；success为空"}, {"occurred_at", "body", true, "string", "RFC3339结果发生时间"}} {
			e.Parameters = append(e.Parameters, p)
		}
		e.Response = gin.H{"code": 0, "message": "ok", "request_id": "<request_id>", "data": gin.H{"event_id": "<event_id>", "accepted": true, "duplicate": false}}
	case adminPrefix + "/releases":
		if e.Method == "GET" {
			e.Name = "版本列表与更新统计"
			e.Description = "返回全部版本、每版去重后的成功/失败/回滚事件数、非撤销实例当前版本分布、最近100条报告及总报告数。版本分布是最近观察到的版本，不代表当前在线；统计只来源于已登记实例上报。返回受信任key_id及上传上限，不返回私钥。"
			e.Response = gin.H{"code": 0, "message": "ok", "request_id": "<request_id>", "data": gin.H{"items": []any{}, "version_distribution": gin.H{"1.6.0": 1}, "recent_reports": []any{}, "total_reports": 0, "signing_key_ids": []string{"<key_id>"}, "upload_limit_bytes": releaseMaxBytes}}
		} else {
			e.Name = "上传签名更新草稿"
			e.Description = "管理员会话与同源Origin校验。multipart/form-data，两个文件manifest与artifact；不可用API Key调用。校验Ed25519签名、包SHA256与大小；最多256MiB。仅创建不可下载的草稿，不会触发更新。同版本/渠道/架构不可覆盖。"
			e.Parameters = append(e.Parameters, Parameter{"manifest", "formData", true, "file", "含key_id、payload、signature的JSON，最多64KiB"}, Parameter{"artifact", "formData", true, "file", "对应tar.gz更新包，最多256MiB"})
			e.Errors = append(e.Errors, "SIGNING_KEYS_NOT_CONFIGURED", "RELEASE_EXISTS", "INVALID_UPLOAD", "PAYLOAD_TOO_LARGE")
		}
	case adminPrefix + "/releases/:id/publish":
		e.Name = "发布更新草稿"
		e.Request = gin.H{}
		e.Description = "仅管理员会话、同源Origin。重新验证签名和文件后从draft进入published，客户端可查询下载。重复发布已发布版本幂等；撤回版本不能重新发布。"
	case adminPrefix + "/releases/:id/withdraw":
		e.Name = "撤回已发布版本"
		e.Request = gin.H{}
		e.Description = "仅管理员会话、同源Origin。从published进入withdrawn，停止后续查询与下载；不卸载已安装客户端、不打断已开始下载。重复撤回幂等。"
	}
}
