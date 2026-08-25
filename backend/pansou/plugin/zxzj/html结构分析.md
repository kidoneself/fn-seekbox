# 在线之家（zxzjhd.com）HTML结构分析

## 网站信息

- **站点名称**：在线之家
- **主域名**：`https://www.zxzjhd.com`
- **搜索入口**：`https://www.zxzjhd.com/vodsearch/-------------.html?wd={关键词}&submit=`
- **详情页格式**：`https://www.zxzjhd.com/detail/{ID}.html`
- **播放页格式**：`https://www.zxzjhd.com/video/{ID}-{线路ID}-{序号}.html`
- **资源特征**：站内所有下载入口均聚合到“百度网盘”线路，播放页中的 `player_aaaa` 对象给出真实的网盘链接。

## 搜索结果页面结构

搜索结果页主体位于 `.stui-pannel .stui-vodlist` 内部，每个条目对应一个 `li.col-md-6.col-sm-4.col-xs-3`。

```html
<ul class="stui-vodlist clearfix">
  <li class="col-md-6 col-sm-4 col-xs-3">
    <div class="stui-vodlist__box">
      <a class="stui-vodlist__thumb lazyload" href="/detail/4572.html"
         title="名侦探柯南：独眼的残像"
         data-original="https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2922540490.jpg">
        <span class="play hidden-xs"></span>
        <span class="pic-text text-right">已完结</span>
      </a>
      <div class="stui-vodlist__detail">
        <h4 class="title text-overflow">
          <a href="/detail/4572.html">名侦探柯南：独眼的残像</a>
        </h4>
      </div>
    </div>
  </li>
</ul>
```

需要采集的字段：

- **详情页链接**：`.stui-vodlist__thumb` 的 `href`
- **唯一ID**：从 `/detail/{id}.html` 中截取 `{id}`
- **标题**：`.stui-vodlist__detail h4 a` 文本
- **状态/清晰度**：`.stui-vodlist__thumb .pic-text` 文本
- **封面**：`data-original` 或 `src` 属性

## 详情页结构

详情页主体位于 `.stui-content` 中，包含影片基础信息、简介以及多个播放线路。

```html
<div class="stui-content">
  <div class="stui-content__thumb">
    <img class="lazyload" data-original="https://img1.doubanio.com/...">
  </div>
  <div class="stui-content__detail">
    <h1 class="title">名侦探柯南：独眼的残像</h1>
    <p class="data">类型：剧情,动画,悬疑,犯罪 / 地区：日本 / 年份：2025</p>
    <p class="data">主演：高山南,山崎和佳奈,小山力也...</p>
    <p class="data">导演：重原克也</p>
    <p class="data">更新：2025-12-11 12:12:14</p>
    <p class="desc detail">
      <span class="detail-content">“我想起来了……”沉睡的记忆...</span>
    </p>
  </div>
</div>
```

采集重点：

- **标题**：`.stui-content__detail h1.title`
- **封面**：`.stui-content__thumb img[data-original]`
- **类型/地区/年份**、**主演**、**导演**、**更新时间**：`p.data` 文本
- **简介**：`.desc .detail-content` 或 `.detail-sketch` 文本

### 网盘播放列表

每个播放线路由一个 `div.stui-vodlist__head` 与紧随其后的 `ul.stui-content__playlist` 组成。百度网盘线路的标题文字固定为“百度网盘”。

```html
<div class="stui-vodlist__head">
  <h3>百度网盘</h3>
</div>
<ul class="stui-content__playlist clearfix">
  <li><a href="/video/4572-2-1.html">1080P</a></li>
</ul>
```

解析逻辑：

1. 遍历所有 `.stui-vodlist__head`，筛选文本包含“百度”或“网盘”的块。
2. 找到其后第一个 `ul.stui-content__playlist`。
3. 列表中的 `<a>` 提供播放页地址 `/video/{id}-{sid}-{nid}.html` 以及清晰度/集数文本，用于区分 `work_title`。

## 播放页结构（真实网盘链接）

播放页会注入一个 `player_aaaa` 对象，携带真实的网盘地址、线路信息以及影片元数据。

```html
<script type="text/javascript">
var player_aaaa={
  "flag":"play",
  "encrypt":3,
  "link":"/video/4572-1-1.html",
  "url":"https:\/\/pan.baidu.com\/s\/18j_Sf7RJ9qx934WzWTAchw?pwd=zxzj",
  "from":"yunpan",
  "note":"",
  "id":"4572",
  "sid":2,
  "nid":1,
  "vod_data":{"vod_name":"名侦探柯南：独眼的残像","vod_actor":"..."}
}
</script>
```

解析重点：

- 使用正则匹配 `var player_aaaa = {...}`，并替换 `\/` 转义后再解析 JSON。
- **真实网盘链接**：`player_aaaa.url`，当 `encrypt` 字段为 2 或 3 时需要尝试 base64 解码。
- **网盘平台**：`player_aaaa.from`（此处为 `yunpan`，需根据 URL 再次判断，实际链接为百度）。
- **集数信息**：`player_aaaa.nid` 或页面上的 `vod_part` 脚本，可与播放列表文本组合为 `work_title`。
- **密码**：百度链接通常自带 `?pwd=xxxx`，解析查询参数即可得到提取码。

## 提取流程总结

1. 构造搜索 URL，请求搜索页并解析 `.stui-vodlist` 列表，得到每个结果的 `detail/{id}.html`。
2. 请求详情页，提取基础信息和“百度网盘”线路下所有 `/video/{...}.html` 播放地址。
3. 逐个访问播放页，解析 `player_aaaa`，得到真实的百度网盘链接及密码。
4. 根据播放列表文本（如 `1080P`、`第01集`）生成 `work_title`，所有链接类型强制识别为 `baidu`。
5. 聚合后输出 `model.SearchResult`，其中：
   - `UniqueID` 可使用 `zxzj-{detailID}`
   - `Datetime` 使用详情页的“更新”时间
   - `Content` 组合类型/主演/简介等信息
   - `Links` 仅包含百度网盘地址及提取码

通过上述步骤，即可从在线之家稳定提取百度网盘资源，满足插件“仅输出百度网盘”的要求。
