# 小白搜盘使用留存与每日检查次数——接口需求文档

- 文档版本：1.1（按转存与删除成功次数统计）
- 日期：2026-11-05
- 对接客户端：飞牛 fnOS「小白搜盘」
- 服务端：用户自行开发的配置与统计服务
- 文档性质：待实现接口约定，不代表相关接口已经存在

## 1. 范围与职责

本需求用于开发服务端接口，核心目标为：了解有多少匿名实例在使用、是否持续运行，以及每日 00:00 的网盘检查完成了多少次「转存成功 + 删除成功」。

**最终计数口径：同一个网盘账号的一次实际检查，确认转存成功且确认删除成功，计 1 次。百度、夸克、迅雷分别统计，可以求和。只完成其中一步、结果未确认、重复上报都不能增加次数。金额由项目维护者自行计算，服务端不需要收益回传、计费、单价、金额、对账或结算接口。**

### 1.1 第一阶段必须交付

1. 三种网盘的最新测试分享获取、搜索来源配置。
2. 匿名实例登记、心跳、账号状态快照。
3. 每日检查开始、阶段、完成与补充清理结果上报。
4. 按网盘、日期、版本、匿名实例统计完整成功次数。
5. 当前在线、每日运行、连续运行、次日/7 日运行留存。
6. 每日应检查、执行、失败、未上报、转存成功但删除未完成明细。
7. 后台管理配置、查询明细、异常排查与必要鉴权。

### 1.2 第二阶段扩展，不阻塞首版交付

搜索量、来源搜索耗时、人工转存、追更任务的细分分析保留接口协议，后续按需接入。本文对应事件和指标标为扩展项；首版未接入的指标返回不支持或暂无数据，不能用 0 冒充真实统计。

### 1.3 次数示例

| 一次实际检查的结果 | 完整成功次数 |
|---|---:|
| 百度转存成功、删除成功，均已确认 | 百度 +1 |
| 夸克转存成功、删除失败或未确认 | +0，列入待清理 |
| 迅雷转存失败，只把空测试目录删除成功 | +0 |
| 百度、夸克、迅雷各完成一次 | 合计 +3，每种网盘各 +1 |
| 同一 run_id 的完成结果重复上报 5 次 | 总共仍是 1 |
| 一次检查涉及多个文件，但只完成一次完整流程 | 仍是 1，不按文件数计费或计次 |
| 原检查转存已确认，稍后关联原 run_id 补充删除且确认成功 | 原检查由 0 变为 1，不新增第二次 |

服务端不接收网盘 Cookie、访问令牌或刷新令牌，不代替客户端登录、转存、删除。定时检查由 NAS 后台服务执行，服务端负责提供测试源、接收执行结果及分析。

客户端业务约定：每天北京时间 00:00 对已启用检查的账号执行「获取测试源 → 验证账号 → 转存指定小文件 → 确认保存 → 删除测试内容 → 确认清理」。支持手动检查。NAS 停机错过计划时，恢复后补最近一次应执行检查，不连续补做所有历史日期。

> 本文接口路径为推荐路径。若服务端已有同类接口，优先复用，交付实际路径与字段映射。

## 2. 统一约定

### 2.1 基础格式

- HTTPS + JSON，UTF-8。
- 时间字段使用 RFC 3339，带时区，例如 `2026-11-05T00:00:35+08:00`。
- 服务端可内部按 UTC 存储；每日统计与每日检查日期统一按 `Asia/Shanghai` 计算。
- 数量使用整数；耗时字段统一为毫秒，后缀 `_ms`。
- ID 使用不透明字符串；不要依赖连续自增数字作为客户端身份凭证。
- 网盘枚举：`baidu`、`quark`、`xunlei`。
- 搜索来源 ID 沿用现有插件 ID，例如 `xiaokupan`、`hunhepan`。
- 请求版本通过 `schema_version` 表示，首版为 `1`。

成功：

```json
{
  "code": 0,
  "message": "ok",
  "request_id": "服务端请求标识",
  "data": {}
}
```

失败：

```json
{
  "code": "SOURCE_NOT_CONFIGURED",
  "message": "该网盘尚未配置测试分享",
  "request_id": "服务端请求标识",
  "data": null
}
```

- HTTP 400：格式或参数错误。
- HTTP 401：凭证无效、过期或撤销。
- HTTP 403：没有接口权限。
- HTTP 404：记录或配置不存在。
- HTTP 409：状态冲突或同一幂等标识携带不同内容。
- HTTP 413：请求体超限。
- HTTP 429：限流，返回 `Retry-After`。
- HTTP 500/503：内部错误或暂不可用。
- 不能在真实失败时返回 `code: 0`。

### 2.2 身份与权限

公共测试源与搜索来源接口可匿名只读访问并限流。客户端登记后获得独立上报凭证；心跳和事件上报必须鉴权。

```http
Authorization: Bearer <report_token>
```

- 服务端从凭证确定 `installation_id`，不相信请求体任意指定的实例身份。
- 上报凭证只允许对应实例读客户端配置、发送心跳和事件，不允许读取全站统计、修改配置或管理凭证。
- 凭证安全随机生成，仅保存安全摘要；创建/轮换时返回明文，不写入日志。
- 不在开源仓库、前端静态文件或安装包中预置全站共用的管理员 Key。
- 复用目标项目已有统一 API Key 鉴权与权限体系。明确使用无效 Key 的请求不能回退到浏览器登录身份放行。
- 后台接口需要管理员登录或被明确授予对应权限的 API Key；不能把上报凭证当管理员凭证。
- 首次登记接口做限流。匿名登记数量只能代表匿名实例数量，不能宣称为可信人数或独立硬件数量。

