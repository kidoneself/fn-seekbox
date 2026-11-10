> 在线更新开发副本：本地启动配置与生产隔离。版本发布见 [ONLINE-UPDATES.md](./ONLINE-UPDATES.md)；2026-11-06 已部署扫码网关与“扫码中心”页面，当前接入见下文。历史段落中的停用测试源等仅代表当时验收状态。

# 小白搜盘管理中心 1.1

基于 Go / Gin / bbolt、Vue 3 / Vite / Tailwind 的独立服务。已部署到国内机，提供公共配置、匿名实例上报、检查次数与运行留存、响应式管理后台和接口中心。管理服务通过私有扫码容器处理短期扫码授权，不执行客户端每日转存/删除任务，不统计金额。

## 2026-11-06：扫码中心

“扫码中心”已加入主导航，代理独立项目 `/home/kidoneself/coding-my/drive-scan-center` 的百度、夸克、UC、迅雷扫码能力。生产公网与原服务共用域名，内部工作服务不暴露端口；浏览器仅查看运行统计，不直接取得网盘凭据。

- NAS 路由：`POST /api/client/scan/sessions`、`GET/DELETE /api/client/scan/sessions/:id`、`POST /api/client/scan/sessions/:id/claim|ack`，逐次验证原实例 `report_token`，拒绝浏览器 Origin。普通 API Key、管理 Cookie 不能替代实例鉴权。
- 管理路由：`GET /seekbox-config/api/scan/status`，纳入原有接口注册、接口中心与动态 API Key 授权。展示并发/队列、包含浏览器的容器内存、四网盘进程期计数；不返回账号、Cookie 或二维码。
- 私有地址与密钥从 `SEEKBOX_SCAN_INTERNAL_URL` 和 `SEEKBOX_SCAN_SECRET_FILE` 读取，配置见 `compose.production.example.yaml`。缺少配置时返回 503，不回退为假成功。
- 生产迅雷共享 2 个浏览器，每个 4 个隔离登录环境，共 8 路；其他网盘各 2 路、总共 14 路；队列最多 64、单实例最多执行 1 个；扫码容器限制 2 GiB / 1.5 CPU。
- 公网已验证无权限 403 → 后台授予 GET 权限 200 → 撤销 403，全程不改代码/重启；方法隔离、轮换、过期、停用及无效 Key 不回退 Cookie 同样通过。
- 本次管理中心验收未创建模拟安装或账号。真实四网盘二维码获取已通过；尚未人工扫码确认并完整验证 NAS 保存与后续转存。

独立服务的 README、API、部署和脱敏验收记录位于 `/home/kidoneself/coding-my/drive-scan-center/`。搜盘副本 1.6.3 仅将迅雷接入远端；现有 NAS 已于 2026-11-07 通过在线更新升级到 1.6.3，升级成功上报与远端二维码获取/取消均已核对。

## 2026-11-06：首页简化

日常导航仅保留「使用数据、测试文件、搜索站点」。其他操作收在默认折叠的「更多设置」。首页展示累计使用人数（按安装估算）、当前在线人数、已登录网盘人数、已登录网盘账号、所选日期执行检查次数、完整成功次数；可选今天、近7天、本月。登录人数按安装去重，多个有效账号仍算1人；账号须最近26小时确认有效且未移除。

三个网盘分别展示有效登录账号、检查次数和完整成功次数。每日成功次数可以下载CSV，供管理者按自己的规则估算收益，页面不计算金额。未有上报时显示明确提示：0不代表没人安装。

overview 新增三个兼容字段：`logged_in_instances`（有效登录账号所属安装去重数）、`valid_accounts_by_provider`（三种网盘的有效账号数）、`executed_runs_by_provider`（所选区间自动检查记录数）。原有字段、接口路径与权限不变。日期只筛选检查次数，人数和登录状态仍为当前快照。检查次数包括失败、跳过与未完成，成功次数仍要求转存和删除都确认；手动测试不计入首页自动检查统计。

