# Gying 搜索插件

## 📖 简介

Gying 是 PanSou 的搜索插件，用于抓取 Gying 站点的影视资源。当前实现里，`https://www.gying.net` 只是默认站点地址，实际支持在管理页里配置自定义域名 / 站点地址；搜索时会聚合所有有效账户的结果。

## ✨ 核心特性

- ✅ **多用户支持** - 每个用户独立配置，互不干扰
- ✅ **自定义域名** - 支持在管理页配置站点地址，登录/搜索/详情请求都基于当前 `base_url`
- ✅ **用户名密码登录** - 支持使用用户名和密码登录
- ✅ **智能去重** - 多用户搜索时自动去重
- ✅ **负载均衡** - 任务均匀分配，避免单用户限流
- ✅ **内存缓存** - 用户数据缓存到内存，搜索性能极高
- ✅ **持久化存储** - Cookie和用户配置自动保存，重启不丢失
- ✅ **Web管理界面** - 一站式配置，简单易用
- ✅ **RESTful API** - 支持程序化调用
- ✅ **默认账户自动登录** - 插件启动时自动使用默认账户登录
- ✅ **反爬挑战处理** - 内置 `cloudscraper` 和挑战页求解逻辑，可自动处理新版“浏览器安全验证”页面，并在登录失效时自动重登重试

## 🚀 快速开始

### 步骤1: 启动服务

```bash
cd /Users/macbookpro/Desktop/fish2018/pansou
go run main.go

# 或者编译后运行
go build -o pansou main.go
./pansou
```

### 步骤2: 访问管理页面

如果需要添加更多账户或管理现有账户，可以访问管理页面：

```
http://localhost:8888/gying/你的用户名
```

**示例**：
```
http://localhost:8888/gying/myusername
```

系统会自动：
1. 根据用户名生成专属64位hash（不可逆）
2. 重定向到专属管理页面：`http://localhost:8888/gying/{hash}`
3. 显示登录表单供手动登录

**📌 提示**：请收藏hash后的URL（包含你的专属hash），方便下次访问。

### 步骤3: 先配置站点地址（重要）

进入管理页后，建议先在“站点地址”区域配置你的自定义域名 / 站点地址，再进行登录。

当前代码逻辑是：

- 默认站点地址为 `https://www.gying.net`
- 初始会话页：`{base_url}`
- 登录接口：`{base_url}/user/login`
- 搜索页：`{base_url}/search?q={keyword}&type=0&mode=2`
- 详情接口：`{base_url}/res/downurl/{type}/{id}`

也就是说，插件不是固定写死抓某一个域名，而是所有核心请求都基于当前 `base_url` 动态拼接。

**📌 提示**：

- 站点地址会保存到 `cache/gying_users/gying_config.json`
- 修改站点地址后，插件会清空当前登录状态和搜索缓存，需要重新登录
- 站点地址只允许填写纯域名，例如 `https://your-gying-domain.com`

### 步骤4: 手动登录

在"登录状态"区域输入：
- 用户名
- 密码

点击"**登录**"按钮。

### 步骤5: 开始搜索

在PanSou主页搜索框输入关键词，系统会**自动聚合所有用户**的Gying搜索结果！

```bash
# 通过API搜索
curl "http://localhost:8888/api/search?kw=遮天"

# 只搜索插件（包括gying）
curl "http://localhost:8888/api/search?kw=遮天&src=plugin"
```

## 📡 API文档

### 统一接口

所有操作通过统一的POST接口：

```
POST /gying/{hash}
Content-Type: application/json

{
  "action": "操作类型",
  ...其他参数
}
```

### API列表

| Action | 说明 | 需要登录 |
|--------|------|---------|
| `get_status` | 获取状态 | ❌ |
| `get_config` | 获取当前站点地址 | ❌ |
| `update_config` | 更新站点地址 | ❌ |
| `login` | 登录 | ❌ |
| `logout` | 退出登录 | ✅ |
| `test_search` | 测试搜索 | ✅ |

---

### 1️⃣ get_status - 获取用户状态

**请求**：
```bash
curl -X POST "http://localhost:8888/gying/{hash}" \
  -H "Content-Type: application/json" \
  -d '{"action": "get_status"}'
```

**成功响应（已登录）**：
```json
{
  "success": true,
  "message": "获取成功",
  "data": {
    "hash": "abc123...",
    "logged_in": true,
    "status": "active",
    "username": "pansou",
    "login_time": "2025-10-28 12:00:00",
    "expire_time": "2026-02-26 12:00:00",
    "expires_in_days": 121
  }
}
```

