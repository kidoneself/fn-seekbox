# Gying HTML / 数据结构分析

## 说明

本文档以当前 `gying.go` 的实现为准，不再沿用旧版“只靠 Cookie 直接请求”或“统一多账号控制台”的描述。这里关注两件事：

1. 目标站点 `gying` 的页面 / JSON 结构
2. 插件内置管理页的 HTML 结构与前端行为

## 0. 域名 / baseURL 是当前实现前提

先明确一点：当前插件不是把站点死写成一个固定域名后直接抓取。

真实实现是：

- 代码里有默认值 `https://www.gying.net`
- 但运行时允许通过管理页把目标站点改成自定义域名
- 这个域名会保存到 `gying_config.json`
- 后续登录、搜索、详情、预热请求全部都从这个 `baseURL` 派生

也就是说，`gying` 的实际运行前提是“先确定站点地址”，不是“永远只请求一个固定域名”。

## 1. 目标站点基础入口

当前插件把站点地址抽象成可配置的 `baseURL`，默认值是：

```text
https://www.gying.net
```

主要入口如下：

| 功能 | URL |
| --- | --- |
| 登录页 / 初始会话页 | `{baseURL}` |
| 登录接口 | `{baseURL}/user/login` |
| 搜索页 | `{baseURL}/search?q={keyword}&type=0&mode=2` |
| 详情接口 | `{baseURL}/res/downurl/{type}/{id}` |
| 预热详情页 | `{baseURL}/mv/wkMn` |

`baseURL` 在保存前会经过标准化：

- 自动补全协议
- 只允许纯域名，不允许路径、查询串、锚点

代码上直接体现这一点的方法有：

- `getBaseURL()`
- `getLoginPageURL()`
- `getLoginAPIURL()`
- `getWarmupDetailURL()`
- `updateBaseURL()`

搜索与详情请求也不是写死域名，而是分别拼接为：

```text
{baseURL}/search?q={keyword}&type=0&mode=2
{baseURL}/res/downurl/{type}/{id}
```

管理页前端对应的配置动作是：

- `get_config`
- `update_config`

## 2. 反爬挑战页结构

### 识别方式

插件会把“页面中存在如下 JS 片段”且正文包含任一验证文案的响应视为挑战页：

- `正在确认你是不是机器人`
- `浏览器安全验证`
- `安全验证`
- `正在进行浏览器计算验证`

挑战页可能出现如下 JS 片段：

```javascript
const json={...};const jss=
```

也可能只出现远程 PoW 脚本配置：

```javascript
const jss={"worker":"//static.filejin.ru/static/file/js/pow.worker-...js"};
```

后一种流程会通过响应头下发 `browser_pow`，再由前端请求 `/res/pow` 获取真实挑战数据。

当前用于提取挑战数据的正则：

```go
challengeJSONPattern = regexp.MustCompile(`(?s)const\s+json\s*=\s*(\{.*?\})\s*;\s*const\s+jss\s*=`)
```

### 挑战数据结构

挑战 JSON 会被解析成：

```go
type ChallengePageData struct {
    ID        string   `json:"id"`
    Challenge []string `json:"challenge"`
    Diff      int      `json:"diff"`
    Salt      string   `json:"salt"`
    N         string   `json:"N"`
    X         string   `json:"x"`
    T         int      `json:"t"`
}
```

当前兼容三套验证流程：远程 PoW、内嵌 PoW、旧版哈希枚举。

旧版哈希枚举字段含义：

- `id`：本次挑战标识
- `challenge`：目标哈希列表
- `diff`：枚举上限
- `salt`：参与哈希运算的盐值

PoW 字段含义：

- `id`：本次挑战标识
- `N`：十六进制大整数模数
- `x`：十六进制初始值
- `t`：平方取模迭代次数

远程 PoW 的 `id` 不在 JSON 中返回，而是由 `browser_pow` Cookie 标记；验证成功后服务端返回 `challenge_id` 并下发 `browser_verified`。

### 插件求解方式：远程 PoW

当前站点可能只在验证页中加载 `powSolve-*.js`，真实挑战数据需要通过接口获取：

```text
GET {baseURL}/res/pow
```

返回 JSON 形如：

```json
{"N":"...","x":"...","t":800000}
```

插件计算完成后提交：

```text
POST {baseURL}/res/pow
Content-Type: application/x-www-form-urlencoded

y={hex(y)}
```

成功响应形如：

```json
{"success":true,"challenge_id":"..."}
```

### 插件求解方式：内嵌 PoW

如果页面直接内嵌 `const json={id,N,x,t}`，worker 的核心逻辑等价于：