新增验证覆盖多人、多账号、失效、过期和已移除账号，以及自动成功/失败与手动检查隔离。

## 访问与鉴权

- 后台：https://hermes.smartloan.cn/seekbox-config/
- API 基址：https://hermes.smartloan.cn
- 后台使用管理员密码登录，签发 Secure / HttpOnly / SameSite=Strict 会话 Cookie，12 小时有效。写入需同源 Origin 与 application/json。
- 管理员初始密码单独交付，不在本文、源码或镜像中保存。

| 方法与路径 | 鉴权 | 用途 |
|---|---|---|
| GET `/api/check-link?provider=baidu` | 无 | 最新测试源；provider=baidu/quark/xunlei，必填且只出现一次 |
| GET `/api/search-sites` | 无 | 全部实际站点配置，包括停用项；按 order、id 排序 |
| POST `/api/client/register` | 无，限流 | 随机匿名实例登记，获得独立上报凭证 |
| GET `/api/client/config` | report_token | 协议版本、上报间隔、批次限制和检查调度提示 |
| POST `/api/client/heartbeat` | report_token | 单调快照序号、账号状态、运行心跳 |
| POST `/api/client/events/batch` | report_token | 四类 account_check 事件的幂等上报 |

上报请求头：`Authorization: Bearer <REPORT_TOKEN>`。后台开放调用使用 `Authorization: Bearer <API_KEY>`，按具体 HTTP 方法和路由逐项授权。两种凭证互不通用，明确提供无效或无权限凭证时不会回退到 Cookie 身份。

调用方管理位于后台「接口中心」。支持新建、编辑权限、停用、有效期和轮换；明文仅创建或轮换时返回，数据库保存摘要。分组勾选只授予当前已注册操作，未来新增接口不会自动授权。Key 管理只允许后台管理员会话。

登记请求字段：`schema_version=1`、持久随机 `client_instance_id`、至少 32 随机字节的 base64url/hex `registration_nonce`、`app_version`、`platform=fnos`、`arch=amd64|arm64`。同一 ID 与秘密在 10 分钟内重试返回相同凭证；服务端的短期重试缓存加密存储。不同秘密冲突；窗口结束后不公开补发旧凭证。凭证不自动到期，可在「匿名实例」撤销。凭证丢失需用户显式重置客户端匿名身份后重新登记，会形成新实例，不把它宣称为新用户。

完整请求字段、事件枚举与示例参见 [需求协议 1.1](requirements-v1.1.md)。字段保持该协议，后台路径映射见下文。真实公网响应保存于 [live-verification.json](docs/live-verification.json)；其中不含密码、Cookie、API Key 或 report_token。

## 当前实际配置与范围

- 43 个真实搜索来源已导入，与 `search-sites-source.json` 中的 ID、名称、地址、启停、顺序一致。43 个 ID 均能对应原项目 `backend/pansou/plugin/<id>`。
- 三种测试源按用户要求保留明显的 `EXAMPLE_NOT_REAL` 占位，全部停用。公网返回 HTTP 503 / `SOURCE_DISABLED`，不会返回假成功。必须在后台填写真实分享、提取码和精确相对路径后启用。服务端不自动验证远端文件大小和可转存性；这些需真实网盘检查确认。
- 首次上线验收（2026-11-05）未植入模拟业务记录，当时统计为0。2026-11-06简化版公网验收时已收到1个安装上报、1个安装有有效网盘登录、3个有效账号；后续数字随客户端上报变化。
- 搜索、普通用户转存、追更细分统计属于第二阶段，返回 `METRIC_NOT_SUPPORTED`，不会用固定 0 冒充已统计。
- 原有 `/api/check/links` 是网盘链接检查能力，与「分发功能检查测试源」含义不同，因此新增 C01。已有搜索引擎位于 fnOS 客户端内，支持 GET/POST `/api/search`，参数 `kw`、`plugins` 等；GET 的插件列表逗号分隔，POST 使用数组。本服务不公开或代理该搜索引擎，不把站点首页 URL 当搜索 API，也未增加爬虫。
- 本次统一接口注册、Key 权限和接口中心覆盖此独立服务的管理操作。Hermes、授权中心和 fnOS 原业务接口不属于本次开放范围。