**成功响应（未登录）**：
```json
{
  "success": true,
  "message": "获取成功",
  "data": {
    "hash": "abc123...",
    "logged_in": false,
    "status": "pending"
  }
}
```

---

### 2️⃣ login - 登录

**请求**：
```bash
curl -X POST "http://localhost:8888/gying/{hash}" \
  -H "Content-Type: application/json" \
  -d '{"action": "login", "username": "xxx", "password": "xxx"}'
```

**成功响应**：
```json
{
  "success": true,
  "message": "登录成功",
  "data": {
    "status": "active",
    "username": "xxx"
  }
}
```

**失败响应**：
```json
{
  "success": false,
  "message": "登录失败: 用户名或密码错误"
}
```

---

### 3️⃣ get_config - 获取站点地址

**请求**：
```bash
curl -X POST "http://localhost:8888/gying/{hash}" \
  -H "Content-Type: application/json" \
  -d '{"action": "get_config"}'
```

**成功响应**：
```json
{
  "success": true,
  "message": "获取成功",
  "data": {
    "base_url": "https://www.gying.net"
  }
}
```

---

### 4️⃣ update_config - 更新站点地址

**请求**：
```bash
curl -X POST "http://localhost:8888/gying/{hash}" \
  -H "Content-Type: application/json" \
  -d '{"action": "update_config", "base_url": "https://your-gying-domain.com"}'
```

**成功响应**：
```json
{
  "success": true,
  "message": "站点地址已保存，当前登录状态已清空，请重新登录",
  "data": {
    "base_url": "https://your-gying-domain.com"
  }
}
```

**说明**：

- 保存前会自动做域名标准化
- 不允许带路径、查询参数或锚点
- 如果域名发生变化，所有用户当前 Cookie 会被清空并回到 `pending`

---

### 5️⃣ logout - 退出登录

**请求**：
```bash
curl -X POST "http://localhost:8888/gying/{hash}" \
  -H "Content-Type: application/json" \
  -d '{"action": "logout"}'
```

**成功响应**：
```json
{
  "success": true,
  "message": "已退出登录",
  "data": {
    "status": "pending"
  }
}
```

---

### 6️⃣ test_search - 测试搜索

**请求**：
```bash
curl -X POST "http://localhost:8888/gying/{hash}" \
  -H "Content-Type: application/json" \
  -d '{"action": "test_search", "keyword": "遮天"}'
```

**成功响应**：
```json
{
  "success": true,
  "message": "找到 5 条结果",
  "data": {
    "keyword": "遮天",
    "total_results": 5,
    "results": [
      {
        "title": "遮天：禁区",
        "links": [
          {
            "type": "quark",
            "url": "https://pan.quark.cn/s/89f7aeef9681",
            "password": ""
          }
        ]
      }
    ]
  }
}
```

---

## 🔍 当前实现补充

下面这些是当前 `gying.go` 已经实现、但原始 README 没写全的部分：

### 1. 反爬挑战处理

所有关键请求都会经过 `requestWithChallengeRetry`。如果检测到站点返回的验证页，例如：

- `浏览器安全验证`
- `安全验证`
- `正在进行浏览器计算验证`
- 旧版 `正在确认你是不是机器人`

插件会进入 `solveBotChallenge` 自动求解后再重试原请求。

当前同时兼容三类验证结构：

- 远程 PoW：验证页只加载 `powSolve-*.js`，并通过 `Set-Cookie: browser_pow=...` 标记挑战；插件请求 `{base_url}/res/pow` 获取 `N/x/t`，计算后 `POST {base_url}/res/pow` 提交 `y`。
- 内嵌 PoW：`const json={id,N,x,t};const jss=...`，插件用 Go 的 `math/big` 直接计算 `y = y*y % N` 共 `t` 次，然后提交 `action=verify&id={id}&y={y}`。
- 旧版哈希枚举：`const json={id,challenge,diff,salt};const jss=...`，插件枚举 `nonce`，匹配 `sha256(strconv.Itoa(nonce)+salt)` 后提交 `nonce[]`。

PoW 求解不依赖 Playwright、Puppeteer、Selenium 或真实浏览器环境；它是对站点 worker 脚本的轻量算法复现。为贴近前端行为，计算完成不足 3 秒时会补齐等待后再提交验证。

验证通过后，站点通常会下发 `browser_verified` 这类验证态 Cookie。当前插件会：

- 在登录阶段导出并保存当前 Cookie 快照
- 在搜索阶段如果拿到新的验证态 Cookie，会自动回写到用户文件
- 在插件重启后优先使用已保存 Cookie 恢复会话，而不是默认先重登

这一步的目的是尽量复用浏览器验证结果，减少重复触发 challenge。

