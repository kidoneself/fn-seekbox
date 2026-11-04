# 夸克转存适配说明

核对日期：2026-11-03。

当前实现独立编写，使用 Go 标准库调用夸克网页端接口。只接受用户主动提供的 Cookie，未使用其他项目的账号、配置、客户端签名密钥，也未引入第三方源码或 SDK。

## 来源与协议范围

- [夸克官方转存说明](https://github.com/quark-clouddrive/quarkclouddrive_offical/blob/main/skills/quarkclouddrive/references/file-saveas.md)：确认普通转存和增量转存是不同操作，异步任务需等待完成；官方方案有独立 OAuth/SDK 接口。本项目没有实现该官方 SDK 的 `saveas-update`。
- [夸克官方授权说明](https://github.com/quark-clouddrive/quarkclouddrive_offical/blob/main/skills/quarkclouddrive/references/auth.md)：官方 CLI 支持浏览器授权。该流程与网页 Cookie 不等价，未直接套用它的应用身份。
- [quark-auto-save 项目接口实现](https://github.com/Cp0204/quark-auto-save/blob/main/quark_auto_save.py)：核对网页端账号查询、分页列表、分享 token/detail/save 和任务查询的请求字段。只参考网络协议事实，不复制其实现。
- [QuarkPanTool 项目实现](https://github.com/ihmily/QuarkPanTool/blob/main/quark.py)：交叉核对创建目录字段及同名错误行为。
- [GoQuark 客户端](https://github.com/ButterFuture/GoQuark/blob/main/internal/client/client.go)：交叉核对 Cookie 失效错误码。未引入其传输层实现。

## 本次行为

- 账号连接通过真实账号接口验证昵称；凭据仅发送给固定的夸克 HTTPS 域名，禁止自动重定向，错误不回显上游原文或请求 URL。
- 分享按目录递归分页读取，保留相对路径；超过 10000 条、32 层或分页不一致会明确失败，不能把部分目录当完整分享。
- 同目录明确选中的文件按批提交 `fid_list` 与一一对应的 `fid_token_list`，每批最多 100 项，随后查询异步任务；只有任务状态 2 视为完成。任务失败、暂停、超时或未知状态都不会记作成功。
- 目标目录存在同名文件时在提交前停止该批转存并返回冲突；不会请求覆盖、删除或移动。尚未找到网页转存 API 的可靠 `conflict_mode` 参数定义，因此不发送猜测参数。用户可以选择另一个目录。
- 追更去重由本项目任务记录负责，不依赖官方 SDK 的历史转存功能。网页接口可能变化，登录失效时需要用户重新提供 Cookie。

## 验证边界

单元测试通过模拟 HTTP 传输验证请求构造、身份失效、分享递归与分页、路径检查、转存最终状态及禁止覆盖行为。没有使用用户真实账号执行在线转存，因此不能把这些测试描述为网盘实号验收。扫码登录和自动刷新 Cookie 未实现。

## 对照用户自有影视盘

另只读参考 `ikantv-next/apps/api/internal/provider/quarkweb/account.go` 的真实会话验证：既验证网站 `account/info` 的 `success` 字段，也调用网盘 `/member`，避免把仅网站登录有效当作网盘可转存。转存请求与任务终态判定亦已逐项核对该项目 `quarkweb/adapter.go`。

影视盘 `drive/qr_authorization.go` 是独立扫码服务的协议客户端，需要单独配置该服务地址及授权；本次未读取或复制其实际配置、密钥、运行数据，也未把该私有服务接入本项目。其夸克网页驱动没有可直接借用的 Cookie 自动续期机制。

1.5.5 追加核对影视盘 `quarkweb/adapter.go:SaveShared`：它既支持 `pdir_save_all=true` 整目录保存，也支持多文件 ID/Token 批量保存。本项目采用后者，按目录分批、保留原始相对路径与已有追更文件标记；不宣称已采用整分享/整目录快速复制。扫码能力以 `cloud-transfer-and-follow.md` 后续版本说明为准。