## 后台路径映射

使用 `/seekbox-config/api`，避免占用现有 Hermes 的 `/api/admin`。客户端六个路径与需求一致。

| 协议能力 | 实际路径 / 方法 |
|---|---|
| 读取测试源、站点全部配置 | GET `/seekbox-config/api/config` |
| 修改测试源 | PUT `/seekbox-config/api/check-links/:provider` |
| 新增站点 | POST `/seekbox-config/api/sites` |
| 修改站点 | PUT `/seekbox-config/api/sites/:id`（完整记录，非 PATCH） |
| 移除站点 | DELETE `/seekbox-config/api/sites/:id`，请求体 `{}` |
| 上报设置 | GET/PATCH `/seekbox-config/api/client-settings` |
| 统计与明细 | GET `/seekbox-config/api/analytics/overview|trends|instances|accounts|checks|events|slots` |
| 检查详情 | GET `/seekbox-config/api/analytics/checks/:run_id?installation_id=...` |
| 撤销实例 | POST `/seekbox-config/api/instances/:id/revoke`，请求体 `{}` |
| 接口注册清单 | GET `/seekbox-config/api/endpoints` |
| OpenAPI | GET `/seekbox-config/api/openapi`（统一响应中的 data 是规范；UI导出为原始规范） |
| 调用日志 | GET `/seekbox-config/api/audit` |
| 调用方 | GET/POST `/seekbox-config/api/api-keys`；PUT `/:id`；POST `/:id/rotate` |
| 原公共接口请求次数 | GET `/seekbox-config/api/stats?days=7`，仅请求计数，不等于业务成功次数 |

测试源 `source_id` 初始化后保持稳定，不因修改链接改变；`source_version` 每次成功保存递增。站点 ID 创建后不可修改，删除后保留墓碑，不能重新用于其他站点。提取码为空时返回空字符串。所有响应均禁用缓存，读取直接来自 bbolt 事务；当前采用单实例部署。

## 统计与时间口径

- 唯一闭环台账键：实例 + run_id。转存、保存确认、删除、清理确认都成功且没有待清理状态，计 1 次；同 run 换 event_id 或批次重报不再增加。
- 原始 `result=failed` 可经后续清理变为 `cycle_completed=true`，原始失败仍保留。清理先到则暂存；没有转存确认不计次。旧失败不会撤销已确认闭环。
- `cycle_completed_at` 优先使用实际删除确认阶段时间，缺失时回退完整终态时间。晚到的精确时间可修正跨日归属；累计次数不变。重复清理不会把日期往后移动。
- 自动统计默认 daily + catch_up，manual 单列。每日计划槽按实例 + 账号 + 北京时间 scheduled_for 去重，实际成功 run 数与成功槽数分开。
- 每日计划按服务端在 00:00 已知的账号启用历史估算。日中新账号从次日进入分母；未上报与失败分开。页面同时显示近期上报实例的预计槽数，避免把历史离线安装误认为当前在线用户。
- 当前在线：最近有效当前心跳不超过 30 分钟。偏离服务端时间超过 5 分钟的高序号心跳可更新快照但不刷新在线或当天运行；低序号不覆盖。同序号不同内容返回冲突。
- 每日运行：合法检查事件或有效当前心跳；失败检查也算运行。近 7/30 天含今天；连续 7/30 天截至昨天。D+1 / D+7 为首次登记 cohort 的对应日运行比例，不等于连续运行。观察日未结束 `matured=false` 且 `value=null`；分母为零也为 null。
- 日期采用 Asia/Shanghai，`from/to` 是 RFC3339 左闭右开区间。趋势最多 366 天，明细最多 31 天；默认明细近 7 天，overview 默认今天。实例/账号接口展示当前快照，不接受 from/to。
- 成功次数趋势支持 day/hour 及 provider/source_id/app_version/result/error_code 分组；每日成功槽仅支持 provider 分组，避免一个槽多次尝试跨版本/结果重复分组。运行/留存仅支持 day、实例和版本筛选/分组；留存版本为首次登记版本，运行版本为当前快照版本。不支持的组合明确 400。
- 分页默认 20，最大 100；游标签名且绑定筛选条件。as_of 限制新记录接收时间，当前记录后续更新仍可反映在下一页；这不是历史数据库快照。
- 原始事件、阶段明细、调用审计默认保留 90 天；每日运行标记与计划槽默认 365 天。成功台账长期保留用于累计、去重与重算，清理旧事件不会清零累计。保留期每日清理，后台可调整；历史累计不代表全部实际安装人数。

