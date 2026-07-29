# mawaya/fclone 历史魔改分析

## 结论

`mawaya/rclone` 当年的 fclone 不是一个全面改写的 rclone，而是一组高度集中在
Google Drive 后端、同步调度和统计输出上的补丁。它的主要能力是：

1. 从目录载入多个 Service Account，预加载 Drive client，在部分元数据请求中轮询，
   并在配额错误后切换账号；
2. 用 `remote:{ID}` 或花括号内的 Google Drive URL 直接指定文件、文件夹或共享云端硬盘；
3. 在 `--check-first` 完成检查后、传输开始前，批量预建 Google Drive 目标目录；
4. 增加 `lsdrives`、`add-drive`、`delete-drive` 后端命令；
5. 在进度统计中增加 Files/s 和按文件数计算的 ETA，并保留已排空队列的
   `xfr#`/`chk#` 计数；
6. 将产物和版本文案改名为 fclone。

最初候选以 rclone v1.74.4 为基线；本次正式实现已迁移到 rclone v1.75.0
开发线的上游提交 `17629d67b`。项目做行为级复刻，不追求旧实现的逐行移植或
bug-for-bug 兼容。配置名和常用命令语义尽量保留，并重新设计了并发、缓存、
取消、权限保护和可测试性。

## 审计范围和可验证坐标

