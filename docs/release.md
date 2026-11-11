# 本地制作 Linux amd64 升级包

本工具在 Windows PowerShell 中生成主控、Agent 两个 `.tar.gz` 包以及 SHA256 校验文件。发布包用于升级已有 PPGo 平台，不提供空数据库初始化，不创建 Git Tag、不上传 GitHub Release，也不自动运行或部署程序。

## 一条命令打包

需要 Git、Go 1.22 或更新版本，以及 Windows 自带的 `tar.exe`，三者均须在 PATH 中。

在仓库根目录执行：

```powershell
.\scripts\package.ps1 -Version v2.9.0 -Ref HEAD
```

`Version` 必填，接受 `v2.9.0`、`v2.9.1-rc.1` 等版本号；`Ref` 默认 `HEAD`，也可指定已存在的提交或标签。初次 `v2.9.0` 升级包的业务源码基线是 `176eae539d801d3693cd4b77e1203209f5d514e5`。

脚本通过 `git archive` 导出指定提交，不打包工作区未提交的代码和本地配置。源码必须包含当前主控、Agent、两份增量迁移和相应文档。脚本不会读取本机配置值来生成示例，也不会把实际数据库密码或邮箱凭据放入包中。

已有同版本输出目录时直接拒绝覆盖。需要另一个版本时使用新的版本号；不要将版本号更改误认为源码已经更新，应同时确认 `Ref`。脚本只清理自己创建的随机临时目录，不删除其他构建或发布产物。

## 输出内容

默认目录为 `PPGo_Job-package/v2.9.0/`，已被仓库现有的 `PPGo_Job*` 忽略规则覆盖：

| 文件 | 内容 |
|---|---|
| `PPGo_Job-v2.9.0-linux-amd64.tar.gz` | `PPGo_Job`、`views/`、`static/`、两份迁移、搜索/监控文档、`conf/app.conf.example`、`UPGRADE.md`、`BUILDINFO.json`、`LICENSE` |
| `ppgo_agent-v2.9.0-linux-amd64.tar.gz` | `ppgo_agent`、`config/conf.ini.example`、`UPGRADE.md`、`BUILDINFO.json`、`LICENSE` |
| `SHA256SUMS` | 两个压缩包的 SHA256；UTF-8 无 BOM、LF 换行，可用 Linux `sha256sum -c` 验证 |

主控和 Agent 的配置均只有 `.example`，不包含实际 `app.conf` 或 `conf.ini`。Agent 示例的 `ServerId` 为 0，注册地址为待替换占位地址，并包含 `MonitorDiskPath=/`。数据库连接使用占位账号/密码，邮件及其他通知入口为空。

构建使用 `GOOS=linux`、`GOARCH=amd64`、`CGO_ENABLED=0`，加上 `-trimpath -mod=readonly -buildvcs=false`。因为干净源码快照没有自身 Git 元数据，版本与完整提交号以包内 `BUILDINFO.json` 为准，同时记录 Go 版本、构建目标和 UTC 构建时间。

脚本检查两个程序的 ELF64/amd64 格式和 Go 构建信息，检查包内路径及禁止文件，实际解压并逐文件比对 SHA256，最后重新核对清单。全部通过后才一次性发布本地版本目录；不会运行 Linux 程序、连接数据库或发送邮件。

## 在 Linux 升级

先确认 `uname -m` 为 `x86_64`，将两个压缩包和 `SHA256SUMS` 放在同一目录：

```sh
sha256sum -c SHA256SUMS
tar -xzf PPGo_Job-v2.9.0-linux-amd64.tar.gz
tar -xzf ppgo_agent-v2.9.0-linux-amd64.tar.gz
chmod 0755 PPGo_Job-v2.9.0-linux-amd64/PPGo_Job
chmod 0755 ppgo_agent-v2.9.0-linux-amd64/ppgo_agent
```

Windows 的归档权限映射可能不同，因此解压后明确执行上述 `chmod`。目标服务器不需要安装 Go。

升级顺序：

1. 备份现有数据库、程序、配置和密钥，在维护窗口使用现有服务管理方式停止旧主控调度。
2. 尚未升级任务搜索的环境先执行 `20261109_task_search.sql`，然后执行 `20261110_server_monitor.sql`；自定义表前缀需要在同一数据库连接设置 `@ppgo_prefix`。不要导入旧 `ppgo_job2.sql`。
3. 替换主控程序、页面和静态资源，**保留原 `conf/app.conf` 和密钥**，不要用 `.example` 覆盖。继续使用原服务配置和工作目录，不同时运行两套调度器。
4. 在空闲时逐台替换 Agent，**保留原 `config/conf.ini` 和实例配置**；按需添加 `MonitorDiskPath` 指向实际数据目录。
5. 检查任务、权限和真实主机指标，再单独完成真实 SMTP 验收并显式开启资源告警。资源告警默认关闭；打包成功不等于生产运行验收完成。

详细步骤见包内 `UPGRADE.md` 和主控包内的监控文档。旧 `package.sh` 保留，但本次升级包请使用新脚本，避免遗漏 Agent、迁移或带入实际配置。

后续如需 GitHub Release，可选择对应源码提交创建版本标签，再上传两个压缩包与校验文件。这个步骤不会由本地打包脚本自动执行。
