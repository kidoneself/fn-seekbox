# 在线更新正式启用记录

日期：2026-11-06。用户授权后，使用独立副本完成管理中心和自有飞牛 NAS 部署。原项目 `fn-pansou` 的 333 个源码文件哈希仍与复制时一致；没有提交或推送仓库。

## 当前状态与入口

- NAS：`10.10.10.15`，实际运行 **1.6.3**。页面顶部版本号进入“软件更新”。飞牛应用中心仍显示最初安装的启动包 **1.6.0**，应用内显示实际运行版本。
- 更新管理员：fnOS 用户 `1000`，通过原生升级向导参数保存；其他用户可查看，不能控制更新。
- 管理中心：<https://hermes.smartloan.cn/seekbox-config/> → “版本发布”。已有密码保持不变，新增签名包上传、发布、撤回、版本分布与结果次数。
- 自动更新已开启：启动时检查版本，每天北京时间 **03:00–03:30** 错峰安装；忙碌时等待，不中断正在执行的网盘操作。原有 **00:00:01** 账号功能检查设置保持不变。

## 首次启用验证结果（1.6.0 → 1.6.1）

- 真实 NAS 从 **1.6.0 在线更新到 1.6.1**，下载、签名及摘要验证、任务排空、切换和健康检查成功。
- 更新后正常执行一次应用停止/启动，仍运行 1.6.1，管理员权限和自动更新设置保留。
- 保留 3 个网盘账号、3 个追更计划、128 条转存记录、17 条功能检查记录。凭据加密密钥摘要不变。
- 管理中心确认 **成功 1 次、失败 0 次、回滚 0 次**；正常重启后仍只计 1 次。版本分布为 1 台 1.6.1。
- 原有站点列表及百度、夸克、迅雷测试分享的配置摘要不变；数据库、管理员口令摘要、实例登记与历史统计继续使用原数据。
- 故障回滚之前已在隔离环境实测通过。本次没有向生产发布故障包，也没有触发真实网盘转存/删除测试。

本次验收记录位于 `build/production-nas-verification.json`、`build/production-release-verification.json`、`build/production-package-verification.json`。原本地隔离验收记录单独保留。

## 签名与以后发布

长期密钥标识：`seekbox-release-2026`。

- 本机私钥：`/home/kidoneself/.local/share/seekbox-release/keys/release-2026.pem`，权限 0600，父目录 0700。仅打包端持有，未上传到服务器、NAS、仓库或任何安装包。请作为长期发布资料保管；后续不能随意重新生成替代。
- 公钥 JSON：同路径追加 `.public.json`。已内置正式 FPK，并部署到管理中心 `secrets/release-public-keys.json`。

后续修改代码后，设置更高版本并构建，例如：

```sh
SEEKBOX_VERSION=1.6.2 \
SEEKBOX_RELEASE_PRIVATE_KEY=/home/kidoneself/.local/share/seekbox-release/keys/release-2026.pem \
SEEKBOX_RELEASE_KEY_ID=seekbox-release-2026 \
SEEKBOX_RELEASE_NOTES_FILE=/路径/更新说明.txt \
  bash scripts/build-online-release.sh
```

将生成的 `release.json` 和 `.tar.gz` 上传至管理中心“版本发布”，核对后发布。同版本不可覆盖。仅改在线可更新的程序、页面或扫码辅助脚本可走此流程；固定更新器、运行环境、飞牛权限或不兼容数据结构仍需完整 FPK。

## 包与备份

- 完整安装包：`fn-seekbox-1.6.1-x86.fpk`，118001226 字节；SHA256：`2c8c8d58750a364d33a0fa7bbe32bd9ade2e029a9a58e8a8576a16efd9c78dfc`。后续自有机器首次安装可用此包；安装向导填写该机器实际的更新管理员用户 ID。
- 已发布在线包：`build/releases/production-1.6.1-amd64/`，9,335,334 字节，SHA256：`1f7a2fe6ac6e5a96385bb5855870aac9598f7efaf26ca10297534642dc852b6c`。
- NAS 升级前备份：`/vol1/1000/.codex-backups/fn-seekbox/20261106-before-online-1.6.0`；含原应用、数据、凭据密钥及验证记录。管理员参数补齐前另有 `20261106-before-online-admin-1.6.0` 备份。
- 管理中心备份：`/home/ubuntu/hubstack/seekbox-backups/20261106-before-online-update`；数据库停服备份，包含原程序、页面、Compose、Caddy 配置与旧镜像标识。
- 线上镜像：`local/seekbox-public-config:20261106-online-update-1`。网关仅为本服务增加 `/api/client/releases/*`，其他站点保持原配置。


## 2026-11-06：1.6.1 → 1.6.2 在线更新实测