## 错误与限制

成功：HTTP 200（新建站点为 201）、`code: 0`。错误统一 `{code: "ERROR_CODE", message: "明确原因", request_id: "...", data: null}`。

| HTTP | 主要错误码 |
|---|---|
| 400 | INVALID_FIELD、INVALID_QUERY、INVALID_TIMEZONE、INVALID_RANGE、INVALID_CURSOR、INVALID_BATCH、DUPLICATE_EVENT_ID、METRIC_NOT_SUPPORTED、UNSUPPORTED_COMBINATION |
| 401 | UNAUTHORIZED：缺少/无效/停用/过期/撤销凭证 |
| 403 | FORBIDDEN、ADMIN_SESSION_REQUIRED：操作未授权或需管理员会话 |
| 404 | SOURCE_NOT_CONFIGURED、NOT_FOUND |
| 405 | METHOD_NOT_ALLOWED |
| 409 | CONFLICT、SOURCE_ID_IMMUTABLE、REGISTRATION_CONFLICT、REGISTRATION_RETRY_EXPIRED、CREDENTIAL_REVOKED、SNAPSHOT_CONFLICT、ACCOUNT_CONFLICT |
| 413 | PAYLOAD_TOO_LARGE |
| 415 | UNSUPPORTED_MEDIA_TYPE |
| 429 | RATE_LIMITED，带 Retry-After |
| 503 | SOURCE_DISABLED、SOURCE_EXPIRED、SERVICE_UNAVAILABLE |

事件批次部分成功仍 HTTP 200，逐条在 `accepted_event_ids`、`duplicate_event_ids`、`rejected_events` 中确认。拒收条目带 `code/field/retryable`，包括 EVENT_TOO_OLD、CLOCK_SKEW、EVENT_TYPE_NOT_SUPPORTED、INCONSISTENT_RESULT、IDEMPOTENCY_CONFLICT、RUN_IDENTITY_CONFLICT、TERMINAL_CONFLICT。禁止把整批 HTTP 200 当成全部成功，也不要重试 `retryable=false`。

schema_version=1；默认每批 100 条 / 256 KiB，单条 data 16 KiB，心跳最多 30 账号，补报 7 天，未来容差 5 分钟。匿名登记 30 次/分钟/来源 IP，公共配置 600 次/分钟/来源 IP，上报 120 次/分钟/实例，管理 Key 600 次/分钟/Key。登录限制 10 次/15 分钟/来源 IP。限流窗口仅在内存中，重启会重置。仅信任 Caddy 当前地址 `172.18.0.2/32` 转发的来源 IP，其他来源的 X-Forwarded-For 不用于限流。

## 实测

2026-11-05：10 组 Go 集成测试通过，`go test -race` 与 `go vet` 通过，Vue 生产构建通过。