```text
N = BigInt("0x" + json.N)
y = BigInt("0x" + json.x)
repeat json.t times:
    y = (y * y) % N
```

当前插件不启动浏览器，也不使用 Playwright / Puppeteer / Selenium，而是在 Go 中用 `math/big` 直接复现上述计算。计算结束后，为贴近前端脚本行为，如果总耗时不足 3 秒，会补齐等待时间。内嵌 PoW 会向当前请求 URL 提交：

```text
action=verify&id={id}&y={hex(y)}
```

验证成功后，服务端会下发 `browser_verified`，原请求再重试一次。

### 插件求解方式：旧版哈希枚举

`solveBotChallenge` 的策略很直接：

1. 从 `0..diff` 顺序枚举 `nonce`
2. 计算：

```text
sha256(strconv.Itoa(nonce) + salt)
```

3. 把命中的 `nonce` 按原顺序回填
4. 向当前请求 URL 提交：

```text
action=verify&id={id}&nonce[]={n1}&nonce[]={n2}...
```

验证成功后，原请求会再重试一次。

## 3. 登录页与登录接口结构

### 第一步：登录页

请求：

```text
GET {baseURL}
```

作用：

- 建立会话
- 收集初始 Cookie
- 如果登录页本身触发挑战，也会先经过挑战求解

### 第二步：登录接口

请求：

```text
POST {baseURL}/user/login
Content-Type: application/x-www-form-urlencoded
```

表单参数固定为：

```text
code=
siteid=1
dosubmit=1
cookietime=10506240
username={用户名}
password={密码}
```

插件当前以 JSON 响应中的 `code == 200` 作为登录成功条件。

### 第三步：预热详情页

请求：

```text
GET {baseURL}/mv/wkMn
```

这一步不是取业务数据，而是为了补齐反爬链路中的额外 Cookie。当前实现里，登录和搜索后都会主动访问一次这个页面。

### 登录失效识别

插件把以下响应都视为登录失效或需重登：

- HTTP 403
- 返回登录壳页面：`_BT.PC.HTML('login')`
- 返回未登录壳页面：`_BT.PC.HTML('nologin')`
- 页面标题或正文出现 `未登录，访问受限`
- 详情 JSON 里的 `code == 403`

### Cookie 生命周期

当前站点至少有两类不同作用的 Cookie：

- 登录态 Cookie：例如 `PHPSESSID`、`app_auth`
- 验证态 Cookie：例如 `browser_verified`

两者不是同一层状态：

- 只有登录态，没有验证态：搜索时仍可能进入“浏览器安全验证”
- 只有验证态，没有有效登录态：搜索可能不再 challenge，但会落到 `nologin`

当前插件的处理链路是：

1. 登录 / 重登成功后，导出整套 Cookie 快照并保存到用户文件
2. 搜索过程中如果 challenge 求解成功，服务端补发的 `browser_verified` 会进入 scraper 的 cookie jar
3. 单次搜索成功后，插件会把 scraper 中最新 Cookie 再次导出并回写到用户文件
4. 插件重启后，优先使用已保存 Cookie 恢复 scraper 会话；只有恢复失败时才回退到用户名密码重新登录

这样做的目的，是尽量复用已经通过的浏览器验证结果，减少不必要的重复 challenge。

### 代理前提

当前目标站点对网络出口非常敏感。

实测里，同一个 `/search` 地址在不同请求链上可能出现两种完全不同的表现：

- 可访问网络出口：先返回 challenge，再返回 `_obj.search`
- 受限网络出口：直接返回由 `Angie` 输出的 `404 Not Found` 假页

也就是说，这类 404 不能直接解释为“搜索参数错误”或“路由不存在”，它很可能只是站点对当前网络出口的伪装拒绝。

为了解决这一点，当前 `gying` 会显式把主程序的 `PROXY` 配置应用到 `cloudscraper` 内部 transport，而不是只依赖库默认行为。

当前代理应用点包括：

1. `doLogin()` 创建 scraper 时
2. `createScraperWithCookies()` 恢复 scraper 时
3. 随后的搜索页、详情页、challenge 提交请求

支持的代理类型与主程序一致：

- `socks5://...`
- `http://...`
- `https://...`

## 4. 搜索页 HTML 结构

### 搜索地址

```text
GET {baseURL}/search?q={url.QueryEscape(keyword)}&type=0&mode=2
```

返回值不是纯 JSON，而是一个 HTML 页面。真正的数据被嵌在 JavaScript 变量里：

```javascript
_obj.search={...};
```

对应正则：

