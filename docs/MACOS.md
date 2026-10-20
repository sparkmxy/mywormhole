# Mac 发送和接收配置

部署者把 `mywormhole-mac.zip` 发给朋友，并通过自己的私密沟通渠道告诉朋友：

- Cloudflare Worker 地址，例如 `https://mywormhole.example.workers.dev`。
- 部署时设置的 **`INVITE_TOKEN`**，用于允许发送文件。

安装包内没有预填任何个人令牌或 Cloudflare 账户密钥。程序会交互询问这两项配置。

## 朋友只需运行一次

1. 把 `mywormhole-mac.zip` 下载到 Mac 的“下载”目录。
2. 双击 ZIP 解压。Finder 通常会生成 `mywormhole-mac` 文件夹。
3. 打开“终端”，运行：

   ```bash
   bash "$HOME/Downloads/mywormhole-mac/install-mac.sh"
   ```

   如果解压目录不同，输入 `bash ` 后把 `install-mac.sh` 拖到终端，再按回车也可以。

4. 按提示粘贴 Worker 地址和邀请令牌。令牌输入不会显示字符，粘贴后按回车即可。
5. 安装完成后**新开一个终端窗口**。

脚本自动选择 Apple Silicon 或 Intel 程序，检查包内 SHA-256、测试程序能否启动、检查服务 `/health` 是否就绪，并保存发送/接收配置。它会检测 Rosetta 下的 Apple Silicon 硬件。

## 使用

发送文件：

```bash
mywormhole send "$HOME/Downloads/archive.zip"
```

把显示的完整接收码告诉对方。接收文件：

```bash
mywormhole receive 483921-amber-brook-cloud-river
```

默认保存到当前终端目录，指定保存位置：

```bash
mywormhole receive --output "$HOME/Downloads/archive.zip" 483921-amber-brook-cloud-river
```

也可以只运行 `mywormhole receive`，然后按提示输入接收码。`mywormhole version` 可检查命令是否安装成功。

## 更新或修改配置

重新运行同一个安装脚本即可更新程序与配置。已有地址和令牌可以直接按回车保留。脚本会保留 shell 配置原内容，相同的 PATH 配置只添加一次。

已知服务地址时，也可以预填地址：

```bash
bash "$HOME/Downloads/mywormhole-mac/install-mac.sh" --server https://你的服务地址.workers.dev
```

暂时无法联网、只想先保存配置时：

```bash
bash "$HOME/Downloads/mywormhole-mac/install-mac.sh" --skip-health-check
```

服务检查不验证邀请令牌是否与服务端匹配；令牌错误时发送命令会返回 401，重新运行脚本输入正确的 `INVITE_TOKEN`。

## 文件保存位置

| 内容 | 位置 |
| --- | --- |
| 命令启动器 | `~/.local/bin/mywormhole` |
| 实际程序 | `~/.local/share/mywormhole/mywormhole` |
| 服务地址和邀请令牌 | `~/.config/mywormhole/settings`（第一行地址，第二行令牌） |

配置目录权限为 `700`，配置文件为 `600`，令牌以明文保存在自己的私有目录。地址和令牌一起原子更新。启动器把配置作为环境变量传给程序，不执行配置文件内容。不要分享此配置文件。zsh 的 PATH 配置添加到 `~/.zshrc`，bash 添加到 `~/.bash_profile`；其他 shell 可直接用 `~/.local/bin/mywormhole`，或自行把该目录加入 PATH。

此前手工写入 `.zshrc` 的 `MYWORMHOLE_SERVER` / `MYWORMHOLE_INVITE_TOKEN` 可以自行删除；新启动器使用这里保存的配置。

## macOS 拦截自建程序时

Mac 二进制由本项目交叉编译，尚未进行 Apple 开发者签名或公证。如果系统阻止运行，先确认文件来自部署者，再按 [Apple 关于打开未知开发者应用的说明](https://support.apple.com/guide/mac-help/open-a-mac-app-from-an-unknown-developer-mh40616/mac) 处理并重跑安装脚本。程序启动测试失败时，脚本保留原有程序和服务配置。

安装脚本适配 macOS 自带 Bash 3.2，只使用系统命令。安装逻辑在模拟的 Mac 环境中验证；实际 macOS 启动、系统安全提示和跨境传输仍需在朋友的 Mac 上确认。

## 部署者重新打包

在项目根目录运行 PowerShell：

```powershell
.\scripts\package-mac.ps1 -Rebuild
```

得到 `dist/mywormhole-mac.zip`。包内包含两种 Mac 芯片的程序、安装脚本、SHA-256 清单、说明和依赖许可。
