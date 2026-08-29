# fclone v0.1.2

这是一个上游同步与安全维护发布。嵌入的 rclone core 从 v1.75.0 development
commit `c99b2d11e` 更新到签名正式版 `v1.75.0`，精确 tag commit 为
`9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048`。

正式版之后还精确回移了三个已合入 `rclone/rclone` 的安全维护提交，不包含其他
development 功能：

- `00593a96f`：更新 `golang.org/x/image` 到 v0.45.0，修复
  CVE-2026-46603；同时更新 `golang.org/x/text` 到 v0.41.0。
- `f0b210a88`：将发布验证工具链更新到 Go 1.26.6，包含多项标准库 CVE 修复。
- `e5e1ee3e9`：更新 `golang.org/x/crypto` 到 v0.55.0，修复
  CVE-2026-56854。

## 主要变化

- 合入 rclone v1.75.0 正式版的安全修复，包括 WebDAV/S3 credential leak、
  SFTP/FTP command injection、RC 未认证调试接口和 remote listing、HTTP CONNECT
  header OOM、路径穿越及异常崩溃隔离。完整上游变化见
  [rclone v1.75.0 changelog](https://rclone.org/changelog/#v1-75-0-2026-07-31)。
- 修复 `fs/march` async RC job 正常完成后的 goroutine leak，以及 iCloud Drive
  有效 2FA code 返回 HTTP 409、Dropbox 小文件上传内存占用等上游问题。
- 合入 Azure Blob experimental Apache Arrow listing、大容器并行 listing、
  RC options parser 和 OpenBSD mount/NFS 等正式版能力；实验能力仍保持 opt-in。
- 收紧 fclone 的 `rc serve` 路径和 local-backend 边界，阻止越过已提供的 remote
  root；限制 Plex token 请求和重定向停留在原始 origin。
- 更新 CI 与发布门禁：Go 1.25/1.26.6、真实 FUSE tests、兼容层 race、
  `govulncheck`、CodeQL、Windows 原生编译及六平台构建打包。

## fclone 兼容能力

以下能力继续保留：

- Google Drive Service Account 目录池、预加载、轮询和配额触发换号。
- `remote:{ID}` 与 Google Drive/Docs URL 直连文件、目录和 Shared Drive。
- `backend lsdrives`、`add-drive`、`delete-drive` 兼容命令。
- Google Drive 目标的 `--check-first` 目录预创建。
- Files/s、文件数量 ETA、fclone/rclone-core 双版本输出。
- 禁用会安装官方 rclone 的 `selfupdate`。

`rclone.conf`、`RCLONE_*`、remote/backend 名称、命令语法、Go module path 和默认
配置/缓存路径均不变，不需要配置迁移。未认证的 `rc serve` 使用者需要配置认证，
或在明确接受风险时显式使用 `--rc-no-auth`。

## 发布物和验证

Release 提供 Linux、macOS、Windows 的 amd64/arm64 六平台归档。每个归档均包含
可执行文件、`COPYING`、`NOTICE`、`README.md`，并附带独立 SHA-256 文件和汇总
`SHA256SUMS`。这些 checksum 未签名，只验证下载完整性，不独立认证发布者。

CI 覆盖全仓 unit tests、真实 FUSE tests、`go vet`、Drive/cache/sync/accounting
定向 race tests、module/tidy、actionlint、golangci-lint、`govulncheck`、CodeQL、
Windows 原生编译、默认构建安全 smoke 和六平台构建打包。

## 已知边界和回退

发布流程没有受控 Google Drive 凭据，因此 Shared Drive 创建/成员复制/删除、真实
配额换号和受保护 resource-key 对象仍以 fake transport 和单元测试为主要证据；
首次对真实数据执行写操作前请使用隔离测试盘和 `--dry-run`。

如需回退，可直接换回 `fclone-v0.1.1` 的对应平台二进制；配置格式没有变化。
回退会同时撤销本版本包含的上游安全修复。