#### 1.1 2026-05 登录验证循环问题

曾出现过一种登录失败场景：日志里先显示 `Challenge验证成功`，但随后重试原请求仍然返回 `浏览器安全验证`，最终报错 `重试后仍然进入机器人验证页`。

排查结论：新版验证态不只依赖 `browser_verified` Cookie，还会受同一次客户端请求指纹影响。此前 `requestWithChallengeRetry` 里 GET 请求直接使用 cloudscraper 内部的 `http.Client.Do`，而验证提交使用 `scraper.Post`；这会让“访问验证页 / 提交验证 / 重试原请求”没有完整经过 cloudscraper 的同一套浏览器化请求处理，导致站点认为重试请求仍然需要验证。

当前修复：

- GET 请求统一使用 `scraper.Get`
- POST 请求继续使用 `scraper.Post`
- challenge 页、验证提交、重试原请求都保持在同一个 cloudscraper 会话与请求链路里
- 调试日志中的登录 POST 数据已对密码脱敏，只输出密码长度

验证方式：

1. 使用错误密码登录，应能通过 challenge 并返回站点的账号密码错误 JSON，而不是停在机器人验证页。
2. 使用正确密码登录，应返回 `登录成功`，并在 Cookie 快照中包含 `PHPSESSID`、`app_auth`、`browser_verified`。
3. 调用 `test_search` 搜索关键词，应能访问搜索页和详情接口并返回网盘链接。

#### 1.2 2026-05 新版 PoW 参数变更

新版验证页不再只返回 `challenge/diff/salt`，而是返回类似：

```javascript
const json={"id":"...","N":"...","x":"...","t":800000};const jss={...};
```

对应的 worker 逻辑等价于：

```text
N = BigInt("0x" + json.N)
y = BigInt("0x" + json.x)
repeat json.t times:
    y = (y * y) % N
```

验证提交表单为：

```text
action=verify&id={id}&y={hex(y)}
```

此前代码只识别旧版 `challenge/diff/salt`，会在登录或搜索进入新版验证页时报 `验证数据无效`。当前实现已在 `solveBotChallenge` 中自动分流新版 PoW 和旧版哈希枚举。

因为新版验证会增加登录耗时，配套前端 `pansou-web` 也把 Gying 登录 / 测试搜索请求超时放宽，并在登录请求超时后回查一次状态，避免后端已登录成功但前端误报“登录失败”。

#### 1.3 2026-05 远程 PoW 流程

站点后来又调整为“页面不再内嵌 `const json`”的流程。验证页只包含：

```javascript
const jss={"worker":"//static.filejin.ru/static/file/js/pow.worker-...js"};
```

同时响应头下发：

```text
Set-Cookie: browser_pow={challenge_id}; Max-Age=300; HttpOnly; SameSite=Lax
```

前端脚本的真实流程是：

1. `GET /res/pow` 获取 `N/x/t`
2. 本地计算 `y = y*y % N` 共 `t` 次
3. `POST /res/pow`，表单为 `y={hex(y)}`
4. 成功后服务端返回 `{"success":true,"challenge_id":"..."}` 并下发 `browser_verified`
5. 原请求重新加载

当前插件在识别到这类验证页时，会自动走 `solveRemotePowChallenge`，用同一个 `cloudscraper` 会话完成 `/res/pow` 获取、计算、提交，再重试搜索或详情请求。

### 2. 搜索链路不是单接口

当前搜索逻辑不是直接调一个公开 API，而是：

1. 访问搜索页 `/{base_url}/search?q={keyword}&type=0&mode=2`
2. 从 HTML 中提取 `_obj.search = {...}` 内嵌 JSON
3. 再并发请求详情接口 `/{base_url}/res/downurl/{type}/{id}`
4. 从详情里提取网盘链接和磁力链接

### 2.1 代理前提

当前目标站点在不同网络出口下行为差异很大。

实测中，如果直连当前 `gying` 域名，搜索入口有时不会进入 challenge，而是直接返回一个由 `Angie` 输出的 `404 Not Found` 假页；这类 404 不是插件参数错误，而是站点对当前网络出口的拒绝或伪装响应。

因此，`gying` 在需要海外网络时，必须确保插件自己的请求链也走代理。

当前实现会显式复用主程序的全局 `PROXY` 配置，并应用到 `cloudscraper` 内部 transport，包括：

- 启动时使用已保存 Cookie 恢复会话
- 用户名密码登录
- 搜索页请求
- 详情接口请求

例如：

```bash
PROXY=socks5://127.0.0.1:7897 ENABLED_PLUGINS=gying go run .
```

### 3. 自动重新登录