### 2.3 隐私与收集范围

禁止上报：网盘 Cookie/Token、NAS 登录凭据、账号昵称、手机号、网盘用户 ID、搜索关键词、分享 URL/提取码、资源标题、文件名/路径、NAS 路径、硬件序列号/MAC。

允许上报：随机生成并持久化的匿名实例 ID、实例内随机账号引用、应用版本、网盘类型、来源 ID、数量、状态、阶段、耗时、受控错误码。

- `account_ref` 只是实例内部随机账号标识，不得用手机号、Cookie 或网盘用户 ID 生成。
- 错误上报使用固定错误码；不上传可能包含凭据和链接的原始异常文本。
- 统计开关与每日功能检查开关相互独立。客户端关闭统计后可以继续本地功能检查，服务端看板仅覆盖同意上报的实例，不能声称为全部实际安装量。客户端应说明自动转存检查及推广用途，允许用户关闭。
- 后台将上报数据标为客户端报告；匿名上报不能保证防篡改，不能直接用作计费依据。

## 3. 客户端接口清单

| 编号 | 方法与路径 | 鉴权 | 用途 |
|---|---|---|---|
| C01 | `GET /api/check-link` | 公共只读 | 获取对应网盘的最新测试分享 |
| C02 | `GET /api/search-sites` | 公共只读 | 获取搜索来源配置 |
| C03 | `POST /api/client/register` | 首次登记公开并限流 | 登记匿名实例、签发专属凭证 |
| C04 | `GET /api/client/config` | 上报凭证 | 获取上报策略和协议配置 |
| C05 | `POST /api/client/heartbeat` | 上报凭证 | 覆盖更新实例与账号状态快照 |
| C06 | `POST /api/client/events/batch` | 上报凭证 | 幂等接收业务与检查事件 |

## 4. C01 获取测试分享

```http
GET /api/check-link?provider=baidu
```

参数：`provider` 必填，限定三种网盘。

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "provider": "baidu",
    "source_id": "probe_baidu_01",
    "source_version": "3",
    "share_url": "https://pan.baidu.com/s/示例分享标识",
    "password": "1234",
    "file_path": "功能检查.txt",
    "max_file_size_bytes": 1048576,
    "expires_at": null
  }
}
```

字段要求：

| 字段 | 必填 | 说明 |
|---|---|---|
| provider | 是 | 与请求网盘一致 |
| source_id | 是 | 稳定测试源标识，用于统计，不含分享密码 |
| source_version | 是 | 配置变动递增/变化，用来定位哪版测试源异常 |
| share_url | 是 | 对应网盘的真实公开分享链接 |
| password | 是 | 无提取码传空字符串 |
| file_path | 是 | 分享内指定文件相对路径，不能是目录或通配符 |
| max_file_size_bytes | 是 | 首版上限 1 MiB，客户端也独立限制，服务端不能任意放大 |
| expires_at | 是 | 可空；不为空时为链接已知有效期 |

行为：

- 后台维护真实小文件分享，修改后下次获取立即生效。
- 每次功能检查重新获取，响应使用 `Cache-Control: no-store`。
- 未配置、停用、已到期分别返回明确错误，不能伪造可用链接。
- 未实际探测链接时，不宣称链接有效；实际可用性由闭环检查反馈。
- `file_path` 禁止绝对路径、`..` 跳转、URL、目录通配。
- 只允许百度、夸克、迅雷已支持的 HTTPS 分享域名与路径格式，不返回本地/内网地址或任意下载地址。
- 客户端必须继续执行网盘域名和重定向校验。
- 测试源获取失败归类为测试源故障，不归类为网盘登录失效。

## 5. C02 获取搜索来源配置

```http
GET /api/search-sites
```

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {
      "id": "xiaokupan",
      "name": "xiaokupan",
      "url": "https://xiaokupan.com",
      "enabled": true,
      "order": 1
    }
  ]
}
```

- `id` 稳定唯一，与客户端现有插件 ID 对应；改名和改网址不更换 ID。
- `name` 为展示名称，允许后台修改。
- `url` 是该来源当前配置地址，可能为主页或 API 基址，不保证都是可浏览首页。
- `enabled` 是配置启停，不是实时健康状态。
- `order` 为整数，升序；相同值按 `id` 稳定排序。
- 返回全部已配置记录，包括停用记录，供客户端同步状态。
- 可使用 ETag/304；管理员修改后 ETag 必须变化。
- 客户端本地用户选择仍需保留，不能每次同步都覆盖为全选。
- 新增未知插件 ID 只能成为配置记录，不能宣称客户端自动具备对应搜索解析能力。
- 首版配置同步不承诺任意 URL 替换现有插件协议；具体搜索适配仍由客户端实现。

初始真实数据：同仓库 `docs/data/search-sites-1.5.13.json`，共 43 个来源。`name` 保留当前界面的插件标识；此数据不是实时健康检查结果。`ouge` 与 `wanou` 当前地址相同，仍保留不同插件 ID，不按 URL 自动合并。

## 6. C03 匿名实例登记

```http
POST /api/client/register
Content-Type: application/json
```

```json
{
  "schema_version": 1,
  "client_instance_id": "客户端首次启动生成并持久保存的随机UUID",
  "registration_nonce": "客户端生成的至少256位随机登记秘密",
  "app_version": "1.5.13",
  "platform": "fnos",
  "arch": "amd64"
}
```

