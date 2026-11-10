# 小白搜盘

小白搜盘是专为飞牛 fnOS 打造的资源聚合搜索应用。输入影视、软件或学习资料名称，即可同时查询多个搜索来源，不必在不同网站之间反复切换。搜索结果会按百度网盘、阿里云盘、夸克网盘、天翼云盘、UC网盘、115、迅雷云盘、123云盘、磁力等类型自动分类，方便快速筛选和使用。

| 项目 | 说明 |
| --- | --- |
| 显示名 | 小白搜盘 |
| 包名 | `fn-seekbox` |
| 当前版本 | 1.5.17 |
| 平台 | x86（amd64）/ ARM（arm64） |
| 访问方式 | fnOS 统一网关（需登录） |
| 维护者 | `kidoneself` |
| 仓库 | https://github.com/kidoneself/fn-seekbox |

---

## 特性

- **fnOS 原生应用**：以 FPK 形式安装，无需 Docker 容器
- **多来源聚合**：内置 43 个可配置搜索插件，并提供网络连通与试搜检测
- **分类展示**：按网盘类型汇总搜索结果，支持 JSON / TXT 导出
- **高效操作**：搜索历史、结果一键复制、直接打开链接、JSON / TXT 批量导出
- **网盘转存与追更**：百度、夸克、迅雷结果支持一次转存或持续追更，提供账号与任务管理；接入方式、当前限制和验证范围见 [使用说明](docs/cloud-transfer-and-follow.md)
- **首页账号配置**：统一顶部导航进入完整网盘页面，连接百度、夸克、迅雷；首页账号区下方横向展示项目入口、公众号和裁切后的赞赏码
- **响应式界面**：小窗口自动精简次要信息，浏览器宽屏充分利用横向空间
- **统一鉴权**：通过 fnOS 统一网关复用 NAS 登录状态，内部后端仅监听 `127.0.0.1`，不向局域网暴露 Web 端口
- **权限隔离**：以独立应用用户运行，不使用 root 权限启动服务

---

## 系统要求

- 飞牛 fnOS **1.1.8** 及以上
- **x86 / amd64** 或 **ARM / arm64** NAS
- 应用中心安装 Node.js v22（安装包声明依赖）；1.6.3 默认由远端扫码中心处理迅雷登录，百度/夸克在本地处理
- 可访问外网的插件站点（可选配置 HTTP/SOCKS5 代理）

---

## 安装

### 方式一：安装 FPK（推荐）

1. 准备对应 NAS 架构的 FPK：x86 为 `fn-seekbox-1.5.17-x86.fpk`；ARM 可按下方构建步骤生成 `fn-seekbox-1.5.17-arm.fpk`
2. 打开 **应用中心** → **手动安装**，选择 FPK 文件
3. 安装向导里按需填写出站代理；不需要代理可留空
4. 安装完成后，在桌面点击 **小白搜盘** 图标

### 方式二：源码目录本地安装（开发调试）

将项目目录上传到 NAS 后：

```bash
cd /path/to/fn-seekbox
chmod +x cmd/* app/bin/seekbox app/bin/gateway.py

# 可选：用 config.env 跳过向导
sudo appcenter-cli install-local --env config.env
```

访问：登录 fnOS 后从桌面打开，或访问系统域名下的 `/app/fn-seekbox`。

---

## 使用说明

1. 打开应用，输入关键词搜索
2. 点击顶栏 **来源**，勾选要使用的搜索插件
3. 绿点 / 黄点 / 红点表示插件连通与试搜状态；可点 **重新检测**
4. **检测** 页签可开关链接有效性检测（默认关闭，开启后自动检测可见结果）
5. 搜索结果按网盘类型分组，可复制链接或导出

---

## 开发与构建

### 改前端界面

前端源码在 `frontend/`（Vue 3 + Vite + Tailwind）。

```bash
cd frontend
npm install
npm run dev          # 本地预览
npm run build        # 构建
cp -R dist/* ../app/web/
```

改完 `app/web/` 后，在 NAS 上重启应用使静态页生效：

```bash
sudo appcenter-cli stop fn-seekbox
sudo appcenter-cli start fn-seekbox
```

### 改搜索后端（Go 源码）

源码在 **`backend/pansou/`**（日常改这里）。交飞牛的是编译后的 **`app/bin/seekbox`** 二进制，FPK 里不带 Go 源码。

详情见 [`backend/README.md`](backend/README.md)。

```bash
# 改插件优先级 / 搜索逻辑后，重新编译 linux/amd64 二进制
./scripts/build-seekbox.sh
# → 覆盖 app/bin/seekbox
```

需要本机 **Go（默认自动使用 1.24.9 工具链）**，或已启动的 **Docker**。ARM64 使用 `GOARCH=arm64 ./scripts/build-seekbox.sh`，输出到 `build/seekbox-linux-arm64`。

### 改网关 / 生命周期

