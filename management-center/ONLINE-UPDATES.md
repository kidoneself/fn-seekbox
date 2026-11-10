# 在线更新：管理中心开发副本

本目录最初用于独立开发和联调。2026-11-06 经用户授权，在线更新发布功能已部署到管理中心；使用长期发布公钥，保留原数据库、管理员密码与公共配置。`docs/live-verification.json`、旧截图等是归档资料；本次正式启用记录见上级 `ONLINE-UPDATE-ACTIVATION.md`。

## 本地运行

默认 `compose.yaml` 已改为独立项目 `seekbox-update-development`，只绑定 `127.0.0.1:18999`，使用 `dev-data/`、`dev-secrets/` 和自己的 Docker 网络。原配置保留在 `compose.production.example.yaml` 供对照，默认不会连接生产网络或挂载生产数据。

先准备管理员密码的 bcrypt 摘要和发布公钥配置，禁止复用线上 Cookie 或 Token。服务端不需要也不保存签名私钥。

```sh
cd backend
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o ../build/server .
cd ../frontend
npm ci
npm run build
```

本地直接启动示例（路径替换为本地临时文件）：

```sh
./build/server \
  -addr=127.0.0.1:18999 \
  -db=/tmp/seekbox-update-dev/config.db \
  -seed= \
  -web=./frontend/dist \
  -admin-password-hash-file=/tmp/seekbox-update-dev/admin-password-hash \
  -origin=http://127.0.0.1:18999 \
  -allow-insecure-loopback \
  -release-public-keys-file=/tmp/seekbox-update-dev/release-public-keys.json
```

管理页地址 `http://127.0.0.1:18999/seekbox-config/`。`-allow-insecure-loopback` 只允许 localhost/127.0.0.1/::1 的 HTTP Origin，用于本地测试；线上仍应使用 HTTPS，Cookie 保持 Secure。如果运行 Docker，`dev-data/` 需允许容器 UID 10001 写入，两个公开配置文件和 bcrypt 摘要需允许该 UID 读取。

公钥文件内容：

```json
{"release-key-2026":"<32字节Ed25519公钥的标准base64>"}
```

也可使用 `SEEKBOX_RELEASE_PUBLIC_KEYS` 环境变量提供相同 JSON。文件参数优先；无公钥时可查看管理页，但不能上传发布包。公钥轮换需要同时更新客户端信任配置，不能只修改此服务器。

## 发布流程

1. 在客户端项目用离线打包工具生成更新包和签名清单。
2. 管理中心打开“版本发布”，选择两个文件。服务端验证签名、大小和 SHA256 后保存草稿。
3. 确认版本号、架构、渠道与更新说明，点击“发布”。只有已发布版本才能查询和下载。
4. 出现问题时点击“撤回”，立即停止后续查询与下载。已开始的下载不被中断，已安装的客户端也不会自动降级；客户端安装前还应检查版本是否仍发布。

发布包内容不可覆盖。同版本、渠道、架构重复上传返回冲突。撤回版本不能再次发布；修复后使用新版本号。服务端只存公钥，不能替打包机生成签名。

## 协议

所有 JSON 接口复用原格式：`{code:0,request_id,message,data:...}`，错误使用非 0 业务码与对应 HTTP 状态。下载接口返回 gzip 二进制。

### 签名清单

外层 JSON：

```json
{"key_id":"release-key-2026","payload":"<标准base64>","signature":"<标准base64>"}
```

`payload` 是下列 JSON 的原始 UTF-8 字节的 base64；Ed25519 签名对这些原始字节计算，不先规范化或重新序列化：

```json
{
  "schema":1,
  "app":"fn-seekbox",
  "version":"1.6.1",
  "channel":"stable",
  "arch":"amd64",
  "min_bootstrap":1,
  "data_schema":1,
  "sha256":"<更新包SHA256小写十六进制>",
  "size":12345,
  "notes":"更新说明"
}
```