覆盖三种网盘配置读取、即时修改、非法/重复参数、缺少/停用/过期配置、服务异常；站点增改删与 ID 墓碑；Key 未授权→授权→撤销、HTTP 方法隔离、停用、到期、轮换旧 Key 失效、无效 Key 不回退 Cookie；登记重试/冲突/隔离/撤销；心跳序号、完整/部分快照及离线；重复批次与业务终态、混合拒收、敏感字段拒收、空值、补报窗口、先清理后终态、跨日校正、失败保留、重启去重、过期清理累计不变、连续运行/留存与分页。

网盘成功响应测试在隔离临时数据库中使用明确的测试夹具，验证配置读取与协议逻辑，不代表验证过远端真实分享或实际转存删除。真实网盘闭环需后续填入有效分享并由客户端执行。

浏览器实测：1440×1000 桌面、390×844 手机，11 个模块、登录/退出、设置保存后刷新、真实接口调试、Key 创建、无本地存储密钥、无页面脚本错误、无水平溢出。另在公网真实 HTTPS 登录复核桌面/手机与三个停用测试源。

公网实测：32 个请求验证配置、缺参/非法参数、上报鉴权、43 个来源完全对应、后台改排序立即生效并恢复、API Key 403→授权200→撤销403→停用401；无效 Key 携带有效会话仍401。未添加模拟实例；验收调用方 Key 已停用。16 个原有容器启动时间未变，原 Hermes 根路径仍302、healthz仍ok。

## 开发、部署与维护

源码独立位于 `services/public-config`，没有修改 fnOS 原项目业务文件。构建需要 Go 和 Node，依赖锁文件已保留：

```sh
cd backend
go test -race ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o ../build/server .
cd ../frontend
npm ci
npm run build
```

本机临时磁盘曾发生 fdatasync 阻塞，验收通过 `SEEKBOX_TEST_TMPDIR=/dev/shm go test -race ./...` 使用临时内存文件系统，未关闭 bbolt 同步写入；生产运行在国内机实际磁盘，公网读写已通过。

部署目录 `/home/ubuntu/hubstack/seekbox-public-config`，容器 `seekbox-public-config`；数据库 `data/config.db`，密码摘要 `secrets/admin-password-hash`，容器 UID/GID=10001。镜像无内置密码或外部端口映射，通过 `hubstack_frontend` 网络由 Caddy 转发。

Caddy 仅添加六个客户端路径和 `/seekbox-config`、`/seekbox-config/*`。原文件备份在部署目录 `Caddyfile.before`。网关单文件 bind mount 需保持 inode：修改内容、validate、reload，不能用替换 inode 的重命名方式。后续若 Caddy IP 变化，要更新 compose 的 `-trusted-proxy` 并重建本服务。

部署：构建二进制和前端后 `docker compose build && docker compose up -d`。备份数据库时停止本服务后复制数据库，再启动本服务，避免直接复制写入中的 bbolt 文件；长期保留数据库及密码摘要文件。密码变更通过服务二进制 `-hash-password` 从标准输入读取新密码生成 bcrypt，再更新外部摘要文件并重启本服务，旧后台会话失效。

本版本为单管理员、单实例小型服务。匿名上报只是客户端报告，不能作为防伪人数或计费凭证。原始历史已清理的阶段不再恢复，界面明确 `details_expired`；未接入客户端的功能、真实网盘分享和第二阶段统计不计为已完成。

## 公网真实返回摘录

三种测试源当前均为停用状态，百度的实际返回：

```json
{
  "code": "SOURCE_DISABLED",
  "request_id": "req_64a3XcJUp5mCQsHIUI6ipWfsqjEwpeH4u4M82dbZ-kI",
  "message": "该网盘测试源已停用",
  "data": null
}
```

搜索来源实际返回中的首条（完整43条见验收JSON）：

```json
{
  "id": "ikantv",
  "name": "ikantv",
  "url": "https://api.naspt.vip/",
  "enabled": true,
  "order": 1
}
```
