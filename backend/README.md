# 后端源码（pansou → seekbox）

日常改搜索逻辑、插件优先级、排序规则，都在这里改。

| 角色 | 路径 |
| --- | --- |
| **开发源码** | `backend/pansou/`（基于 [fish2018/pansou](https://github.com/fish2018/pansou)） |
| **编译产物** | `app/bin/seekbox`（linux/amd64 静态二进制） |
| **交飞牛** | `./pack.sh` 打出的 `.fpk`（只含二进制 + 前端产物，不含 Go 源码） |

## 本仓库相对上游的改动

- **插件-only**：已移除 Telegram 搜索与 JWT 认证；仅保留 26 个启用插件。
- `plugin/ikantv/ikantv.go`：`priority` **3 → 1**（等级 1 = 1000 分，搜索结果优先）
- Health：`channels: []`、`channels_count: 0`、`auth_enabled: false`

以后改插件分数、加插件、修爬虫，都直接改 `backend/pansou/` 下对应文件。

## 编译

需要本机 **Go 1.24+**，或 **Docker**（`golang:1.24-bookworm`）。

```bash
./scripts/build-seekbox.sh
# 输出覆盖：app/bin/seekbox
```

环境变量（可选）：

| 变量 | 说明 |
| --- | --- |
| `PANSOU_SRC` | 源码目录，默认 `backend/pansou` |
| `GO_IMAGE` | Docker 镜像，默认 `golang:1.24-bookworm` |

## 和 FPK 的关系

```
改 backend/pansou/*.go
        ↓
./scripts/build-seekbox.sh   →  app/bin/seekbox
        ↓
（可选）改 frontend → npm run build → app/web/
        ↓
./pack.sh                    →  fn-seekbox-x.y.z.fpk  ← 提交飞牛应用中心
```

`pack.sh` 只打包 `app/`、`cmd/`、`config/`、`wizard/`、`manifest`，**不会**把 `backend/` 打进 FPK。
