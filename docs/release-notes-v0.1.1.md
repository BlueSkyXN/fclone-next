# fclone v0.1.1

这是 fclone v0.1.0 之后的修正发布，嵌入的 rclone core 不变
（v1.75.0 development line，上游基线 `c99b2d11e`）。

## 变更

- `services_max` 与 `service_account_min_sleep` 现在拒绝负值并给出明确错误，
  不再静默归一；`services_max=0` 保持既有“使用默认值”语义。
- 默认开发构建版本升级为 `v0.1.1-dev`；正式构建仍由 tag 注入版本。

## 兼容

- 无命令、配置键或输出格式变化；合法配置不受影响。
- 仅原先无效（负数）配置的报错时机提前到 remote 初始化阶段。

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
