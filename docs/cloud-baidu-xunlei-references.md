# 百度与迅雷转存接入依据

核对日期：2026-11-03。实现使用用户本人的网页登录凭据，不包含其他项目的账号、Cookie、Token、应用密钥或验证码签名材料。以下接口均真实发出请求；本地协议测试使用独立测试值，不能替代本人账号联调。

## 百度网盘

百度开放平台提供账号授权与文件管理能力，但不能据此推定普通 OAuth access_token 具有当前网页分享转存权限。因此这次使用用户从 `https://pan.baidu.com` 复制的本人完整 Cookie。Cookie 需要有效的 BDUSS 和网盘 STOKEN 等登录信息，不能只填写一个用户名或密码。

实现流程：

1. `GET /api/gettemplatevariable` 读取真实账号标识和 `bdstoken`，缺少任一项均不算连接成功。
2. `GET /api/list` 分页列出个人目录；`POST /api/create` 创建目标目录并确认返回的目录信息。
3. 有提取码时调用 `POST /share/verify`；将 `randsk` 解码为 `sekey`，以正确编码的 BDCLND Cookie 保留本次分享会话。
4. `GET /share/list` 读取根目录及各子目录，支持页码遍历，优先使用 API 返回的 `share_id`、`uk`，字段缺失时才兼容读取分享网页。分享文件 ID 保持十进制字符串，避免大整数精度丢失。
5. `POST /share/transfer` 按同目录批量转存文件（每批最多 100 项），传入 `sekey`；拒绝目标已有同名文件，远端使用不覆盖的 `ondup=newcopy`。首先检查 `info[].errno`，不能把 errno=0 的批次包裹当成全部成功。`extra.list` 明确给出对应源/目标 ID 时确认；否则等待目标目录出现基线中没有的新文件，核对名称、大小和可用的 MD5。仅收到任务号或无关文件数量增加不算成功。

依据：