- Python 网关：`app/bin/gateway.py`（Unix Socket + 静态页 + API 反代）
- 生命周期脚本：`cmd/main`

---

## 打包 FPK

本机需安装 [fnpack](https://static2.fnnas.com/fnpack/)。项目自带 `pack.sh`，会在临时目录里只复制打包所需文件，**不会**把 `backend/`、`frontend/` 源码打进包。

扫码运行库使用公开的 `playwright-core@1.57.0`。1.6.3 默认不打包 Chromium：迅雷向远端扫码中心请求二维码和短期凭据，百度/夸克保持 NAS 本地 HTTP 登录；内部服务密钥不会进入安装包。接入说明见 [扫码中心集成](docs/scan-center-integration.md)。

首次运行 `./scripts/prepare-qr-runtime.sh` 准备本地扫码依赖。两种架构都可用同一份 JavaScript 运行库，后端二进制必须匹配 NAS 架构。构建两个架构需要 Node.js/npm、Go、rsync、Python 3、sha256sum 和 fnpack：

```bash
./scripts/build-all.sh
# fnpack 不在 PATH 时：FNPACK=/path/to/fnpack ./scripts/build-all.sh
# 输出：fn-seekbox-1.6.3-x86.fpk、fn-seekbox-1.6.3-arm.fpk
# 校验文件：build/fn-seekbox-1.6.3-SHA256SUMS.txt
```

本次产物分别为 11,439,132 / 9,816,452 字节；已检查版本、静态资源、后端 ELF 架构、发布公钥与无浏览器内容，并验证 x86 包内后端健康启动。ARM64 未在飞牛实机验证。实际验收记录见 `build/package-verification-1.6.3.json`。

单独构建 x86：

```bash
./scripts/build-seekbox.sh
(cd frontend && npm run build)
cp -R frontend/dist/. app/web/
./pack.sh
```

ARM64 后端使用 `GOARCH=arm64 ./scripts/build-seekbox.sh`，然后 `PLATFORM=arm ./pack.sh`。

需要本地迅雷浏览器时显式设置 `SEEKBOX_INCLUDE_LOCAL_BROWSER=1`，再准备运行库并打包；ARM 使用 `QR_PLATFORM=arm QR_CHROMIUM_DIR="$PWD/build/chromium-arm"` 准备对应浏览器，打包时同样指定该目录。运行时 `SEEKBOX_REMOTE_XUNLEI=0` 才使用本地迅雷流程；轻量包没有浏览器，不能只改运行开关就启用本地迅雷。

---

## 目录结构

```
fn-seekbox/
├── manifest              # 应用元数据（名称、版本、changelog）
├── ICON.PNG / ICON_256.PNG
├── pack.sh               # 干净目录打包 → FPK（交飞牛）
├── config.env
├── backend/
│   ├── README.md         # 后端开发说明
│   └── pansou/           # Go 源码（日常改这里，不打进 FPK）
├── app/
│   ├── bin/
│   │   ├── seekbox       # 后端编译产物（打进 FPK）
│   │   └── gateway.py    # Web 网关
│   ├── web/              # 前端构建产物（打进 FPK）
│   └── ui/
├── frontend/             # Vue 源码（不打进 FPK）
├── scripts/
│   ├── build-seekbox.sh  # 编译指定架构后端
│   └── build-all.sh      # 前端 + x86 / ARM64 后端及 FPK
├── cmd/
├── config/
└── wizard/
```

---

## 配置

安装向导可配置：

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `PROXY` | 出站代理（可选） | 空 |

应用入口注册为 `/app/fn-seekbox`，由 fnOS 校验用户会话后转发至 `/var/apps/fn-seekbox/target/app.sock`。网关转发的 `X-Trim-Userid` 等用户 Header 会继续传递给内部后端。

应用数据目录（fnOS 典型路径）：

- 程序：`/vol1/@appcenter/fn-seekbox/`
- 数据：`/vol1/@appdata/fn-seekbox/`
- 后端日志：`.../data/logs/backend/seekbox.log`
- 网关日志：`.../data/logs/gateway.log`
- 应用日志：`.../info.log`

---

## 常见问题

**装完一直「启动中」或桌面入口打不开**

- 检查 `/var/apps/fn-seekbox/target/app.sock` 是否已创建
- 查看 `info.log` 与 `seekbox.log` 是否有启动超时或权限错误

**搜不到结果**

- 打开 **来源**，确认插件已勾选且检测不是全红
- NAS 需能访问对应插件站点；内网受限时可配置 `PROXY`

**卸载后还有数据**

- 卸载向导若选了「保留数据」，缓存与日志会留在 `@appdata/fn-seekbox`

---

## 免责声明

本项目仅供学习研究使用，请勿用于任何盈利或违法用途。搜索结果来自第三方插件站点，请遵守当地法律法规与网盘服务条款。

---

## 链接

- 项目仓库：https://github.com/kidoneself/fn-seekbox
- 飞牛开发者文档：https://developer.fnnas.com
