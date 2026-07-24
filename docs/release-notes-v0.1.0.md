# fclone v0.1.0

这是 fclone 在当前 rclone 代码基线上的首个正式发布。嵌入的核心来自
rclone v1.75.0 development line，精确上游基线为 `c99b2d11e`。

## 主要能力

- Google Drive Service Account 目录池、预加载、轮询及 typed quota
  错误触发的账号切换。
- `remote:{ID}` 与花括号 Google Drive/Docs URL 直连文件、目录和 Shared Drive，
  包括可重建的 `resourcekey` identity。
- `backend lsdrives`、`add-drive`、`delete-drive` 兼容命令；保留上游
  `drives`、`copyid`、`moveid`。
- Google Drive 目标的 `--check-first` 目录预建：只处理实际入队传输，
  并在 rename worker 完成后执行。
- `Files/s`、文件数量 ETA、`xfr#` / `chk#` 进度信息。
- 独立的 fclone 与 rclone-core 版本输出；移除会安装官方 rclone 的
  `selfupdate`；`version --check` 保持只读成功退出，但不再查询官方 rclone。

## 兼容与迁移

- 保留 `rclone.conf`、`RCLONE_*`、remote 语法、默认配置/缓存目录及
  上游 module path。
- 首次使用现有配置时，建议先执行只读命令和 `--dry-run`。
- 完整配置、命令与边界见 `docs/fclone-compatibility.md`。

## 发布物

Release 提供 Linux、macOS、Windows 的 amd64/arm64 六平台归档。
每个归档均包含可执行文件、`COPYING`、`NOTICE`、`README.md`，并附带
独立 SHA-256 文件和汇总 `SHA256SUMS`。

## 已知验证边界

CI 覆盖全仓 unit tests、变更包 `go vet`、Drive/cache/sync/accounting
定向 race tests、默认构建安全 smoke 和六平台交叉构建。由于发布环境没有
受控 Google Drive 测试凭据，Shared Drive 创建/成员复制/删除、真实配额换号
与受保护 resource-key 对象仍以 fake transport 和单元测试为主要证据；首次对
真实数据执行写操作前请使用隔离测试盘和 `--dry-run`。