成功：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "installation_id": "inst_匿名标识",
    "report_token": "仅本次签发的随机上报凭证",
    "token_expires_at": null
  }
}
```

要求：

- `client_instance_id` 不是身份秘密；仅知道这个 ID 不能领取已有实例凭证。
- 服务端保存登记秘密摘要，避免首次响应丢失导致反复创建实例。相同实例 ID + 相同登记秘密允许短期重试；不同登记秘密返回冲突，不接管旧实例。
- 如需重试返回同一个凭证，可短期加密缓存签发响应，建议 10 分钟，过期删除。不得长期明文保存凭证。
- 短期重试窗口过后，未持有凭证的恢复需走单独、明确的恢复/重新登记流程，不能公开接口任意重签旧实例凭证。
- 首版可使用未设置自动到期的可撤销实例凭证；以后若设置有效期，需同时交付轮换协议，不能让客户端凭证静默到期。
- 正常升级、服务重启保留实例 ID 和凭证；全新安装或清空数据可能产生新实例，统计不强行当同一硬件合并。
- 本接口不能用请求量直接累加安装数；成功创建的不同实例才计数。

## 7. C04 获取客户端配置

```http
GET /api/client/config
Authorization: Bearer <report_token>
```

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "config_version": "1",
    "server_time": "2026-11-05T10:00:00+08:00",
    "accepted_schema_versions": [1],
    "heartbeat_interval_seconds": 600,
    "event_batch_max_items": 100,
    "event_batch_max_bytes": 262144,
    "event_max_age_days": 7,
    "check_schedule": {
      "timezone": "Asia/Shanghai",
      "local_time": "00:00",
      "catch_up": "latest_due_only"
    }
  }
}
```

配置仅描述本协议的上报参数与每日检查时间约定，不提供任意脚本、远程命令或远程删除能力。不覆盖客户端用户的统计关闭设置。客户端无法获取配置时，保留上次有效配置和本地默认值，不影响搜索与正常转存。

## 8. C05 心跳与账号快照

```http
POST /api/client/heartbeat
Authorization: Bearer <report_token>
```

```json
{
  "schema_version": 1,
  "snapshot_seq": 18,
  "reported_at": "2026-11-05T10:00:00+08:00",
  "app_version": "1.5.13",
  "platform": "fnos",
  "arch": "amd64",
  "accounts_complete": true,
  "accounts": [
    {
      "account_ref": "实例内随机账号引用",
      "provider": "baidu",
      "auth_status": "valid",
      "auth_checked_at": "2026-11-05T00:00:10+08:00",
      "check_enabled": true,
      "check_enabled_since": "2026-11-04T15:00:00+08:00",
      "next_check_at": "2026-11-06T00:00:00+08:00",
      "last_check_run_id": "run_随机标识",
      "last_check_result": "passed",
      "last_check_at": "2026-11-05T00:00:35+08:00",
      "cleanup_pending_count": 0
    }
  ],
  "subscriptions": {
    "total": 3,
    "enabled": 2
  },
  "active_jobs": 0
}
```

规则：

- 推荐每 10 分钟发送。时间和参数可由 C04 返回，客户端不能因过高频配置无限发送。
- `snapshot_seq` 在实例内持久递增。服务端仅用更大的序号更新当前快照，旧快照补报不能覆盖新状态。
- `reported_at` 与服务端 `received_at` 分开保存。
- 仅接收时间与发生时间接近的当前心跳可以推进在线状态；旧心跳补报不能把离线实例标为在线。首版允许时差 5 分钟，超出标为时钟异常/旧快照。
- 账号唯一键为 `(installation_id, account_ref)`；不能每次心跳累加账号数。
- `accounts_complete: true` 表示本次账号列表完整，缺失账号标记已移除，保留历史。
- 列表读取失败传 `accounts_complete: false`，允许更新已知项，但不能清空之前的账号列表。
- 首版最多 30 个账号；校验网盘枚举、数量非负、启用计划数不大于总数。
- `auth_status`：`valid` / `expired` / `unknown`。网络超时、测试源失效、空间不足不能标成 `expired`。
- 心跳本身不代表发生了人工使用行为，不计入活跃使用实例。
- 成功返回 `accepted_seq`、`server_time`；重复/旧序号返回 `applied: false`，不重复计数。

## 9. C06 批量事件上报

```http
POST /api/client/events/batch
Authorization: Bearer <report_token>
```

```json
{
  "schema_version": 1,
  "batch_id": "batch_随机标识",
  "events": [
    {
      "event_id": "evt_随机标识",
      "type": "account_check_finished",
      "occurred_at": "2026-11-05T00:00:35+08:00",
      "app_version": "1.5.13",
      "data": {
        "run_id": "run_随机标识",
        "account_ref": "实例内随机账号引用",
        "provider": "baidu",
        "trigger": "daily",
        "scheduled_for": "2026-11-05T00:00:00+08:00",
        "attempt": 1,
        "source_id": "probe_baidu_01",
        "source_version": "3",
        "auth_status": "valid",
        "result": "passed",
        "failed_stage": null,
        "error_code": null,
        "duration_ms": 35000,
        "cleanup_pending": false,
        "deletion_mode": "recycle_bin",
        "stages": [
          {"stage": "fetch_source", "status": "passed", "duration_ms": 100},
          {"stage": "validate_account", "status": "passed", "duration_ms": 900},
          {"stage": "prepare_target", "status": "passed", "duration_ms": 1500},
          {"stage": "transfer", "status": "passed", "duration_ms": 17000},
          {"stage": "verify_transfer", "status": "passed", "duration_ms": 2000},
          {"stage": "delete", "status": "passed", "duration_ms": 9500},
          {"stage": "verify_cleanup", "status": "passed", "duration_ms": 4000}
        ]
      }
    }
  ]
}
```

