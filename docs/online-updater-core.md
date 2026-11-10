# 在线更新核心与发布包

此实现位于在线更新独立副本。它更新业务二进制、静态页面和配套登录脚本，不改用户数据目录、飞牛权限、Node、Chromium 或固定更新助手。

## 运行结构

固定启动器与 `app/bin/update-helper.py` 随第一次完整 FPK 安装。此后固定启动器从 `${TRIM_PKGVAR}/runtime/current` 选择运行版本；没有 current 时使用 FPK 自带版本。

```
runtime/
  settings.json          # 自动更新偏好
  update.lock            # 跨进程安装锁
  update-state.json       # 掉电恢复事务日志
  failed-release.json    # 自动更新失败包抑制
  current -> releases/<version>-<arch>-<sha-prefix>
  releases/              # 经过签名、内容和架构校验的程序
  events/                # 尚未写入管理中心报告队列的事件
  updater.log
```

`data/`、Cookie、账号数据库和追更记录不在允许更新的路径中。正常提交后只清理更新器命名且元数据匹配的旧版本目录，保留当前版和上一版；未知目录不动。

## 信任配置

固定安装配置提供以下环境变量：

- `SEEKBOX_UPDATE_BASE_URL`：管理中心 HTTPS 基础地址，例如包含 `/seekbox-config` 前缀的 URL。
- `SEEKBOX_UPDATE_PUBLIC_KEYS_FILE`：本机受信 JSON 文件，结构 `{ "签名密钥标识": "base64-Ed25519-公钥" }`。
- `SEEKBOX_RUNTIME_ROOT`：独立 runtime 目录。
- `SEEKBOX_BOOTSTRAP_CMD`：固定启动脚本绝对路径，不能位于下载版本内。
- `SEEKBOX_UPDATE_HELPER`：固定 Python 更新助手绝对路径。
- `GATEWAY_SOCKET`：本机 Unix 网关 socket。
- `SEEKBOX_UPDATE_HEALTH_URL`：默认 `http://localhost/app/fn-seekbox/api/health`。

也可通过 `SEEKBOX_UPDATE_PUBLIC_KEYS` 提供本机 JSON 公钥配置，文件配置优先。未配置可信源和公钥时显示 disabled，不联系更新源。服务端响应不能新增可信公钥、指定 shell 命令或改变信任源。

生产只接受 HTTPS，同一源下载，拒绝跨源重定向。隔离测试可设置 `SEEKBOX_UPDATE_ALLOW_LOOPBACK_HTTP=1`，这仅允许 IP 为 loopback 的 HTTP 更新源，签名校验仍然启用。`SEEKBOX_UPDATE_TEST_HEALTH_TIMEOUT` 只在此显式测试模式下生效。

## 制作和签名

使用 Go 标准库工具，不依赖在线签名服务。私钥保存在发布机器外部路径，不进入源码仓库、管理后台、更新包或客户端。

```bash
# 只执行一次；拒绝覆盖已有密钥。另生成 .public.json 供固定安装信任配置使用。
go run scripts/release/main.go -generate-key \
  -key-id release-2026 -private-key /secure/seekbox-release.pem

# package-root/app 下放本次编译的二进制和页面；本命令不代替前后端构建。
go run scripts/release/main.go \
  -root /path/to/package-root -version 1.6.1 -arch amd64 \
  -key-id release-2026 -private-key /secure/seekbox-release.pem \
  -out /path/to/release-1.6.1 -notes-file /path/to/changelog.txt
```

输出 `fn-seekbox-1.6.1-amd64.tar.gz` 和 `release.json`。后者为 `{key_id,payload,signature}`，payload 是原始 JSON 的 Base64，Ed25519 签名直接覆盖原始 payload 字节。管理中心负责保存、展示和发布这对文件，不需要持有签名私钥。

前端构建必须生成 `web/version.json` 并与传入版本一致；Vite 的 `SEEKBOX_VERSION` 可指定测试版本。后端也须使用相同版本构建，运行后通过健康接口验证。工具会验证 ELF 架构、前端版本，以及登录脚本是否成对。

## 包校验

允许的文件只有：

- `bin/seekbox`
- `bin/gateway.py`
- `web/**`，必须包含 `index.html` 和 `version.json`
- `auth/login.cjs` 与 `auth/cookies.cjs`，两者必须同时存在或同时省略
- 自动生成的 `runtime.json`

拒绝绝对路径、`..`、反斜线、符号链接、硬链接、特殊文件、重复路径、越界条目和包内更新助手。压缩包上限 256 MiB，解压流上限 512 MiB，最多 20,000 个条目。下载 SHA256、长度、签名、版本、应用、渠道、CPU 架构、前端版本、ELF 架构全部匹配后才进入 releases。

首版仅支持 `data_schema=1`、`min_bootstrap<=1`，数据库结构不兼容的升级直接要求完整安装包，不能借普通热更新做不可回退的数据迁移。

## 安装与恢复

1. 启动后检查一次新版；自动安装安排在北京时间次日或当天尚未到来的 03:00，加 0～29 分钟随机错峰。
2. 下载和验证在业务仍可运行时完成。
3. 维护钩子阻止新任务进入并等待当前任务结束；取消或失败恢复原状态。
4. 排空后再次询问管理中心，确认同一个 release ID 和包摘要仍然发布。等待期间撤回、替换或网络失败都不切换。
5. 持久化 prepared 事务后启动固定 Python 助手。助手获取跨进程锁并写 installing 握手，初始化失败则原后端解除维护。
6. 助手原子切换 current，调用固定 bootstrap restart。候选后端启动时保持业务维护状态，避免健康验收期间启动新转存。
7. 通过同一个 Unix 网关连续三次验证：后端健康版本、静态 `version.json` 版本、HTML 首页。全部通过才提交。
8. 新版失败时切回 previous，重新启动并检查旧版；失败包摘要记入抑制文件，自动安装不再反复尝试同一坏包，手动重试仍可进行。
9. 新实例仅在启动时记录的同一事务提交或回滚成功、且实际版本匹配时解除维护。其他版本、其他事务或 recovery_failed 不会误放行。

启动器在启动业务前执行 `update-helper.py recover --root ...`。如果另一助手持锁，恢复过程直接返回，允许助手启动候选。若上一次更新中断且没有助手在运行，恢复旧 current 后再启动业务，不递归调用 restart。

终态报告以稳定事务事件 ID、持久化终态时间和固定错误码落盘。崩溃重试复用同一内容；报告队列持久保存成功后才删除事件文件。更新报告故障不会让已经验收成功的版本被回退。

## 核心验证命令

```bash
GOTOOLCHAIN=go1.24.9 go -C backend/pansou test -race ./update -count=1
python3 -m unittest discover -s scripts/release/tests -v
```

覆盖签名篡改/未知公钥、不兼容架构/版本/schema、恶意路径与链接、重复条目、哈希失败、跨源重定向、断网、安装互斥、取消维护、助手初始化失败、发布撤回、候选启动维护、健康失败回滚、掉电恢复、事件幂等与安全清理。完整前后端及管理中心联调由独立测试环境验证，不能据此宣称已在生产 NAS 发布。
