# 小白搜盘

小白搜盘是专为飞牛 fnOS 打造的资源聚合搜索应用。输入影视、软件或学习资料名称，即可同时查询多个搜索来源，不必在不同网站之间反复切换。搜索结果会按百度网盘、阿里云盘、夸克网盘、天翼云盘、UC网盘、115、迅雷云盘、123云盘、磁力等类型自动分类，方便快速筛选和使用。

| 项目 | 说明 |
| --- | --- |
| 显示名 | 小白搜盘 |
| 包名 | `fn-seekbox` |
| 当前版本 | 1.0.3 |
| 平台 | x86（amd64）/ ARM（arm64） |
| 访问方式 | fnOS 统一网关（需登录） |
| 维护者 | `kidoneself` |
| 仓库 | https://github.com/kidoneself/fn-seekbox |

---

## 特性

- **fnOS 原生应用**：以 FPK 形式安装，无需 Docker 容器
- **多来源聚合**：内置 26 个可配置搜索插件，并提供网络连通与试搜检测
- **分类展示**：按网盘类型汇总搜索结果，支持 JSON / TXT 导出
- **响应式界面**：分别适配 fnOS 应用窗口与独立浏览器访问
- **统一鉴权**：通过 fnOS 统一网关复用 NAS 登录状态，内部后端仅监听 `127.0.0.1`，不向局域网暴露 Web 端口
- **权限隔离**：以独立应用用户运行，不使用 root 权限启动服务

---

## 系统要求

- 飞牛 fnOS **1.1.8** 及以上
- **x86 / amd64** 或 **ARM / arm64** NAS
- 可访问外网的插件站点（可选配置 HTTP/SOCKS5 代理）

---

## 安装

### 方式一：安装 FPK（推荐）

1. 根据 NAS 架构下载 `fn-seekbox-1.0.3-x86.fpk` 或 `fn-seekbox-1.0.3-arm.fpk`
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

需要本机 **Go 1.24+**，或已启动的 **Docker**。

### 改网关 / 生命周期

- Python 网关：`app/bin/gateway.py`（Unix Socket + 静态页 + API 反代）
- 生命周期脚本：`cmd/main`

---

## 打包 FPK

本机需安装 [fnpack](https://static2.fnnas.com/fnpack/)。项目自带 `pack.sh`，会在临时目录里只复制打包所需文件，**不会**把 `backend/`、`frontend/` 源码打进包。

```bash
# 若改过后端
./scripts/build-seekbox.sh

# 若改过前端
cd frontend && npm run build && cp -R dist/* ../app/web/ && cd ..

# 打包（提交飞牛用这个 .fpk）
./pack.sh
# 输出：fn-seekbox-1.0.3.fpk
```

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
│   └── build-seekbox.sh  # backend/pansou → app/bin/seekbox
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