返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "accepted_event_ids": ["evt_随机标识"],
    "duplicate_event_ids": [],
    "rejected_events": [],
    "server_time": "2026-11-05T00:00:36+08:00"
  }
}
```

部分拒收示例：

```json
{
  "event_id": "evt_bad",
  "code": "INVALID_FIELD",
  "field": "data.duration_ms",
  "retryable": false
}
```

### 9.1 幂等、校验与补报

- 每批最多 100 条，总请求体最多 256 KiB，单事件建议最多 16 KiB。
- 幂等唯一键为 `(installation_id, event_id)`，不能只对 `batch_id` 去重。
- 同事件 ID、相同内容返回 duplicate；同 ID 不同内容返回冲突，不覆盖原记录。
- accepted/duplicate 只在持久化成功后返回。客户端仅删除明确确认收到或重复的本地事件。
- accepted、duplicate、rejected 三组必须覆盖本批全部事件，每个事件恰好出现一次；请求整体非法时使用 HTTP 错误。
- 支持乱序到达；完成事件先到、开始事件后到，不能把已完成任务改回进行中。
- 断网后按发生时间补报，默认接收最近 7 天；超期明确拒收，返回不可重试错误码。
- 未来超过 5 分钟的事件拒收并返回服务端时间，不能改写发生时间来伪造按时执行。
- 建议客户端本地待报队列最多保留 7 天、10,000 条、20 MiB，任一达到上限时按文档约定淘汰并记录丢弃数；不影响正常业务。
- 服务端保存 occurred_at 和 received_at。业务趋势按 occurred_at，报告延迟按两者差值。
- 限制事件类型与允许字段，拒绝未知敏感字段；错误文本由服务端错误码字典生成。
- 客户端重试和事件重复投递不能增加使用次数、转存次数或检查次数。

### 9.2 事件类型与字段

第一阶段必须实现 `account_check_*` 事件（含补充清理事件）。`search_*`、`transfer_finished`、`subscription_check_finished` 为第二阶段扩展，不阻塞首版。

所有事件带公共字段：`event_id`、`type`、`occurred_at`、`app_version`、`data`。以下为 `data` 字段。

| 事件类型 | 必需业务字段 | 用途 |
|---|---|---|
| `search_finished` | search_id、trigger、result、result_count、duration_ms、error_code | 一次整体搜索结果 |
| `search_source_finished` | search_id、source_id、result、result_count、duration_ms、cache_hit、error_code | 一次来源插件结果 |
| `transfer_finished` | job_ref、account_ref、provider、purpose、result、saved_count、skipped_count、filtered_count、total_count、duration_ms、error_code | 正常转存终态 |
| `subscription_check_finished` | job_ref、plan_ref、account_ref、provider、trigger、result、saved_count、skipped_count、filtered_count、total_count、duration_ms、error_code | 追更执行终态 |
| `account_check_started` | run_id、account_ref、provider、trigger、scheduled_for、attempt | 功能检查开始，识别未完成任务 |
| `account_check_stage_finished` | run_id、stage、status、duration_ms、error_code | 某个检查阶段结束 |
| `account_check_finished` | 见完整示例 | 整体检查终态和阶段最终快照 |

补充约定：

- `search_id`、`job_ref`、`plan_ref` 均为匿名业务标识，不包含标题、关键词或分享地址。
- 一次用户搜索只产生一条 `search_finished`。前端轮询、异步结果刷新和后台重试不重复计为用户搜索。
- 搜索 `trigger`：`user` / `diagnostic`；统计用户使用量时排除来源诊断试搜。
- 来源终态按 `(installation_id, search_id, source_id)` 去重，计一次来源调用，不把内部 HTTP 重试算成多次插件调用。
- 来源状态：`succeeded` / `failed` / `timeout` / `cancelled`；无结果是 succeeded 且 result_count 为 0。
- 整体搜索状态：`succeeded` / `partial` / `failed` / `cancelled`。支持部分来源失败但仍返回结果。
- 转存 `purpose` 首版为 `user_transfer`；测试转存只通过 account_check 事件统计，追更只通过 subscription_check 事件统计，不能再重复上报 transfer_finished。
- 转存/追更状态：`succeeded` / `partial` / `failed` / `interrupted` / `cancelled`。
- 追更 `trigger`：`scheduled` / `manual`。`saved_count > 0` 才算本次发现并保存了新增内容。
- 文件数量不等于集数，统计端不把 saved_count 当更新集数。
- 正常任务业务唯一键使用实例 + job_ref；同一业务终态即使换 event_id 重报，也不能重复聚合。
- 同一业务标识的冲突终态需要记录异常，不按到达顺序随意覆盖。

### 9.3 每日功能检查专用约定

- `trigger`：`daily` / `catch_up` / `manual`。
- daily/catch_up 必须带 `scheduled_for`，为应执行日期的北京时间 00:00；manual 必须为 null。
- `run_id` 表示一次实际执行，重试必须使用新 run_id，`attempt` 递增。
- daily/catch_up 同一计划槽的唯一键：`(installation_id, account_ref, scheduled_for)`。一次计划槽可有多次尝试，但每天应检查账号数量只计一次。
- 手动检查不能自动抵扣每日计划完成量。
- 检查终态 `result`：`passed` / `failed` / `incomplete` / `skipped`。
- 阶段状态：`passed` / `failed` / `unknown` / `skipped`。
- 仅所有必需阶段确认通过，才允许 result=passed。网络响应成功不能代替文件实际保存/删除的确认。
- 发现 transfer 结果不确定或 cleanup 未确认时，不得上报完整通过。
- `failed_stage` 使用固定阶段枚举；`error_code` 使用固定码，不含真实目录或原始响应。
- `auth_status=expired` 只能来自明确的认证失败；业务失败使用 valid 或 unknown。
- `cleanup_pending=true` 表示仍有需确认/清理的测试内容。后续清理应记录关联原 run_id 的补充结果，不删除原失败记录。
- 补充事件 `account_check_cleanup_finished`：run_id、result（passed/failed/unknown）、duration_ms、error_code、deletion_mode。它更新遗留清理状态，保留原失败过程；若原 run_id 的转存已确认且本次删除也确认成功，则该 run_id 的完整成功次数由 0 变为 1，记录为补充清理后完成，不改写为首次执行即成功。
- `deletion_mode`：`recycle_bin` / `permanent` / `unknown`。移入回收站可以证明删除能力，但不能宣称已永久清除或释放容量。
- `account_check_finished.stages` 必须包含全部阶段，未执行的标为 skipped；允许它独立补齐丢失的阶段事件。
- 清理结果只能指本次独立测试目录和文件，接口不接受由服务器指定用户网盘删除路径。

建议错误码：

```text
SOURCE_NOT_CONFIGURED
SOURCE_DISABLED
SOURCE_EXPIRED
SOURCE_UNAVAILABLE
SOURCE_INVALID_RESPONSE
TEST_FILE_NOT_FOUND
TEST_FILE_TOO_LARGE
AUTH_EXPIRED
AUTH_IDENTITY_MISMATCH
RATE_LIMITED
NETWORK_TIMEOUT
PROVIDER_UNAVAILABLE
INSUFFICIENT_SPACE
TARGET_PREPARE_FAILED
TRANSFER_FAILED
TRANSFER_UNCONFIRMED
DELETE_FAILED
CLEANUP_UNCONFIRMED
CLIENT_INTERRUPTED
CONFIG_DISABLED
CLOCK_SKEW
```

未知上游错误映射为受控的 `PROVIDER_UNAVAILABLE` 或对应阶段通用失败码，不上传上游原始异常。

### 9.4 核心计数规则（服务端计算）

1. 计数单位为一次实际检查，唯一键 `(installation_id, run_id)`。一个账号一次检查最多贡献 1 次。
2. 同一 run_id 的 account_ref、provider、scheduled_for 不得变化；冲突记录拒收并供后台排查。
3. 必须同时具备本次 `transfer=passed`、`verify_transfer=passed`、`delete=passed`、`verify_cleanup=passed`，且 cleanup_pending=false，才能记为完整成功；清理补充事件可补齐后两项。
4. 补充清理事件只在原 run_id 已存在且原转存确认成立时生效。先到的补充清理可暂存待关联，不能仅凭删除成功先加次数。
5. 服务端从阶段结果推导 `cycle_completed`，不接受客户端直接上传「累计次数」作为真实统计。不因同事件换 event_id、乱序补报、重复阶段、重启重传而重复增加。
6. `cycle_completed_at` 使用删除确认事件的 occurred_at；若阶段事件缺失，使用包含完整阶段快照的终态事件 occurred_at。不能使用服务器收到补报的日期代替实际完成日期。
7. 补充删除后完成的计数，归到本次删除确认发生日期；原计划 scheduled_for 日期仍保留，可查看跨日补检/清理完成情况。
8. 一次原尝试失败后真正重新转存、重新清理，使用新 run_id。统计「实际完整成功次数」按真实完成的不同 run_id 计数；每日计划覆盖另外按检查槽去重。一个计划槽成功后客户端应停止自动重试。
9. manual、daily、catch_up 分开展示。默认每日检查主卡片展示 daily + catch_up；手动成功次数单独展示；全类型合计必须标明范围。
10. `successful_cycles` 为上述完整成功次数；`daily_successful_slots` 为至少一个 run 完整成功的每日计划槽数。两者名称与口径不同，不能混用。
11. 检查仍失败、仅部分成功、未知或未上报均不贡献成功次数；保留失败及异常明细。
12. 此数值是按客户端执行报告计算的成功闭环次数，不是平台认可的计费转存、结算次数或金额。无需对接收益平台。

## 10. 后台统计查询接口

以下接口均属于后台管理权限，禁止公开匿名查询。

| 编号 | 方法与路径 | 主要数据 |
|---|---|---|
| A01 | `GET /api/admin/analytics/overview` | 核心：每日完整成功次数、各网盘分布、运行实例与账号 |
| A02 | `GET /api/admin/analytics/trends` | 核心：成功次数、每日运行实例、连续运行及留存趋势 |
| A03 | `GET /api/admin/analytics/instances` | 匿名实例、版本、在线状态、最后上报 |
| A04 | `GET /api/admin/analytics/accounts` | 匿名账号当前状态与最近检查 |
| A05 | `GET /api/admin/analytics/checks` | 核心：每日/手动检查、转存删除阶段、是否计次、计次时间 |
| A06 | `GET /api/admin/analytics/events` | 脱敏原始事件明细 |
| A07 | `GET /api/admin/analytics/checks/:run_id` | 检查阶段、尝试历史、清理补充结果 |

A07 还需 `installation_id` 查询参数，按实例 + run_id 定位，避免不同实例生成相同 run_id 时串数据。

### 10.1 查询参数

- `from`、`to`：RFC 3339，左闭右开区间 `[from, to)`。
- `timezone`：首版只接受 Asia/Shanghai，其他值明确拒绝。
- `provider`：网盘筛选。
- `app_version`：客户端版本筛选。
- `installation_id`：匿名实例筛选。
- `source_id`：搜索来源或测试来源，必须同时通过事件类型/指标确定语义，不混合两类。
- `result`、`error_code`、`trigger`：明细接口筛选。
- `limit`、`cursor`：游标分页，limit 默认 20，最大 100。
- trends 另有 `metric`、`granularity=day|hour`、`group_by`。
- `group_by` 仅允许 provider / source_id / app_version / result / error_code；不接受任意 SQL 字段。
- 限制查询跨度，例如趋势最多 366 天、明细最多 31 天。
- 不支持的参数/指标组合返回明确错误，不能静默忽略筛选条件。

分页响应：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [],
    "next_cursor": null,
    "as_of": "2026-11-05T10:00:00+08:00"
  }
}
```

