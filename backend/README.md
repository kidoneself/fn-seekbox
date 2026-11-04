# 后端源码（pansou → seekbox）

日常改搜索逻辑、插件优先级、排序规则，都在这里改。

| 角色 | 路径 |
| --- | --- |
| **开发源码** | `backend/pansou/`（基于 [fish2018/pansou](https://github.com/fish2018/pansou)） |
| **编译产物** | `app/bin/seekbox`（linux/amd64 静态二进制） |
| **ARM 编译产物** | `build/seekbox-linux-arm64`（linux/arm64 静态二进制） |
| **交飞牛** | `./pack.sh` 打出的 `.fpk`（只含二进制 + 前端产物，不含 Go 源码） |

## 本仓库相对上游的改动

- **插件-only**：已移除 Telegram 搜索与应用自建 JWT；仅保留 43 个启用插件。用户登录鉴权由 fnOS 统一网关负责。
- `plugin/ikantv/ikantv.go`：`priority` **3 → 1**（等级 1 = 1000 分，搜索结果优先）
- 1.3.0 新增来源及直连实测记录：[来源筛选记录](../docs/sources-1.3.0.md)。
- Health：`channels: []`、`channels_count: 0`、`auth_enabled: false`

以后改插件分数、加插件、修爬虫，都直接改 `backend/pansou/` 下对应文件。

## 编译

需要本机 **Go（默认自动使用 1.24.9 工具链）**，或 **Docker**（`golang:1.24.9-bookworm`）。

```bash
./scripts/build-seekbox.sh
# 输出覆盖：app/bin/seekbox

GOARCH=arm64 ./scripts/build-seekbox.sh
# 输出：build/seekbox-linux-arm64
```

环境变量（可选）：

| 变量 | 说明 |
| --- | --- |
| `PANSOU_SRC` | 源码目录，默认 `backend/pansou` |
| `GOARCH` | `amd64`（默认）或 `arm64` |
| `SEEKBOX_OUT` | 覆盖默认输出路径；相对路径基于项目根目录 |
| `GOTOOLCHAIN` | 默认 `go1.24.9`，用于本机 Go 构建 |
| `GO_IMAGE` | Docker 镜像，默认 `golang:1.24.9-bookworm` |

## 和 FPK 的关系

```
改 backend/pansou/*.go
        ↓
./scripts/build-seekbox.sh   →  app/bin/seekbox
        ↓
（可选）改 frontend → npm run build → app/web/
        ↓
./pack.sh                    →  fn-seekbox-x.y.z-x86.fpk  ← 提交飞牛应用中心
```

`pack.sh` 只打包 `app/`、`cmd/`、`config/`、`wizard/`、`manifest`，**不会**把 `backend/` 打进 FPK。

ARM64 后端编译完成后，运行 `PLATFORM=arm ./pack.sh` 生成 `fn-seekbox-x.y.z-arm.fpk`。打包入口会检查实际 ELF 架构；`SEEKBOX_BIN` 可覆盖后端路径。

运行 `./scripts/build-all.sh` 可依次构建前端、两个架构的后端和 FPK，并生成 `build/fn-seekbox-x.y.z-SHA256SUMS.txt`。1.3.5 的 x86 原生与 ARM64 QEMU 启动、真实搜索及网关检查已通过；ARM 飞牛实机安装仍待验证。
