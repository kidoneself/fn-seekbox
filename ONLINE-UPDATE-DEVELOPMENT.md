# 小白搜盘在线更新开发副本

本目录：`fn-seekbox-online-update`。从现有工作区复制，保留复制时的未提交代码。原项目源码未被覆盖；初次开发只做本地验收。2026-11-06 用户随后授权正式启用，NAS 与管理中心已部署，见 [正式启用记录](ONLINE-UPDATE-ACTIVATION.md)。管理中心完整源码放在 `management-center/`，本地配置使用独立数据目录和端口。

## 本次范围

- 顶部版本号显示后端实际运行版本，点击进入完整的“软件更新”页面。
- 管理中心上传签名清单和更新包，保存草稿，确认发布后提供下载；支持撤回和版本/更新结果统计。
- 客户端验证 Ed25519 签名、SHA256、架构、最低 bootstrap 版本和数据结构兼容性。
- 下载到独立版本目录；全实例暂停新任务，等待现有网盘操作结束。排队任务保留。
- 固定更新助手切换版本、重启并检查后台与静态页面。候选版本在确认成功前继续暂停领取网盘任务；成功后恢复，失败恢复上一版。
- 自动更新默认开启，可信发布源配置完整后生效。启动时检查，日常在北京时间 03:00–03:30 错峰执行；忙碌或暂时失败会推迟。原有每日功能检查时间不变。
- 更新结果持久化，管理中心确认接收后再删除待报事件；重复事件不重复计数。

## 两类包

1. **首次安装的 FPK**：包含固定启动器、更新助手、Go 程序、页面、扫码运行环境和可信发布公钥。
2. **后续在线更新包**：只包含 `bin/seekbox`、`bin/gateway.py`、`web/`、配套 `auth/login.cjs`/`cookies.cjs` 和版本元数据。没有账号数据库、Cookie、签名私钥或管理后台口令。

飞牛权限、应用入口、Chromium/Node 运行环境、固定更新器自身或不兼容的数据结构改动，首版仍要求完整 FPK。飞牛应用中心显示安装包版本，页面显示实际运行版本。

## 发布密钥与首个安装包

在安全的外部路径生成签名密钥，只需一次。工具拒绝覆盖已有密钥：

```sh
go run scripts/release/main.go -generate-key \
  -key-id release-2026 \
  -private-key /安全目录/seekbox-release.pem
```

工具同时生成 `.public.json`。私钥仅留在打包端；管理中心与客户端使用该公钥 JSON。不要使用本地验收脚本生成的临时测试密钥发布正式版本。

设置项目 `manifest` 为目标版本后构建：

```sh
bash scripts/build-seekbox.sh
npm run build --prefix frontend
rsync -a --delete frontend/dist/ app/web/
SEEKBOX_RELEASE_PUBLIC_KEYS_FILE=/安全目录/seekbox-release.pem.public.json \
  FNPACK=/路径/fnpack bash pack.sh
```

安装向导的“更新管理员用户 ID”填写可操作更新的 fnOS 用户 ID，多个使用逗号分隔。未配置管理员时页面只读。程序用户保持原来的非 root 身份。

## 后续发版

修改 `manifest` 版本并准备更新说明，然后运行配套构建脚本：

```sh
SEEKBOX_RELEASE_PRIVATE_KEY=/安全目录/seekbox-release.pem \
SEEKBOX_RELEASE_KEY_ID=release-2026 \
SEEKBOX_RELEASE_NOTES_FILE=/路径/更新说明.txt \
  bash scripts/build-online-release.sh
```

输出 `build/releases/<版本>-<架构>/release.json` 和 `fn-seekbox-<版本>-<架构>.tar.gz`。脚本统一前后端版本，签名清单绑定更新说明和文件摘要。上传这两个文件，在后台确认后发布。版本、渠道、架构相同的包不允许被覆盖；修复需要新版本号。

管理端启动、配置、公钥和完整接口见 [管理中心说明](management-center/ONLINE-UPDATES.md)。原有生产配置另存为示例，本地默认 Compose 不使用生产网络或生产数据库。

## 本地验证

测试全部使用临时目录和临时签名密钥，不连接 NAS、不使用真实网盘账号：

```sh
cd backend/pansou
TMPDIR=/dev/shm GOTOOLCHAIN=go1.24.9 go test ./update ./cloud ./api
cd ../..
python3 scripts/tests/test_lifecycle.py
python3 -m unittest discover -s scripts/release/tests -v
npm run build --prefix frontend
node scripts/tests/update-browser.cjs
```

真实进程的联调脚本为 `scripts/tests/online-update-e2e.py`。它使用预先编译的 `/tmp/seekbox-update-v160`、`/tmp/seekbox-update-v161`，以及 `build/e2e-bootstrap-web`（1.6.0）、`frontend/dist`（1.6.1）和 `management-center/build/server`，会启动临时管理中心，完成上传、发布、真实升级、故意启动失败后的回滚与真实上报检查。临时签名私钥和口令摘要在结束时删除，结果写入 `build/online-update-e2e.json`。

旧源码归档中的 `management-center/docs/live-verification.json` 和旧截图不是本次在线更新验收记录。本次浏览器模拟接口截图仅用于布局及交互检查，真实升级以端到端脚本结果为准。

## 正式启用

长期签名密钥、公钥信任、管理中心发布存储和首次 FPK 安装已于 2026-11-06 配置完成；正式启用记录见 `ONLINE-UPDATE-ACTIVATION.md`。上报复用现有已登记的账号所属用户身份，固定选择一个启用统计的身份；该身份停用统计时保留本地待报记录。

## 本次已完成的验收（2026-11-06）

- 管理中心全量 Go 测试、Go vet、前端构建通过。
- 更新核心 Go race 测试通过；Python 助手 10 项测试通过。
- cloud/api 全量测试及 race 检测通过；覆盖跨用户排空、扫码延续、候选启动暂停、报告重试、权限与参数限制。
- 本地真实升级 1.6.0 → 1.6.1 成功；故意发布可校验但不能启动的 1.6.2 ELF 后恢复 1.6.1。
- 真实管理中心统计核对：1 次成功、1 次回滚、2 条终态报告，版本分布为 1 台运行 1.6.1。
- 测试数据文件、凭据密钥和已登记身份保留；撤回后查询立即停止提供该版本。
- 原项目全部复制时源码哈希与 Git 状态保持不变；未部署线上后台或 NAS。

初次本地验收结果：`build/online-update-e2e.json`。随后生成的正式 FPK 已内置长期发布公钥，管理中心也已信任同一公钥；该文件中的本地测试历史与本次正式启用记录分别保留。
