# KKV (小悠家) HTML结构分析

## 网站信息
- 网站名称: 小悠家
- 域名: kkv.q-23.cn
- 类型: 影视资源搜索（支持多种网盘）

## 1. 搜索页面

### URL格式
```
http://kkv.q-23.cn/?s={keyword}
```

### HTML结构
- 容器: `article.post` (多个article元素)
  - ID格式: `id="post-{id}"` (如 `id="post-72474"`)
  - Class: `post-{id} post type-post status-publish format-standard hentry category-{category}`
  
- 每个搜索结果包含:
  - **标题**: `.entry-header h2.entry-title a`
    - href: `http://kkv.q-23.cn/?p={id}`
    - text: 影片标题
  - **发布时间**: `.entry-meta time.entry-date`
    - datetime属性: ISO格式时间
  - **更新时间**: `.entry-meta time.entry-modified-date.updated`
    - datetime属性: ISO格式时间
  - **简介**: `.entry-summary` 或 `.entry-summary p`

### 提取信息
- 影片ID: 从href提取 `?p=(\d+)`
- 影片标题: 从 `h2.entry-title a` 获取
- 更新时间: 从 `time.updated` 的datetime属性获取

## 2. 详情页面

### URL格式
```
http://kkv.q-23.cn/?p={id}
```

### HTML结构

#### 基本信息
- 标题: `.entry-header h1.entry-title`
- 发布时间: `.entry-meta time.entry-date` (datetime属性)
- 更新时间: `.entry-meta time.updated` (datetime属性)
- 分类: `.entry-meta .categories-links a`

#### 内容信息
- 详细信息: `.entry-content p` (第一个p标签)
  - 包含导演、编剧、主演等信息
- 剧情简介: `.entry-content #link-report span`

#### 网盘链接区域
网盘链接在 `.entry-content` 中，位于 `<hr/>` 标签之后的区域

**链接格式示例**:

1. **迅雷云盘**:
```html
<p>
    视频：<a href="https://pan.xunlei.com/s/VOeeCkzFwv09p0ERN-vV4vQ1A1?pwd=f26g#">迅雷云盘</a>
</p>
```

2. **百度网盘**:
```html
<p>
    视频：<a href="https://pan.baidu.com/s/1NWbakSbG1rLZnM9x2KrSZA?pwd=1234">百度网盘</a>
    提取码：1234
</p>
```

3. **其他网盘** (推测可能的格式):
```html
<p>
    视频：<a href="https://pan.quark.cn/s/xxx">夸克网盘</a>
</p>
<p>
    视频：<a href="https://www.alipan.com/s/xxx">阿里云盘</a>
</p>
```

### 网盘链接提取规则

1. **查找策略**: 遍历 `.entry-content` 下的所有 `<p>` 标签
2. **匹配规则**: 
   - 查找包含 `<a>` 标签的段落
   - 检查链接href是否包含网盘域名特征
3. **密码提取**:
   - 优先从URL的 `?pwd=` 参数提取
   - 如果URL中没有，查找文本中的"提取码："、"密码："等关键词后面的内容
   - 密码通常是4位字母或数字

## 3. 支持的网盘类型

根据插件开发指南，需要识别以下网盘类型：

| 网盘名称 | 类型标识 | 域名特征 |
|---------|---------|----------|
| 夸克网盘 | `quark` | `pan.quark.cn` |
| UC网盘 | `uc` | `drive.uc.cn` |
| 百度网盘 | `baidu` | `pan.baidu.com` |
| 阿里云盘 | `aliyun` | `aliyundrive.com`, `alipan.com` |
| 迅雷网盘 | `xunlei` | `pan.xunlei.com` |
| 天翼云盘 | `tianyi` | `cloud.189.cn` |
| 115网盘 | `115` | `115.com`, `anxia.com` |
| 123网盘 | `123` | `123pan.com`, `123684.com` 等 |
| 移动云盘 | `mobile` | `caiyun.139.com` |
| PikPak | `pikpak` | `mypikpak.com` |

## 4. 插件实现要点

### 搜索流程
1. 构造搜索URL: `http://kkv.q-23.cn/?s={URLEncode(keyword)}`
2. 发送GET请求，解析HTML
3. 提取所有 `article.post` 元素
4. 对每个结果提取：
   - 影片ID (从 `?p=` 参数)
   - 影片标题
   - 详情页URL

### 详情页处理
1. 请求详情页
2. 提取标题、更新时间、剧情简介
3. 在 `.entry-content` 中查找所有包含网盘链接的段落
4. 对每个链接：
   - 识别网盘类型
   - 提取URL
   - 提取密码（URL参数或文本）

### 密码提取策略
```go
// 1. 从URL参数提取
pwd := url.Query().Get("pwd")

// 2. 从文本中提取
patterns := []string{
    `提取码[：:]\s*([a-zA-Z0-9]{4})`,
    `密码[：:]\s*([a-zA-Z0-9]{4})`,
    `pwd[：:]\s*([a-zA-Z0-9]{4})`,
}

// 3. 密码验证（必须是4位）
if len(pwd) == 4 {
    return pwd
}
```

### 更新时间提取
```go
// 从datetime属性提取
timeStr := doc.Find("time.updated").AttrOr("datetime", "")
// 格式: 2025-12-06T20:26:57+08:00
t, _ := time.Parse(time.RFC3339, timeStr)
```

## 5. 特殊处理

### 并发控制
- 详情页并发数: 3-5个
- 请求超时: 30秒

### 错误处理
- 网络请求失败 → 重试3次
- HTML解析失败 → 跳过该项
- 未找到网盘链接 → 跳过该影片
- 密码提取失败 → 密码字段留空

### 结果去重
- UniqueID格式: `kkv-{影片ID}`
- 同一影片包含所有找到的网盘链接

## 6. SearchResult结构

```go
SearchResult{
    UniqueID: "kkv-30027",
    Title:    "[凡人修仙传][更新至172集][动画]",
    Content:  "导演: 伍镇焯 / 王裕仁 编剧: 忘语...",
    Links: []Link{
        {Type: "xunlei", URL: "https://pan.xunlei.com/s/xxx", Password: "f26g"},
        {Type: "baidu", URL: "https://pan.baidu.com/s/xxx", Password: "1234"},
    },
    Channel:  "",
    Datetime: time.Parse(...),
}
```

## 7. 优先级设置

建议设置为优先级3（标准网盘搜索插件）

## 8. 请求头设置

```
User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36
Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8
Accept-Language: zh-CN,zh;q=0.9,en;q=0.8
Referer: http://kkv.q-23.cn/
```