- [百度网盘开放平台](https://yun.baidu.com/open/platform)：官方能力与授权入口。
- [BaiduPCS-Go 分享转存实现](https://github.com/qjfoidnh/BaiduPCS-Go/blob/master/baidupcs/transfer.go) 与 [命令编排实现](https://github.com/qjfoidnh/BaiduPCS-Go/blob/master/internal/pcscommand/transfer.go)：网页分享列表、提取码与转存请求。
- [urldb 百度接入源码](https://github.com/ctwj/urldb/blob/main/common/baidu_pan.go)：`gettemplatevariable`、分享 Cookie 与 `extra.list` 的成功确认格式。仅核对协议，未采用项目配置、凭据或代码中的日志方式。

Cookie 失效、短信/验证码校验、分享取消或容量限制都会使本次操作明确失败，不会使用固定结果冒充成功。

## 迅雷云盘

优先导入用户本人的 `web_session` JSON，结构与用户影视盘源码的会话契约一致：`access_token`、`refresh_token`、RFC3339 格式的 `expires_at`、`headers` 和可选 `captcha_meta`。`headers` 必须包含 `x-device-id`、`x-client-id`、`x-captcha-token`；允许同一会话自己的 `x-client-version`、`x-device-sign`、`x-protocol-version`、`x-sdk-version`，忽略其他任意请求头。`captcha_meta` 必须来自本人登录流程，本项目不会生成或借用别人的验证码签名。

后端兼容旧四字段 `access_token`、`captcha_token`、`device_id`、`client_id`。四字段模式没有刷新令牌，失效仍需重连。会话导入不等于本项目已实现扫码采集；不要把本人会话、Cookie 或请求头发到聊天、日志或公开文件。

实现流程：

1. `GET https://xluser-ssl.xunlei.com/v1/user/me` 确认用户，同时访问个人云盘目录，验证令牌组合能实际读取文件。
2. `GET https://api-pan.xunlei.com/drive/v1/files` 按 `next_page_token` 列目录；`POST /drive/v1/files` 创建文件夹。
3. `GET /drive/v1/share` 读取分享及 `pass_code_token`；子目录使用 **GET** `/drive/v1/share/detail`，递归处理分页。某些开源客户端存在 POST 旧实现，本次以官方公开网页脚本为准。
4. `POST /drive/v1/share/restore` 按同目录批量提交 `file_ids`（每批最多 100 项），带分享上下文、祖先目录及目标目录；兼容 `restore_task_id` 和 `task_id`，随后 `GET /drive/v1/tasks/{id}` 确认终态。分享目录同时兼容 `file_infos` 和 `files`。
5. 只有任务完成且没有 `partial_failed` 或任务错误才记成功。单独的 progress=100、任务已受理或部分成功均不算完成。

一手依据：

- [迅雷官方网页](https://pan.xunlei.com/)。
- 页面引用的 [utils-initial.f23af17a7de85831b2fa.js](https://static-pan.xunlei.com/_nuxt/dist/client/utils-initial.f23af17a7de85831b2fa.js)：`getShare`、`getDetail`、`postRestore`、`checkRestoreStatus`，以及任务 `partial_failed` 的判定。
- 页面引用的 [app.5b4b7a7a1d0c81d69108.js](https://static-pan.xunlei.com/_nuxt/dist/client/app.5b4b7a7a1d0c81d69108.js)：分享状态与任务终态处理。脚本文件名带版本摘要，未来可能变化。
- [OpenList 迅雷驱动](https://github.com/OpenListTeam/OpenList/blob/main/drivers/thunder/driver.go)：账号查询、个人文件列表和创建文件夹请求。未采用该驱动的客户端私钥、登录签名或第三方授权信息。

访问令牌过期或收到明确鉴权拒绝时，使用该会话自己的 refresh_token 与 client_id 调用 `POST /v1/auth/token`，不使用 client_secret。明确的 captcha_invalid 拒绝且会话含 captcha_meta.captcha_sign 时，向 `POST /v1/shield/captcha/init` 提交本人会话元数据以尝试续期。每种刷新最多一次，刷新结果必须先经 `CommitCredentials` 成功加密保存，再用于后续请求；保存失败即停止。网络写入失败不自动重试，避免重复转存。

没有验证码续期元数据或上游返回人工验证 URL 时停止并要求用户重新连接，不绕过验证，不能承诺永久免维护。

## 用户自有影视盘源码参考

按用户要求只读对照 `/home/kidoneself/coding-my/ikantv-next` 的源代码，未修改该工程，也未读取其 .env、数据库、运行数据或账号配置。

- `apps/api/internal/provider/baiduweb/adapter.go`、`transfer_response.go`：分享 API 元数据、sekey 编解码、批次内错误和转存可见性确认。
- `apps/api/internal/provider/xunleiofficial/adapter.go`：restore_task_id、file_infos、个人目录以及用户 refresh_token 续期协议。
- `apps/api/internal/provider/xunleiofficial/web_session.go`、`web_captcha.go`：导入本人网页会话、白名单请求头、验证令牌条件刷新与完整会话持久化。
- `apps/api/internal/drive/xunlei_oauth.go`：另一路官方 OAuth 需要本项目自己的开放平台应用资料；本次未复制该项目应用配置、授权服务秘密或品牌流程。

## 边界与验证

- 每次响应最多 8 MiB，每目录最多 100 页，每份分享最多 10000 项，最多 32 层。超过上限返回明确错误，不返回不完整分享当作全量结果。
- 仅接受对应官方分享域名；请求固定发往官方 API 主机。认证请求不跟随跨主机重定向。错误不输出上游完整 URL、请求头或响应正文。
- 已存在同名目标文件时拒绝转存，不把它视为本次成功；应用层还需按已确认的源文件 ID/版本去重。若网络在写入后中断，用户需要检查网盘中的实际结果再处理冲突。
- `baidu_test.go`、`xunlei_test.go` 覆盖递归和分页、超出浮点安全整数的 ID、提取码会话、单文件转存确认、已受理未完成、部分失败、同名拒绝、凭据过期、分页循环、重定向泄漏防护，以及令牌轮换先保存后使用、保存失败停止、人工验证与写入失败不盲目重试。
- 尚未提供本人网盘凭据，未执行真实账号转存联调。上线前应使用本人小型测试分享分别验证个人目录、嵌套目录转存、新增一集后的追更，以及令牌失效后的错误展示。

## 1.5.5 批量转存

再次对照影视盘 `baiduweb/adapter.go:SaveShared` 的多 ID `fsidlist`、`xunleiofficial/adapter.go:SaveShared` 的多 ID `file_ids`。本项目按相对父目录分组，每批最多 100 个文件（应用设定，不宣称是上游最大值），保留原有路径、逐文件去重和不覆盖规则。百度登录会话仅在同一账号的单次任务内复用，每批读取目标目录一次；仍完整读取源分享，以维护追更快照和精确文件总数。

先将整批标记为 pending，再提交一次远端写入；已确认的文件与任务计数在同一个数据库事务中持久化。提交前冲突可以重试，提交后网络中断或部分未确认则保留对应 pending，防止重启或追更重复写入。百度优先核对每个源 ID 的新目标 ID，缺少明细时逐项核对新出现文件的名称、大小及可用 MD5，不采用影视盘仅依赖目标文件数量增长的确认方法。批次返回错误不能当成全成功。
