# fclone

这是一个以 rclone v1.75.0 开发线（上游提交 `17629d67b`）为当前基线、
重新实现历史 fclone/gclone 实用能力的
兼容分支。它不是对 2020 年旧代码的直接移植：新实现保留 rclone 的配置、命令和
后端兼容性，同时修复旧实现中 Service Account 并发切换、目录预创建和构建版本
注入等问题。

当前提供：

- Google Drive 多 Service Account 目录池、预加载、轮询与配额错误自动换号；
- `remote:{ID}`、`remote:{Google Drive URL}` 形式的文件、文件夹与共享盘直连；
- `backend lsdrives`、`add-drive`、`delete-drive` 兼容命令；
- `--check-first` 检查完成后，为 Google Drive 目标预创建传输所需目录；
- `Files/s`、按文件数估算的 ETA，以及 fclone/rclone core 双版本输出。

## 构建

最低需要 Go 1.25，发布构建推荐 Go 1.26.5：

```console
make fclone
./fclone version
```

所有 fclone 构建都不注册上游 `selfupdate` 命令，防止把 fclone 替换成官方 rclone。

## 配置兼容

默认继续使用 `rclone.conf`、`RCLONE_*` 环境变量和 rclone 原有配置/缓存目录。
建议第一次运行时显式指定现有配置，先只读检查，再 dry-run：

```console
fclone --config /path/to/rclone.conf lsd remote:
fclone --config /path/to/rclone.conf copy --dry-run source: destination:
```

Service Account 池示例：

```ini
[drive]
type = drive
service_account_file_path = /path/to/service-accounts
service_account_min_sleep = 100ms
services_preload = 50
services_max = 100
```

ID/URL 直连示例：

```console
fclone copy 'drive:{FILE_ID}' ./download
fclone copy 'drive:{FOLDER_ID}' destination:
fclone lsf 'drive:{https://drive.google.com/drive/folders/FOLDER_ID}'
```

完整的行为边界、迁移说明和已知差异见
[兼容性参考（英文）](docs/fclone-compatibility.md)。当年到底改了哪些代码、对应提交、
净差异和旧实现缺陷，见[历史魔改分析](docs/historical-analysis.md)。

本项目基于 MIT 许可的 rclone。历史行为参考 mawaya/rclone 和 donwa/gclone，
兼容层按当前 rclone API 重新实现；详细归属见 [NOTICE](NOTICE)。