经用户要求“刚好测试一下在线更新”，将边界检查修复打包签名并发布，通过应用自身 `/api/update/check` 与 `/api/update/install` 完成真实 NAS 在线升级，没有通过飞牛应用中心重装，也没有手动替换 NAS 运行二进制。

- 已发布版本：**1.6.2 / stable / amd64**。发布 ID：`rel_VGf8WD6pP6oEFyQIIGq3C38WjIXd6akHBRT_qlmUO5w`。
- 更新包：`build/releases/production-1.6.2-amd64/fn-seekbox-1.6.2-amd64.tar.gz`，9,338,023 字节，SHA-256：`dd5ffa90734c1a2c1baf84b9fb2868253484555f73136abd49184dc88413773d`。本地验签、公开下载字节、包内版本及运行后端摘要均已匹配。
- 实际运行目录：`/vol1/@appdata/fn-seekbox/runtime/releases/1.6.2-amd64-dd5ffa90734c1a2c`；更新事务 committed，健康接口与实际返回的 JS/CSS 均验证通过。应用仍以非 root 身份运行，更新管理员权限及自动更新设置保留。
- 保留 3 个账号、3 个追更计划、143 条转存记录及原有 17 条功能检查。凭据密钥摘要不变；追加本次 3 条真实检查后共有 20 条检查记录。
- 管理中心本版本更新结果：成功 1、失败 0、回滚 0；版本分布为 1 台 1.6.2。再次检查版本无可用更新，没有重复计次。
- 三个网盘账号均通过真实身份校验，但本次完整转存闭环未通过：百度配置中单独提取码与链接内提取码不一致，返回提取码错误；夸克和迅雷拒绝转存当前账号自己的分享。测试产生的两个空目录已通过应用清理，待清理与待上报均为 0。后台核对 19 条相关事件，三次均计 0 次成功，原手动成功次数 6 保持不变。
- 没有更改后台测试链接、提取码或搜索站点。成功闭环仍需正确的百度提取码，以及由另一账号提供的夸克、迅雷小文件分享。
- 本次为主动触发在线升级；没有调整 NAS 时钟，未宣称已观察到凌晨自动更新或午夜账号检查的自然触发。

管理中心统计修复镜像：`local/seekbox-public-config:20261106-check-fixes-162`，健康检查通过，数据库、管理员密码摘要、发布公钥与公共配置保留。

备份：NAS `/vol1/1000/.codex-backups/fn-seekbox/20261106-before-online-1.6.2`，数据副本逐文件摘要核验通过；管理中心 `/home/ubuntu/hubstack/seekbox-backups/20261106-before-check-fixes-1.6.2`，停服备份数据库后仅更新该服务。

脱敏验收记录：`build/online-update-1.6.2/`。完整 FPK 仍为此前 1.6.1 包，本次发布的是在线更新包。

## 2026-11-07：1.6.2 → 1.6.3 在线升级

按用户要求，已将 `10.10.10.15` 上的 1.6.2 通过应用 `/api/update/check`、`/api/update/install` 升级到 **1.6.3**。未通过飞牛应用中心重装，未手工替换 NAS 运行程序。

- 发布：1.6.3 / stable / amd64，ID `rel_NFGvGMMk7AlWfyK1ecFapTrLwaiCofFTQZ7H3Xfw45Q`。
- 在线包：`build/releases/production-1.6.3-amd64/fn-seekbox-1.6.3-amd64.tar.gz`，9,344,289 字节，SHA256 `781513afc90ee366a035b76e2c28d78a4799d4285288d43309144224c4beaae7`。本地 Ed25519 验签通过，内容与已验收的 1.6.3 x86 FPK 对应文件一致，公开下载摘要一致。
- NAS 更新事务 committed，实际运行目录 `/vol1/@appdata/fn-seekbox/runtime/releases/1.6.3-amd64-781513afc90ee366`；后端健康与页面版本均 1.6.3，实际返回的 JS/CSS 与更新目录匹配。应用仍以非 root 用户运行，更新管理员与自动更新设置保留。
- 升级前 3 个网盘账号、3 个追更计划、179 条转存记录和 23 条功能检查全部保留；凭据加密密钥摘要、管理中心配置不变。待上报事件为 0。
- 管理中心已记录本版本成功 1、失败 0、回滚 0，版本分布 1 台 1.6.3；原公共配置和客户端设置摘要保持不变。
- 从真实 NAS 发起迅雷扫码，经国内扫码中心取得二维码，约 8 秒；取消后 NAS 会话已删除、现有账号不变。验收没有手机确认，因此不宣称完成新的真实账号登录/凭据刷新验收。
- 升级前已有 2 条待清理记录，升级后仍保留；本次没有额外执行网盘转存或清理，不将已有检查问题算作升级成功解决。

备份：`/vol1/1000/.codex-backups/fn-seekbox/20261107-before-online-1.6.3`。升级前数据副本逐文件摘要核验通过。脱敏验收记录：`build/online-update-1.6.3/`。