审计对象是真正承载历史 fclone 代码的
[`fwwkr/master`](https://github.com/mawaya/rclone/tree/fwwkr/master) 分支，不是该仓库的
`master` 分支。以下坐标均可由 Git 对象和链接复核：

| 坐标 | 提交 | 含义 |
|---|---|---|
| 首个集中魔改提交 | [`96ddd31787deedc7740b1f2f4fbd6b647c55ac15`](https://github.com/mawaya/rclone/commit/96ddd31787deedc7740b1f2f4fbd6b647c55ac15) | 2020-06-01；提交说明明确写有 `apply gclone mod`，合入了直接 ID/URL、SA 目录相关修改和 Files/s |
| 首个魔改提交的父提交 | [`8774381e2ef6298aab7fc6fbedd7743600fdc84b`](https://github.com/mawaya/rclone/commit/8774381e2ef6298aab7fc6fbedd7743600fdc84b) | `git describe` 为 `v1.52.0-8-g8774381e2`；用于理解项目起点，不适合作为最终净差异基线 |
| 最后一次合入当时上游 | [`3318f04941bdb7f903226fc28074c4282a91d1f9`](https://github.com/mawaya/rclone/commit/3318f04941bdb7f903226fc28074c4282a91d1f9) | 合并提交的第二父提交是 `4a001b8a…` |
| 净差异基线 | [`4a001b8a02808912cb3e1e67ac964e225a108062`](https://github.com/mawaya/rclone/commit/4a001b8a02808912cb3e1e67ac964e225a108062) | 2020-09-05；`git describe` 为 `v1.53.0-9-g4a001b8a0`，源码版本已是 `v1.54.0-DEV` |
| v0.4.1 标签指向 | [`07e5a6ed9869fb89602dbcd383f4d58a85024701`](https://github.com/mawaya/rclone/commit/07e5a6ed9869fb89602dbcd383f4d58a85024701) | 注解标签 [`fclone-v0.4.1`](https://github.com/mawaya/rclone/tree/fclone-v0.4.1) 解引用后指向此提交；也是最后一个功能修复 |
| 历史分支最终 HEAD | [`88050fc0300a0974604dd7083fe9a33440a9450e`](https://github.com/mawaya/rclone/commit/88050fc0300a0974604dd7083fe9a33440a9450e) | 2020-09-22；相比 `07e5a6ed…` 只删除了 README 中的 3 行，运行时树不变 |
| 初始候选基线 | [`rclone/rclone@5bc93a2a`](https://github.com/rclone/rclone/commit/5bc93a2a7ab0ebd0a11352bc4968eabeffb18027) | [rclone v1.74.4](https://github.com/rclone/rclone/releases/tag/v1.74.4)，2026-07-08 |
| 当前实现基线 | [`rclone/rclone@17629d67`](https://github.com/rclone/rclone/commit/17629d67b26904d102d9072c66ba95cf08a51316) | rclone v1.75.0 development line，2026-07-28 |

“最早起点”和“最终净差异基线”不是同一个概念。fclone 在 2020 年 6–9 月期间继续
合入上游；因此要回答“最终 fclone 比同期 rclone 多了什么”，应比较
[`4a001b8a…88050fc0`](https://github.com/mawaya/rclone/compare/4a001b8a02808912cb3e1e67ac964e225a108062...88050fc0300a0974604dd7083fe9a33440a9450e)，
而不是直接从 `8774381e…` 算起。

## 功能演化

| 日期 | 提交 | 可确认的功能变化 |
|---|---|---|
| 2020-06-01 | [`96ddd317…`](https://github.com/mawaya/rclone/commit/96ddd31787deedc7740b1f2f4fbd6b647c55ac15) | 首次集中合入 gclone 相关改动：Drive ID/URL 解析、SA 目录路径处理、SA 日志、Files/s |
| 2020-06-13 | [`9e992b9d…`](https://github.com/mawaya/rclone/commit/9e992b9d0adb642e365c899d2b4e16966b3a406c) | SA 目录自动选账号、多 client 池、启动预加载，增加 `services_preload`、`services_max`、`service_account_min_sleep` |
| 2020-06-29 | [`792692b5…`](https://github.com/mawaya/rclone/commit/792692b527409a96b1dc6ae61f1ac19989792feb) | `--check-first` 检查后预建 Drive 目标目录 |
| 2020-07-03 | [`515a059f…`](https://github.com/mawaya/rclone/commit/515a059ff884fb27132d1648d49311fcc9a29ca6) | 增加 `backend lsdrives`，默认输出为 `ID<TAB>Name` |
| 2020-09-04 | [`6f0b5910…`](https://github.com/mawaya/rclone/commit/6f0b5910f6fbca157efe5007b297118d6dbbc37a) | 放宽文件 ID 长度限制，增加对当时 Drive mobile URL 形式的支持 |
| 2020-09-04 | [`05e8d4bc…`](https://github.com/mawaya/rclone/commit/05e8d4bc3c5ed7b92c10e895c1ef071d9ad54dd3) | 增加 `backend add-drive` 和 `backend delete-drive`，支持从另一共享云端硬盘复制/替换成员 |
| 2020-09-05 | [`c3aac1d3…`](https://github.com/mawaya/rclone/commit/c3aac1d359ac90544d057ebbc3a44d5fa743932e) | 修复预建目录在 `Pending: 0` 时卡住的问题 |
| 2020-09-05 | [`986bf084…`](https://github.com/mawaya/rclone/commit/986bf084c5bd3106e769e54233dc4ae3c9b04040) | 版本更新为 fclone v0.4.1 |
| 2020-09-06 | [`07e5a6ed…`](https://github.com/mawaya/rclone/commit/07e5a6ed9869fb89602dbcd383f4d58a85024701) | Drive Fs 创建失败时正确返回错误；成为 v0.4.1 标签目标 |

这个时间线只列与本次复刻直接相关的里程碑，不是分支的完整 commit log。

## 最终净差异

对 `4a001b8a…` 和 `88050fc0…` 执行 `git diff --shortstat` 的原始结果是：

```text
22 files changed, 5109 insertions(+), 3869 deletions(-)
```

这个数字不能直接代表魔改的代码量，因为其中包含重生成的 `MANUAL.html`、
`MANUAL.md`、`MANUAL.txt`，以及一个 Git 按二进制文件计数的 `rclone.exe`。
实际运行时行为改动主要集中在 6 个 Go 源文件，合计 983 行新增、43 行删除：

| 文件 | 新增 | 删除 | 主要内容 |
|---|---:|---:|---|
| `backend/drive/drive.go` | 893 | 34 | SA 池、ID/URL、目录预建、共享云端硬盘命令 |
| `fs/sync/sync.go` | 62 | 1 | `--check-first` 后的 Drive 目录预建阶段 |
| `fs/accounting/stats.go` | 20 | 5 | Files/s、文件 ETA、`xfr#`/`chk#` 展示 |
| `cmd/copy/copy.go` | 6 | 0 | 识别直接文件 ID 产生的 `isFile:` 内部 root |
| `cmd/cmd.go` | 1 | 1 | `version` 输出从 rclone 改为 fclone |
| `fs/version.go` | 1 | 2 | 将上游版本常量直接替换为 `v0.4.1` |

剩余变更主要是 README、版本文件、生成文档、交叉编译脚本、两个 mount 测试、
`go.mod`/`go.sum`、`timecmd.bat` 和已编译的 `rclone.exe`。

## 历史能力的具体语义

### Service Account 池

历史版增加了以下 Drive 配置项，默认值来自最终源码：

```ini
service_account_file_path = /path/to/json-directory
service_account_min_sleep = 100ms
services_preload = 50
services_max = 100
```

- 从目录中收集 `.json` 文件；未明确设置主 SA 时从中选一个作为初始账号。
- 启动时创建多个 `drive.Service`，并对列表、建目录、Drive 内复制、变更通知等部分
  API 请求轮询 client。
- 遇到 `rateLimitExceeded`、`userRateLimitExceeded` 或部分 daily-limit 错误时，
  尝试切换主 SA；`service_account_min_sleep` 限制切换频率。
- SA 只提供身份，不会合并权限或 My Drive 命名空间。所有账号仍需要独立获得目标数据的访问权。

### 直接 Drive ID 和 URL

历史语法用花括号与普通路径分隔：

```console
fclone lsf 'drive:{DRIVE_OBJECT_ID}'
fclone lsf 'drive:{https://drive.google.com/drive/folders/DRIVE_FOLDER_ID}'
fclone copy 'drive:{https://drive.google.com/file/d/DRIVE_FILE_ID/view}' ./destination
```

启动时先调用 Drive API 检查 ID，再区分普通文件、文件夹和 Shared Drive。普通文件
使用 `isFile:<name>` 作为内部 root，并在 `copy` 命令中增加特判。这说明当时的
直接文件支持是命令特例，而不是一个完整的 Fs/缓存身份模型。

### `--check-first` 后的目录预建

历史版在检查队列排空后，收集需要传输文件的目标目录，并调用 Drive 特有的
`CreateDirs` 并发创建。空源目录只在 `--create-empty-src-dirs` 下纳入。这个优化不影响
其他目标后端。

### Shared Drive 命令

- `lsdrives` 按名称排序，默认每行输出 `ID<TAB>Name`，可用 `separator` 替换分隔符。
- `add-drive` 创建 Shared Drive；`copy-members` 复制成员，`replace-members` 还会尝试删除不在源集合中的成员。
- `delete-drive` 删除当前选中的 Shared Drive，默认交互确认，出现 `force` 键时跳过确认。

### 进度统计和品牌

历史版用已完成传输文件数除以总活动时间得到 Files/s，再用剩余文件数计算 ETA。
它还去掉了“队列非空才显示 `xfr#`/`chk#`”的条件。品牌方面则直接把上游
`fs.Version` 替换成 `v0.4.1`，丢失了嵌入的 rclone core 版本信息。

## 已识别的旧实现问题

以下项目都能从最终源树或其提交历史中直接观察到。它们是本次重新设计的依据，
不等于对当年所有实际运行环境的故障率做结论。

1. **运行中原地替换共享 client 状态。** `changeServiceAccountFile` 会替换 `f.svc`、
   `f.v2Svc`、`f.client`、`f.pacer` 和配置字段，而大量并发请求直接读取这些字段。
   只锁住配额错误分支不能保护其他读者，因而存在竞态和同一逻辑操作中身份不稳定的风险。
2. **SA 池的锁和上限语义不一致。** `GetService` 在加锁前读取 slice 长度；
   `PreloadServices` 直接把所有预加载 client 插入 slice，没有应用 `_addService` 中的
   `services_max` 截断逻辑。目录通过 Go map 遍历和随机选取，启动结果不可复现。
3. **分页与账号切换绑定不清晰。** 变更通知代码可以在分页之间重新取一个 service；
   另一方面，列表、建目录或复制函数会先把 service 保存在局部变量里，但配额重试只切换
   全局 `f.svc`。这使“一次逻辑操作到底由哪个账号执行”缺少稳定保证。
4. **目录预建调度器脆弱。** 历史中先后出现“修复死锁”、“出错时取消”和
   “`Pending: 0` 时卡住”的专门提交，例如
   [`7a2ced04…`](https://github.com/mawaya/rclone/commit/7a2ced0489a63fde8e9d9b0da2d20cf0c0597280)、
   [`23f71084…`](https://github.com/mawaya/rclone/commit/23f7108473aa874acdcb46c271a2fefaa053e01a)和
   [`c3aac1d3…`](https://github.com/mawaya/rclone/commit/c3aac1d359ac90544d057ebbc3a44d5fa743932e)。
   最终实现仍使用自管 channel、goroutine、重试定时器和 `WaitGroup`，发送不感知 context 取消。
5. **核心 sync 层反向依赖 Drive 具体类型。** `fs/sync/sync.go` 直接 import
   `backend/drive` 并断言 `*drive.Fs`，增大了跟随上游合并时的冲突面。
6. **Shared Drive 写操作缺少安全边界。** `add-drive` 没有检查 dry-run；
   `delete-drive -o force=false` 仍会因为 `force` 键存在而当作 true。权限复制尝试回写
   `PermissionDetails`、`Domain`、`ExpirationTime` 等字段，且 replace 逻辑没有明确保护现有 manager，
   有 API 拒绝或锁出管理者的风险。
7. **构建与测试有回归。** 仓库保持的 module path 是 `github.com/rclone/rclone`，
   但交叉编译脚本把 ldflag 目标写成 `github.com/mawaya/rclone/fs.Version`。
   `cmd/cmount` 测试被整体替换为 `SkipUnreliable`，`cmd/mount2` 也在测试开头跳过；
   源树还提交了已编译的 `rclone.exe`。

## 本项目的复刻与改良对照

| 历史能力 | 本项目实现 | 兼容性/刻意差异 |
|---|---|---|
| `service_account_file_path` 和 SA 轮换 | 按文件名稳定排序；主 client 用原子可切换 transport；高容量元数据和上传请求使用“一次逻辑操作一个 lease”；分页拿到 token 后固定身份，resumable session 换号时整文件重试 | 保留四个历史配置名及默认值；`services_max` 明确为内存 client-cache 槽位上限，到上限后可汰换缓存项，仍在运行的 lease 会安全保留其 transport，不再把上限误当成可用 JSON 总数上限 |
| `{ID}` / `{Drive URL}` | 识别文件、文件夹和 Shared Drive；直接文件保留真实文件名；公开 ConfigString 仍可往返，内部使用不可与用户路径冲突的缓存键 | 保留花括号语法；只接受 `drive.google.com`/`docs.google.com`，拒绝任意 HTTP host；支持现代 `resourcekey` |
| `--check-first` 预建 Drive 目录 | sync 核心只识别可选的 backend-neutral 接口；收集仅在该能力启用时发生；按层级父目录优先创建，同层并发 | 仍只对 Drive 目标生效；支持 dry-run 和 context 取消；预建是 best-effort，失败后由正常传输的惰性建目录决定最终结果 |
| `lsdrives` | 保留按名称排序的行式输出 | 默认仍是 `ID<TAB>Name`；保留 `separator` |
| `add-drive` | 创建前先校验源 Drive；dry-run 不创建也不改权限；返回 ID、名称和权限处理计数 | 只复制可安全定位的 user/group 直接成员；跳过 domain/anyone；replace 始终保留现有 organizer/manager，避免锁出 |
| `delete-drive` | 严格解析 `force` 布尔值，无值的 `-o force` 仍表示 true | `force=false` 不再被当作 true；非法布尔值报错 |
| Files/s 和文件 ETA | 保留文件速率、文件 ETA 和队列排空后的 `xfr#`/`chk#` | 字节速率和字节 ETA 仍是传输量的主指标；新增单元测试锁定格式 |
| fclone 品牌 | 二进制和发布产物名为 `fclone`；`fclone version` 分别输出 fclone 版本与嵌入的 rclone core 版本 | 保留上游 module path、`rclone.conf`、`RCLONE_*` 环境变量及默认配置/缓存目录；不注册 `selfupdate`，避免误更新成官方 rclone |

更完整的使用和边界说明见 [fclone compatibility reference](fclone-compatibility.md)。
上游升级时的补丁面管理见 [upstream maintenance guide](upstream-maintenance.md)。

## 审计方法

为了避免把上游同期变更误算成 fclone 魔改，本文使用下列 Git 操作交叉验证：

```console
git ls-remote https://github.com/mawaya/rclone.git refs/heads/fwwkr/master
git show --no-patch --format='%H %P %aI %s' 3318f04941bdb7f903226fc28074c4282a91d1f9
git describe --tags --always 4a001b8a02808912cb3e1e67ac964e225a108062
git diff --shortstat 4a001b8a02808912cb3e1e67ac964e225a108062 88050fc0300a0974604dd7083fe9a33440a9450e
git diff --numstat 4a001b8a02808912cb3e1e67ac964e225a108062 88050fc0300a0974604dd7083fe9a33440a9450e
git diff --name-status 4a001b8a02808912cb3e1e67ac964e225a108062 88050fc0300a0974604dd7083fe9a33440a9450e
```

功能判定来自最终树的
[`backend/drive/drive.go`](https://github.com/mawaya/rclone/blob/88050fc0300a0974604dd7083fe9a33440a9450e/backend/drive/drive.go)、
[`fs/sync/sync.go`](https://github.com/mawaya/rclone/blob/88050fc0300a0974604dd7083fe9a33440a9450e/fs/sync/sync.go)和
[`fs/accounting/stats.go`](https://github.com/mawaya/rclone/blob/88050fc0300a0974604dd7083fe9a33440a9450e/fs/accounting/stats.go)，
并用相关里程碑提交复核演化过程。提交说明中的 `apply gclone mod` 只用于记录该仓库
自述的来源；本文不根据这句说明扩大推断具体代码的原始作者关系。