首版版本号采用严格的三段非负整数，无前导零、v 前缀或 prerelease 后缀；测试版本使用 `beta` 等独立渠道。架构为 amd64/arm64。更新包上限 256 MiB，清单上限 64 KiB。

### 客户端

| 方法与地址 | 说明 |
| --- | --- |
| `GET /api/client/releases/latest?app=fn-seekbox&channel=stable&arch=amd64&version=1.6.0` | 公开查询；返回同渠道架构最高且高于当前版本的已发布版本 |
| `GET /api/client/releases/:id/artifact` | 公开下载已发布包，草稿/撤回返回 404；支持 Range，无缓存 |
| `POST /api/client/releases/report` | 使用已有 report_token 上报实际更新结果 |

有更新的 `data`：

```json
{"available":true,"release_id":"rel_...","manifest":{"key_id":"...","payload":"...","signature":"..."},"download_url":"https://<服务域名>/api/client/releases/rel_.../artifact"}
```

无更新：`{"available":false}`。客户端必须自行验签、验证文件并检查 `min_bootstrap`、`data_schema`，不能仅信任服务端返回的版本号或地址。

结果报告请求：

```json
{
  "release_id":"rel_...",
  "event_id":"<本次结果持久随机ID>",
  "version":"1.6.1",
  "from_version":"1.6.0",
  "status":"success",
  "error_code":"",
  "occurred_at":"2026-11-06T03:00:10+08:00"
}
```

- `status` 为 success/failed/rolled_back。error_code 只接受大写字母、数字、下划线，不能包含日志、路径、Cookie 等敏感数据；成功必须为空。
- 服务端按 Token 确定实例，拒绝客户端提交 installation_id。目标 release_id 必须对应相同版本和实例架构且曾发布；撤回后仍可补报历史结果。
- release_id 可省略以兼容旧报告，但有多个渠道同版本时必须提供，否则 409。
- 同实例与 event_id 去重，重复内容返回成功；同 ID 内容变更返回 409。不同实例相同 event_id 不相互覆盖。
- 时间允许过去 90 天至未来 5 分钟。
- 接收回执 `data={"event_id":"<同请求>","accepted":true,"duplicate":false}`；重复时 duplicate=true、accepted 仍为 true。客户端拿到确认后才移除本地待报记录。

### 管理员

| 方法与地址（前缀 `/seekbox-config/api`） | 说明 |
| --- | --- |
| `GET /releases` | 版本、结果次数、当前版本分布、最近 100 条上报 |
| `POST /releases` | multipart 文件字段 manifest、artifact，创建草稿 |
| `POST /releases/:id/publish` | JSON `{}`，发布草稿，重新验签和校验文件 |
| `POST /releases/:id/withdraw` | JSON `{}`，撤回已发布版本 |

写操作仅允许原管理员会话，保留同源 Origin 校验，不允许 API Key 发布包；显式错误 Key 不回退到会话身份。读取版本清单可沿用现有具体接口授权。接口中心和 OpenAPI 从真实路由注册信息生成这些接口说明。

## 统计口径

- 发布记录的 success/failed/rolled_back 是按实例+event_id 去重后的**结果次数**，不是独立机器数。
- 当前版本分布从实例最新心跳与最新更新结果的时间比较得到。成功采用目标版本；失败或回滚采用更新前版本。迟到结果不覆盖更晚的心跳。
- 版本分布排除已撤销实例，但包含离线实例，不能作为在线机器数。
- 最近结果展示最多 100 条，同时返回总数。数据持久化在原 bbolt 数据库的新 bucket 中，不迁移已有配置与账号统计。

## 验证

```sh
cd backend
go test ./...
go vet ./...
cd ../frontend
npm run build
```

`releases_test.go` 覆盖未登录/错误 Origin/Key 越权拒绝、错误签名与哈希、草稿隔离、发布撤回、数字版本比较、包被篡改后拒绝发布、实例隔离、上报去重冲突、回滚版本分布及接口文档鉴权标记。所有测试使用独立临时数据库和随机测试签名密钥，不连接真实管理服务。