```go
searchDataPattern = regexp.MustCompile(`_obj\.search=(\{.*?\});`)
```

### SearchData 结构

插件当前真正依赖的字段如下：

```go
type SearchData struct {
    Q  string   `json:"q"`
    WD []string `json:"wd"`
    N  string   `json:"n"`
    L  struct {
        Title  []string `json:"title"`
        Year   []int    `json:"year"`
        D      []string `json:"d"`
        I      []string `json:"i"`
        Info   []string `json:"info"`
        Daoyan []string `json:"daoyan"`
        Zhuyan []string `json:"zhuyan"`
    } `json:"l"`
}
```

其中关键字段含义如下：

| 字段 | 说明 | 当前用途 |
| --- | --- | --- |
| `q` | 搜索关键词 | 调试与校验 |
| `n` | 结果数，字符串 | 调试参考 |
| `l.title` | 标题列表 | 构建结果标题 |
| `l.year` | 年份列表 | 标题后缀、标签 |
| `l.d` | 资源类型列表 | 构造详情接口 URL |
| `l.i` | 资源 ID 列表 | 构造详情接口 URL |
| `l.info` | 基础说明 | 生成 `Content` |
| `l.daoyan` | 导演 | 生成 `Content` |
| `l.zhuyan` | 主演 | 生成 `Content` |

### 插件在搜索页上的额外过滤

虽然 `_obj.search` 里可能返回很多资源，但当前实现还做了一层过滤：

- 只有 `title` 中包含搜索关键词的条目，才会继续取详情
- 使用 `strings.Contains(strings.ToLower(title), strings.ToLower(keyword))`

## 5. 详情接口 JSON 结构

### 详情地址

```text
GET {baseURL}/res/downurl/{resourceType}/{resourceID}
```

例如：

```text
{baseURL}/res/downurl/mv/xJe3
```

### DetailData 结构

插件当前依赖的详情结构如下：

```go
type DetailData struct {
    Code     int  `json:"code"`
    WP       bool `json:"wp"`
    Downlist struct {
        IMDB string `json:"imdb"`
        Type struct {
            A []string `json:"a"`
            B []string `json:"b"`
        } `json:"type"`
        Hex  string `json:"hex"`
        List struct {
            M []string      `json:"m"`
            T []string      `json:"t"`
            S []string      `json:"s"`
            E []interface{} `json:"e"`
            P []string      `json:"p"`
            U []string      `json:"u"`
            K []interface{} `json:"k"`
            N []string      `json:"n"`
        } `json:"list"`
    } `json:"downlist"`
    Panlist struct {
        ID    []string `json:"id"`
        Name  []string `json:"name"`
        P     []string `json:"p"`
        URL   []string `json:"url"`
        Type  []int    `json:"type"`
        User  []string `json:"user"`
        Time  []string `json:"time"`
        TName []string `json:"tname"`
    } `json:"panlist"`
}
```

### 插件真正使用的字段

| 字段 | 说明 | 当前用途 |
| --- | --- | --- |
| `code` | 状态码 | `403` 时触发重登 |
| `panlist.url` | 网盘原始链接 | 提取并归一化 |
| `panlist.p` | 提取码 | 作为密码兜底 |
| `panlist.type` | 类型编码 | 识别网盘类型 |
| `panlist.tname` | 类型名称 | 类型识别兜底 |
| `panlist.name` | 链接标题 | 构造 `WorkTitle` |
| `panlist.time` | 更新时间 | 构造链接时间、结果时间 |
| `downlist.list.m` | 磁力 hash | 构造 magnet 链接 |
| `downlist.list.t` | 资源名 | 作为 magnet `dn` |
| `downlist.list.s` | 文件大小 | 资源名兜底 |
| `downlist.list.n` | 更新时间 | 链接时间、结果时间 |

### 结构兼容性说明

当前站点的详情 JSON 并不是所有字段都严格稳定。

实测里，`downlist.list.e` 和 `downlist.list.k` 可能混用：

- `int`
- `string`

例如某些条目会返回：

- `e: [1, 0, 0, 9, 0]`
- `e: ["-1"]`
- `e: [0, 10, "-1", "-1"]`

因此当前实现把这两个字段按宽松类型接收，避免因为单个字段的类型漂移导致整条详情 JSON 反序列化失败。

## 6. SearchResult 映射逻辑

当前 `buildResult` 会把搜索页和详情页数据拼装为：