计数指标返回整数 value；比例指标同时返回 numerator / denominator。核心次数趋势示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "metric": "successful_cycles",
    "granularity": "day",
    "timezone": "Asia/Shanghai",
    "group_by": "provider",
    "items": [
      {
        "bucket": "2026-11-05",
        "group": "baidu",
        "value": 8
      }
    ],
    "as_of": "2026-11-05T10:00:00+08:00"
  }
}
```

上例仅用于说明协议，开发验收必须使用实际入库数据聚合，不能用固定数值冒充统计。

### 10.2 第一阶段核心看板与口径

首页优先展示：**今日完整成功次数、百度次数、夸克次数、迅雷次数、累计成功次数、当前在线实例、近 7 天运行实例、连续 7 天运行实例**。再展示转存失败、删除未完成和未上报，方便定位次数下降原因。

| 指标 / 字段 | 口径 |
|---|---|
| `successful_cycles` | 区间内 cycle_completed_at 对应的去重成功 run 数，默认 daily + catch_up |
| `successful_cycles_by_provider` | 按 baidu / quark / xunlei 分组，与同筛选口径总数相加一致 |
| `manual_successful_cycles` | trigger=manual 的成功 run 数，单独展示 |
| `cumulative_successful_cycles` | 自统计上线以来同一筛选范围的成功总数，不因原始事件过期归零 |
| `daily_successful_slots` | 至少一次完整成功的每日计划槽数；按 scheduled_for 归属日期 |
| `transfer_confirmed_runs` | 本次转存及保存确认通过的 run 数，不代表已计入完整成功次数 |
| `cleanup_pending_runs` | 已确认转存、但删除/清理尚未确认的 run 数 |
| `transfer_failed_runs` | 转存明确失败的 run 数，未知状态另列 |
| `unknown_runs` | 转存或清理结果不确定的 run 数，不计成功 |
| `expected_daily_slots` | 按最近已知启用配置估算的应检查账号数 |
| `unreported_daily_slots` | 应检查但尚未收到开始/终态报告的槽数，不算登录失效 |
| 累计登记实例 | 去重 installation_id，只代表匿名安装实例，不等于真实人数或硬件数 |
| 新增实例 | 首次登记时间落在区间的实例数 |
| 当前在线实例 | 最近有效当前心跳距 as_of 不超过 30 分钟 |
| 每日运行实例 | 当天有时间有效心跳或合法检查执行事件的去重实例；检查失败也证明客户端当时运行 |
| 近 7/30 天运行实例 | 最近 7/30 个北京时间自然日内至少有一天运行的去重实例；包含当天 |
| 连续 7/30 天运行实例 | 截至昨天连续 7/30 个完整北京时间自然日，每天均有运行证据的实例 |
| 次日运行留存 | 首次登记日期 D 的实例中，D+1 有运行证据的数量 / D 首次登记数 |
| 7 日运行留存 | 首次登记日期 D 的实例中，D+7 有运行证据的数量 / D 首次登记数；不等于连续运行 7 天 |
| 当前账号数 | 最新完整快照中未移除的匿名账号，按网盘分组 |
| 最近验证有效账号 | 最新 auth_status=valid 且验证距 as_of 不超过 26 小时 |
| 明确失效 / 待验证账号 | expired 与 unknown/过期的 valid 记录分开展示 |
| 最近功能正常账号 | 最近检查对应 run 已完成闭环，完成时间距 as_of 不超过 26 小时；只登录有效不算 |
| 版本分布 | 最新有效实例快照按 app_version 分组，注明在线实例或全部实例分母 |
| 阶段失败分布 | 按失败阶段、错误码、网盘和 source_version 聚合 |

- 每日运行/留存反映持续运行软件，不等于用户主动操作。人工活跃为第二阶段指标。
- 连续运行按完整日统计，避免今天尚未到检查时间就判断掉线。留存观察日未结束的 cohort 返回待观察，不算 0%。
- 留存查询支持 `metric=retention_d1|retention_d7`，bucket 为首次登记日，返回 numerator、denominator、value、matured。首版留存限定按实例及首次登记版本筛选，不支持 provider/source_id 分组。
- 心跳需保留去重实例每日运行标记，不能只保存最后一次心跳后尝试倒推历史。
- 功能次数按实际闭环完成时间过滤；每日计划指标按 scheduled_for 过滤，API 返回 `time_basis=completed_at|scheduled_for` 明示口径。
- 次数趋势支持 day/hour；运行留存、连续天数只支持 day。不支持的组合返回明确错误。
- 分母为零时比例返回 null，展示「暂无样本」。
- 统计关闭、从未上报、清空重装或多设备安装会影响观察范围，不强行还原真实人数。

总览 data 示例（仅协议示例，服务端需真实聚合）：

```json
{
  "from": "2026-11-05T00:00:00+08:00",
  "to": "2026-11-06T00:00:00+08:00",
  "as_of": "2026-11-05T10:00:00+08:00",
  "timezone": "Asia/Shanghai",
  "time_basis": "completed_at",
  "triggers": ["daily", "catch_up"],
  "successful_cycles": 3,
  "successful_cycles_by_provider": {
    "baidu": 2,
    "quark": 1,
    "xunlei": 0
  },
  "manual_successful_cycles": 1,
  "transfer_confirmed_runs": 4,
  "cleanup_pending_runs": 1,
  "online_instances": 2,
  "running_instances_7d": 5,
  "continuous_running_instances_7d": 2
}
```

明细 A05/A07 除原始阶段外，返回服务端推导的 `cycle_completed`、`cycle_completed_at`、`completion_method=initial|cleanup_recovery`、`counted_value=0|1` 和 `not_counted_reason`。客户端无权直接决定 counted_value。

### 10.2.1 第二阶段扩展指标

搜索次数、搜索来源调用量、无结果率、缓存命中率、平均/P95 耗时、人工转存文件数、广告过滤数、追更执行和新增率。这些数据不影响第一阶段每日闭环次数，也不混入 successful_cycles。

### 10.3 每天应检查与实际检查

服务端需要保留账号检查启停状态的历史，而不仅是最新数量。

- 每日 00:00 为在此时已登记且启用检查的账号生成「预期检查槽」，不意味着服务端实际执行转存。
- 当天 00:00 后新连接/新启用的账号，从下一个 00:00 开始计入预期；手动检查单独统计。
- 每个槽可关联 daily/catch_up 多次尝试，分别显示首次结果、最新结果、尝试次数、是否最终通过。
- 截止统计时未收到结果，标为「未上报/待执行」，不能直接判定登录失效或执行失败。
- 建议 00:10 为按时检查宽限；10 分钟后仍未开始标为延迟，之后收到结果可更新为补检完成。
- 展示应检查账号数、已开始数、已完成数、完整通过数、失败数、未完成数、跳过数、未上报数。
- 覆盖率 = 有终态的预期槽数 / 应检查槽数；通过率需注明按实际执行尝试还是按计划槽聚合。
- 手动检查不计入每日计划分母和覆盖率。
- 账号离线期间的真实启停变化服务端可能不知道，预期数应标明「按最近已知启用配置估算」。收到有发生时间的更新后允许校正并记录依据。
- 实例长时间不再上报时，区分近期上报实例与所有历史实例，避免历史离线安装无限拉低每日覆盖率。

## 11. 后台配置管理接口

沿用目标服务端已有后台框架和 CRUD 机制；以下为需要具备的能力和建议路径。

| 方法与路径 | 需求 |
|---|---|
| `GET /api/admin/check-sources` | 查看三种网盘测试源配置 |
| `PUT /api/admin/check-sources/:provider` | 设置 source_id、链接、提取码、文件路径、enabled、expires_at；递增配置版本 |
| `GET /api/admin/search-sites` | 查看全部来源 |
| `POST /api/admin/search-sites` | 新增来源，校验 ID 唯一 |
| `PATCH /api/admin/search-sites/:id` | 修改名称、地址、启停、排序；ID 不变 |
| `DELETE /api/admin/search-sites/:id` | 移除配置，保留历史统计关联；客户端同步时移除该配置 |
| `GET /api/admin/client-settings` | 查看心跳、批量限制、统计保留期等配置 |
| `PATCH /api/admin/client-settings` | 修改允许调整的参数，执行范围校验并更新配置版本 |
| `POST /api/admin/instances/:id/revoke` | 撤销实例上报凭证，立即生效；保留历史数据 |

- 管理操作均做权限检查与审计，审计不记录明文凭证或分享提取码。
- source_version、config_version 在持久化成功后变化。
- 来源 ID/测试源 ID 的历史统计不能因改名、停用、删除而串到新记录。
- 不因普通查询或测试接口自动上传、公开分享管理员网盘文件。

## 12. 接口开放与文档要求

复用目标项目的统一路由、鉴权、权限和接口注册机制，不另复制一套业务处理逻辑。

- 为上述接口维护稳定接口标识、HTTP 方法、路径、分组、参数、响应和权限要求。
- 后台可以按接口授权/撤销 API Key 权限，已纳入体系的权限变更无需重启或发版。
- 新增接口默认不授权既有 Key；同一路径不同 HTTP 方法分别控制。
- 公共只读接口、首次登记、上报凭证和管理身份保留各自语义，不能统一放宽为公开访问。
- 如目标项目已有自有接口中心，在其中提供本次接口说明和真实调试；没有时按项目自身 UI 建立本次所需的接口文档与调试页，不要求更换技术栈。
- 文档至少包括参数、枚举、必填项、请求/响应示例、错误码、分页、幂等和时间口径。
- 调试严格使用所选 Key，不得用后台登录身份兜底；明文密钥不进入浏览器持久存储或示例文件。
- 可导出 OpenAPI，但不以 Swagger UI 代替自有接口中心。
- 对修改配置、撤销凭证等真实写操作提示实际影响。

## 13. 服务端存储与保留要求

不强制数据库技术，沿用目标项目。

至少需要能够存储：

1. 实例身份、登记时间、凭证摘要、撤销状态。
2. 实例最新快照、快照序号、发生/接收时间、版本、有效心跳时间。
3. 匿名账号最新状态与检查启停历史。
4. 原始事件、事件幂等键、业务终态唯一键、接收时间。
5. 检查 run、阶段、每日预期槽、重试关联、清理补充结果、唯一闭环计数台账（实例 + run_id）。
6. 搜索来源与测试源配置、版本和管理审计。
7. 可重算的每日聚合、实例每日运行标记、首次登记 cohort、长期累计成功次数；不把事件接收次数直接当业务次数。

建议默认保留：原始事件与检查明细 90 天、每日匿名汇总 365 天，允许后台调整。晚到 7 天内事件需重算相应发生日期；去重/业务唯一键不能早于最大补报窗口被删除。数据清理不能使仍可接受的重报再次计数。

核心的每日运行、连续运行和留存需要保留实例每日运行标记，首版必须支持，不能只依赖当前快照。其他历史账号状态趋势可作为扩展。累计成功次数应保留自上线以来的去重汇总；不能随 90 天明细或 365 天趋势清理而归零。

## 14. 验收清单

### 公共配置

- [ ] 三种网盘均能返回管理员实际配置的测试源。
- [ ] 修改、停用、到期后，下次请求立即反映变化。
- [ ] 不存在测试源时返回明确错误，不假装检查成功。
- [ ] 43 个搜索来源种子数据可以导入，唯一 ID 和地址保持对应。
- [ ] 搜索来源改名、启停、排序正确；相同 URL 的不同插件不被误合并。

### 身份与隔离

- [ ] 不同实例获得不同上报凭证。
- [ ] 仅知道实例 ID 不能获取/覆盖已有凭证。
- [ ] 凭证失效、撤销、越权请求被拒绝，不回退后台身份。
- [ ] 实例 A 不能上报到实例 B，也不能查询全站后台统计。
- [ ] 已注册的一个接口，API Key 未授权时拒绝，后台授权后可调用，撤销后立即拒绝，全程不改代码、不重启、不发版。

### 事件与快照

- [ ] 同一批次重复、同一事件换批次重发，统计只增加一次。
- [ ] 同一业务终态换 event_id 重发，仍只增加一次。
- [ ] 同 ID 不同内容返回冲突，不静默覆盖。
- [ ] 同批部分非法事件分别返回结果，不导致已确认数据重计。
- [ ] 完成事件先于开始/阶段事件到达，最终状态仍正确。
- [ ] 断网补报归属原发生日期；超期事件明确拒收。
- [ ] 旧心跳不覆盖新快照，不把离线实例伪装为在线。
- [ ] 不完整账号快照不清空原账号；完整快照支持标记账号移除。

### 核心计次与持续运行

- [ ] 同一账号同一 run_id 转存与删除均确认成功，计 1 次；任一未确认，计 0 次。
- [ ] 百度、夸克、迅雷各成功一次，总数为 3，各网盘分别为 1。
- [ ] 转存失败但空目录删除成功，不计 1 次。
- [ ] 同一完成结果重复发送、换 event_id、换 batch_id、重启后重报，仍只计 1 次。
- [ ] 原转存确认、后补充删除成功，原 run 计数从 0 变 1；重复补充清理不再加次数。
- [ ] 清理补充事件先到、转存确认后到，最终只计 1 次；没有转存确认时保持 0。
- [ ] 跨日补充清理按实际确认日期计次，计划覆盖仍按原 scheduled_for 归属。
- [ ] 手动次数与自动每日次数分开，文件数不作为检查次数。
- [ ] 删除旧事件后累计次数保持，不再接受的过期重报不能重复增量。
- [ ] 连续 7 天运行与 D+7 留存分别正确，未到观察日的不算流失。
- [ ] 开始执行但失败可以算当日运行实例，不能算成功闭环次数。
- [ ] 首版无金额字段、计费、收益回传、对账和结算接口。

### 检查统计

- [ ] 认证有效但转存失败：不计功能正常，不计 Cookie 失效。
- [ ] 测试分享失效：计测试源故障，不计登录失效。
- [ ] 转存成功但删除失败：不计完整通过，计清理遗留。
- [ ] 删除请求成功但未确认文件消失：计 cleanup_unconfirmed，不假装闭环完成。
- [ ] 后续清理成功可解除遗留标记，原失败记录保留。
- [ ] daily / catch_up / manual 区分，补检不重复增加应检查账号数。
- [ ] 未上报、失败、跳过、未完成分别展示；未上报不当登录失效。
- [ ] 功能测试不增加用户转存量或用户活跃使用量。
- [ ] 跨午夜事件按约定时区和 scheduled_for 归属，手动检查不抵扣每日检查槽。

### 分析与交付

- [ ] 总览、趋势、明细来自实际入库数据，口径与本文一致。
- [ ] 分母为零返回 null；每个百分比可查看分子和分母。
- [ ] 分页、筛选、版本和网盘分组不串数据。
- [ ] 接口日志、原始事件、管理审计不含网盘凭据、搜索词或分享内容。
- [ ] 提供实际 API 基址、鉴权说明、真实返回样本、错误码、字段映射、后台配置入口与测试结果。
- [ ] 提供接口文档或 OpenAPI 导出，明确哪些能力未实现，不用演示数据冒充实测。

## 15. 交付给小白搜盘对接的信息

开发完成后请提供：

1. 公共配置与客户端上报的实际 API 基址。
2. 上述六个客户端接口的真实路径、请求与响应示例。
3. 注册/上报凭证的签发、撤销、有效期和恢复规则。
4. 三种网盘真实小文件测试源配置状态。
5. 搜索来源配置与当前 43 个插件 ID 的对应关系。
6. 服务端支持的 schema_version、请求大小、限流、补报窗口。
7. 后台统计及接口中心入口。
8. 三种网盘完整成功次数、按日趋势、持续运行与留存查询的真实返回样本。
9. 验收结果与已知限制，明确本期仅统计次数，不计算金额。

服务端接口完成后，再由小白搜盘实现客户端对接、每日调度、真实转存删除、上报队列和界面展示；本需求文档本身不代表这些客户端功能已经完成。