如果搜索页或详情接口返回下列任一失效信号，插件会尝试使用已保存的加密密码重新登录，然后用新的会话重试搜索：

- HTTP `403`
- `_BT.PC.HTML('login')`
- `_BT.PC.HTML('nologin')`
- 页面标题或正文出现“未登录，访问受限”
- 详情 JSON 里的 `code == 403`

### 4. 当前管理页的实际定位

管理页是“一个 hash 对应一个用户槽位”，不是统一多账号后台；但插件搜索时，仍然会把所有 `active` 用户的结果汇总后去重。

---

## 🔧 配置说明

### 站点地址配置（重要）

站点地址相关逻辑对应 `getBaseURL()`、`updateBaseURL()`、`getLoginPageURL()`、`getLoginAPIURL()` 等方法。

配置规则：

- 默认值：`https://www.gying.net`
- 支持通过管理页动态修改
- 自动补全 `https://`
- 只允许 `http://` 或 `https://`
- 不允许包含路径、参数、锚点

建议实际使用顺序：

1. 访问 `/gying/你的用户名`
2. 进入 hash 页面后先配置站点地址
3. 再登录账号
4. 最后再使用 `test_search` 或正式搜索

### Cookie 生命周期

当前 `gying` 的会话至少包含两类状态：

- 登录态 Cookie：例如 `PHPSESSID`、`app_auth`
- 验证态 Cookie：例如 `browser_verified`

两者作用不同：

- 没有登录态，搜索可能直接进入 `nologin`
- 没有验证态，即使已登录，也可能先进入“浏览器安全验证”

当前插件的处理链路是：

1. 登录或重登时保存完整 Cookie 快照
2. 搜索过程中如果站点补发新的验证态 Cookie，会同步回写到 `cache/gying_users/{hash}.json`
3. 插件重启后优先恢复这份 Cookie 快照，以复用验证结果

### 环境变量（可选）

```bash
# 缓存目录（默认 ./cache）
export CACHE_PATH="./cache"

# 目标站点如果需要海外网络，建议显式配置 PROXY；gying 会复用这条代理到自己的 cloudscraper 请求链
export PROXY="socks5://127.0.0.1:7897"

# Hash Salt（推荐自定义，增强安全性）
export GYING_HASH_SALT="your-custom-salt-here"

# 当前主流程的密码加解密未使用这个环境变量；它只影响可选的 Cookie 加密辅助函数
export GYING_ENCRYPTION_KEY="your-32-byte-key-here!!!!!!!!!!"
```

### 代码内配置

在 `gying.go` 中修改：

```go
const (
    MaxConcurrentUsers   = 10    // 最多使用的用户数（搜索时）
    MaxConcurrentDetails = 50    // 最大并发详情请求数
    DebugLog             = false // 调试日志开关
)
```

### 默认账户配置

在 `gying.go` 中修改默认账户：

```go
var DefaultAccounts = []struct {
    Username string
    Password string
}{
    // 可以添加更多默认账户
    // {"user2", "password2"},
}
```

**参数说明**：

| 参数 | 默认值 | 说明 | 建议 |
|------|--------|------|------|
| `MaxConcurrentUsers` | 10 | 单次搜索最多使用的用户数 | 10-20足够 |
| `MaxConcurrentDetails` | 50 | 最大并发详情请求数 | 50-100 |
| `DebugLog` | false | 是否开启调试日志 | 生产环境false |

## 📂 数据存储

### 存储位置

```
cache/gying_users/gying_config.json
cache/gying_users/{hash}.json
```

### 数据结构

**站点配置文件**：

```json
{
  "base_url": "https://your-gying-domain.com",
  "updated_at": "2026-04-03T12:00:00+08:00"
}
```

**用户文件**：

```json
{
  "hash": "abc123...",
  "username": "pansou",
  "encrypted_password": "base64-aes-gcm",
  "cookie": "PHPSESSID=xxx; app_auth=xxx; browser_verified=xxx",
  "status": "active",
  "created_at": "2025-10-28T12:00:00+08:00",
  "login_at": "2025-10-28T12:00:00+08:00",
  "expire_at": "2026-02-26T12:00:00+08:00",
  "last_access_at": "2025-10-28T13:00:00+08:00"
}
```

**字段说明**：
- `hash`: 用户唯一标识（SHA256，不可逆推用户名）
- `username`: 原始用户名（存储）
- `encrypted_password`: 加密后的密码，用于搜索失效或 403 后自动重新登录
- `cookie`: 登录 Cookie 快照，常见值包括 `PHPSESSID`、`app_auth`、`browser_verified`
- `status`: 用户状态（`pending`/`active`/`expired`）
- `expire_at`: Cookie过期时间（121天）