```go
model.SearchResult{
    UniqueID: fmt.Sprintf("gying-%s-%s", resourceType, resourceID),
    Title:    titleWithYear,
    Content:  "info | 导演: ... | 主演: ...",
    Links:    extractLinks(detail, titleWithYear),
    Tags:     []string{year},
    Channel:  "",
    Datetime: parsedTime,
}
```

具体规则：

- 如果存在年份，标题会变成 `标题（2024）`
- `Content` 由 `info / daoyan / zhuyan` 拼接
- `Datetime` 不是直接取详情单值，而是综合 `panlist.time + downlist.list.n` 里“最接近当前时间”的值

## 7. 相对时间解析规则

`parseRelativeTime` 当前支持：

- `今天`
- `昨天`
- `N天前`
- `N月前`
- `N年前`

结果时间和链接时间都建立在这套解析逻辑上。如果时间文本无法识别，则：

- 链接时间返回零值 `time.Time{}`
- 结果时间回退到 `time.Now()`

## 8. 网盘链接识别与归一化

### 类型识别优先级

`determineLinkType` 的判断顺序是：

1. 先看 URL 域名 / 前缀
2. 再看 `panlist.type` 对应的内置映射
3. 最后用 `panlist.tname` 做兜底

当前映射到的标准类型包括：

- `quark`
- `uc`
- `baidu`
- `aliyun`
- `xunlei`
- `tianyi`
- `mobile`
- `115`
- `123`
- `magnet`

### URL 归一化

`normalizePanURL` 会先删除诸如：

```text
（访问码：xxxx）
```

再按不同类型执行专用正则提取。

当前支持的站点包括：

- 百度网盘
- 夸克网盘
- 阿里云盘 / alipan
- 迅雷网盘
- 天翼云盘 / tianyi.cloud
- 中国移动云盘 / 彩云
- 115
- 123 系列域名
- UC 网盘

### 提取码逻辑

提取码优先从原始文本里提取，支持：

- `?pwd=xxxx`
- `?password=xxxx`
- `访问码: xxxx`
- `提取码: xxxx`
- `密码: xxxx`

如果原始文本里拿不到，再退回 `panlist.p`。

## 9. 磁力链接构造逻辑

磁力链接来自 `downlist.list.m` 的 40 位十六进制 hash。插件不会直接使用现成 URL，而是手动组装：

```text
magnet:?xt=urn:btih:{hash}&dn={资源名}
```

其中资源名优先取 `downlist.list.t`，为空则退回 `downlist.list.s`。

## 10. 去重策略

当前去重分两层：

### 链接级去重

- 网盘：`{type}:{lowercase(url)}`
- 磁力：`magnet:{infoHash}`

### 结果级去重

最终结果以 `UniqueID` 去重：

```text
gying-{resourceType}-{resourceID}
```

## 11. 搜索执行链路

当前完整链路如下：

1. `SearchWithResult`
2. 读取插件内关键词缓存
3. 获取全部活跃用户
4. 为每个用户取 scraper，没有 scraper 时用已保存 Cookie 恢复
5. 请求搜索页；如果遇到浏览器验证页则自动求解后重试
6. 再访问一次预热详情页刷新反爬 Cookie
7. 提取 `_obj.search`
8. 并发拉取详情接口
9. 构造结果和链接
10. 聚合多用户结果并按 `UniqueID` 去重

其中并发限制由两个常量控制：

```go
MaxConcurrentUsers   = 10
MaxConcurrentDetails = 50
```

## 12. 插件内置管理页 HTML 结构

除了目标站点本身，`gying.go` 里还内嵌了一套插件管理页 HTML 模板，结构如下：

### DOM 分区

1. `.header`
   - 标题
   - 当前访问 hash
2. `#site-section`
   - 当前站点地址显示
   - 站点地址输入框
   - 保存按钮
3. `#login-section`
   - `#logged-in-view`
   - `#not-logged-in-view`
   - 登录 / 退出逻辑
4. `#test-section`
   - 搜索输入框
   - `#search-results`
5. API 说明区

### 前端行为

管理页前端通过统一的 `postAction(action, extraData)` 调后台：

- `get_status`
- `get_config`
- `login`
- `logout`
- `update_config`
- `test_search`

启动逻辑：

```javascript
window.onload = function() {
    updateStatus();
    loadConfig();
    startStatusPolling();
};
```

其中 `startStatusPolling()` 每 5 秒调用一次 `updateStatus()`。

### 管理页定位

这个页面的定位不是“多账号总览控制台”，而是：

- 一个 hash 对应一个账号槽位
- 可在该槽位中登录、退出、测搜索
- 后端搜索时再把所有活跃槽位统一聚合

这也是当前 README 和旧分析文档最需要修正的地方。
