# Windows 发送和接收配置

把 `mywormhole-windows.zip` 发给朋友，再通过私密沟通渠道告诉朋友：

- 你部署后的 Cloudflare Worker 地址。
- 部署时设置的 **`INVITE_TOKEN`**。

安装包包含 Intel/AMD 64 位及 ARM64 程序、配置脚本、校验清单和说明，不含个人配置或 R2 密钥。

当前单文件上限为 **10 GiB（10,737,418,240 字节）**。从 2 GiB 版本升级时，发送方和接收方都需解压新版包并重新运行 `setup-windows.cmd`；按回车可保留已有地址和令牌。部署者还需更新 Worker，线上 `/health` 的 `maxFileBytes` 应为 `10737418240`。

## 配置一次

1. 右键 ZIP，选择“全部解压缩”。
2. 在解压后的文件夹中，普通双击 **`setup-windows.cmd`**。
3. 按提示输入 Worker 地址和 `INVITE_TOKEN`。令牌不会明文显示。
4. 看到“配置完成”后，关闭安装窗口，并重新打开终端。

脚本会自动识别芯片，核对 EXE 的 SHA-256、检查程序启动与服务 `/health`，为当前用户安装命令并保存配置。

也可以在解压目录的 PowerShell 中运行：

```powershell
.\setup-windows.cmd
```

该入口只对当前启动的 PowerShell 进程设置脚本运行策略，不永久修改系统执行策略。

## 发送、接收

发送：

```powershell
mywormhole send 'D:\Files\archive.zip'
```

把程序输出的完整接收码告诉对方。接收：

```powershell
mywormhole receive 483921-amber-brook-cloud-river
```

指定保存位置：

```powershell
mywormhole receive --output 'D:\Downloads\archive.zip' 483921-amber-brook-cloud-river
```

运行 `mywormhole receive` 也可以按提示输入接收码。`mywormhole version` 用于检查命令安装。

## 保存和更新

| 内容 | 默认路径 |
| --- | --- |
| 命令入口 | `%LOCALAPPDATA%\mywormhole\bin\mywormhole.cmd` |
| 实际程序 | `%LOCALAPPDATA%\mywormhole\app\mywormhole.exe` |
| 配置 | `%LOCALAPPDATA%\mywormhole\config\settings.json` |

邀请令牌使用 **Windows 当前用户 DPAPI** 加密，配置目录仅授予当前用户访问。启动器在运行时解密，将配置作为当前 CLI 进程的环境变量传入。令牌不会写进永久环境变量、启动器源码或安装包。地址和加密令牌作为一份 JSON 原子更新。

配置绑定当前 Windows 用户和机器，朋友应在自己的账户中运行安装脚本。请通过 `mywormhole` / `mywormhole.cmd` 使用保存的配置；直接运行 `app\mywormhole.exe` 时，程序只读取调用者提供的环境变量。

修改地址、令牌或更新程序时，重新运行 `setup-windows.cmd`，按回车可保留原配置。用户 PATH 保留原内容，相同安装目录不会重复添加。此前手工设置的环境变量可以继续保留，启动器会为本次运行加载保存的值。

新终端仍找不到命令时，关闭整个 Windows Terminal 后重新打开，或直接使用完整路径：

```powershell
& "$env:LOCALAPPDATA\mywormhole\bin\mywormhole.cmd" version
& "$env:LOCALAPPDATA\mywormhole\bin\mywormhole.cmd" send 'D:\Files\archive.zip'
```

## 高级参数

在 PowerShell 中直接运行配置脚本，预填地址：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install-windows.ps1 -Server https://你的服务地址.workers.dev
```

暂时无法联网、只想保存配置：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install-windows.ps1 -SkipHealthCheck
```

脚本还支持 `-InstallRoot` 指定安装目录、`-SkipPathUpdate` 保留 PATH 原值、`-Architecture amd64/arm64` 手动选择芯片。`-NonInteractive` 用于复用已有配置；自动化传入令牌使用 `-InviteToken` 的 `SecureString` 参数，不将明文令牌放进命令行。

服务检查不验证邀请令牌是否正确。发送返回 401 时，重新运行脚本输入服务端的 `INVITE_TOKEN`。

## 开发者打包和测试

在项目根目录运行：

```powershell
.\scripts\package-windows.ps1 -Rebuild
.\test\windows-installer.test.ps1
```

得到 `dist/mywormhole-windows.zip`。Windows PowerShell 5.1 和 PowerShell 7 均支持配置脚本。测试使用项目缓存目录、假健康检查和测试 EXE，不修改真实用户的 PATH 或程序安装目录。

`dist/` 是本机构建产物，不提交 Git；源码、安装脚本和打包方法保存在 GitHub，ZIP 可以单独发给朋友。
