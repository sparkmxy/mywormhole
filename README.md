# mywormhole

给自己和朋友使用的命令行文件传输原型，面向中国与北美之间约 1 GB 的文件传输。无需租 VPS，后端运行在 Cloudflare Workers、Durable Objects 和 R2。

这版已经实现：

- `send` / `receive`，接收码如 `483921-amber-brook-cloud-river`。
- 现有 SPAKE2 库进行 PAKE，双方确认密钥后才发送文件信息和数据。密码部分不发送到服务端。
- AES-256-GCM 加密文件名、文件元数据和各个独立文件块。
- 默认 16 MiB 分块、4 路并发，上传完成一个块后接收端即可下载。
- 块级重试，双方进程均可凭本地状态文件恢复；接收端重新校验已经写盘的块。
- 整文件 SHA-256 校验、不覆盖已有输出文件、成功后删除云端文件块。
- 默认 2 小时会话过期，服务端定时清理，R2 生命周期规则作为补充。

当前支持一个普通文件，最大 10 GiB（10,737,418,240 字节，约 10.74 GB）；目录可以先打包。本版使用 R2 中转，后续可以在同一套分块协议上增加 P2P。初次配对需要双方在线。接收码沿用 Wormhole 的交互方式，协议不与原版 `wormhole` 互通。

从 2 GiB 版本升级时，发送端和接收端都需要更新 CLI，Worker 也需要重新部署。使用默认 16 MiB 分块即可传输 10 GiB（640 块）；自定义分块仍须满足最多 2048 块。

## 先部署 Cloudflare

按 [Cloudflare 部署指南](docs/CLOUDFLARE.md) 操作：开通 R2、建私有 bucket、创建仅限此 bucket 的 R2 凭据、设置 Worker secrets，再部署。你无需把任何凭据发送到聊天中。

项目附带 PowerShell 辅助脚本。如果这台电脑的 `.tools/` 目录仍在，脚本会自动使用其中的 Go 和 Node.js；该目录被 Git 忽略，不是发布内容。其他电脑从源码构建需要 Go 1.26+ 和 Node.js 22+。朋友只运行编译后的 CLI 时，不需要安装 Go、Node 或 Wrangler。

## 使用

Mac 朋友可使用 [Mac 配置说明](docs/MACOS.md) 中的一次安装脚本。部署者运行 `scripts/package-mac.ps1 -Rebuild` 生成可直接分享的 `dist/mywormhole-mac.zip`；朋友解压后运行安装脚本，按提示输入服务地址与邀请令牌，即可发送和接收。

Windows 用户可使用 [Windows 配置说明](docs/WINDOWS.md)：运行 `scripts/package-windows.ps1 -Rebuild` 生成 `dist/mywormhole-windows.zip`，解压后双击 `setup-windows.cmd`，即可配置发送和接收。令牌使用当前 Windows 用户的 DPAPI 加密保存，使用时自动加载。

双方设置部署后的服务地址。PowerShell：

```powershell
$env:MYWORMHOLE_SERVER = 'https://mywormhole.你的子域.workers.dev'
```

发送方额外设置部署时的 `INVITE_TOKEN`。它仅允许创建传输，与接收码中的临时密码不同：

```powershell
$env:MYWORMHOLE_INVITE_TOKEN = '你自己设置的随机邀请令牌'
.\dist\mywormhole.exe send 'C:\Files\archive.zip'
```

程序输出接收码。朋友运行：

```powershell
.\mywormhole.exe receive 483921-amber-brook-cloud-river
```

验证配对后会显示文件名和大小，输入 `y` 接收。也可以输入 `mywormhole receive` 后按提示输入接收码，避免把接收码放进命令行历史。接收方不需要邀请令牌或 R2 凭据。

指定文件名、自动接受、调整并发：

```powershell
.\dist\mywormhole.exe receive --yes --output 'D:\Downloads\archive.zip' 483921-amber-brook-cloud-river
.\dist\mywormhole.exe send --parallel 4 --chunk-mib 16 'C:\Files\archive.zip'
```

参数必须放在文件名、接收码或状态文件之前。命令默认最多运行 2 小时，可通过 `--timeout 90m` 缩短等待。

macOS / Linux 的对应环境变量写法：

```sh
export MYWORMHOLE_SERVER='https://mywormhole.你的子域.workers.dev'
export MYWORMHOLE_INVITE_TOKEN='你自己设置的随机邀请令牌' # 仅发送方
chmod +x ./mywormhole
./mywormhole send archive.zip
./mywormhole receive 483921-amber-brook-cloud-river
```

## 中断与恢复

配对后程序打印 `Resume state: ...`。网络故障先自动重试；关闭进程后可恢复：

```powershell
.\dist\mywormhole.exe resume 'C:\Users\你\AppData\Local\mywormhole\receiver-会话ID.json'
```

发送方也可以恢复自己的 `sender-会话ID.json`。两边恢复时各用自己的状态文件，无需重新配对。原始文件必须未发生改变，会话必须未过期；默认过期时间从创建接收码时算起，恢复不会延长它。

可以用 `--state 'D:\Private\transfer.json'` 指定状态文件位置。状态文件包含会话密钥和访问令牌，应保存在自己的私有目录，不能分享或提交 Git；POSIX 上按 `0600` 写入，Windows 继承目录 ACL。成功后程序会删除状态文件。过期或放弃传输后，可以自行删除相关状态文件与 `.part` 文件。配对尚未完成时关闭进程，需要发送方重新生成接收码。

## 本地开发与测试

依赖安装（普通 Node.js 环境）：

```powershell
cd worker
npm ci
Copy-Item .dev.vars.example .dev.vars
cd ..
.\scripts\build.ps1
.\scripts\cf.ps1 dev --local --port 8787
```

若本机只有项目内的便携 Node，可用 `.tools\node-版本-win-x64\npm.cmd` 替代 `npm`。本地开发令牌仅用于模拟器，不能部署到生产环境。

两个终端分别设置：

```powershell
$env:MYWORMHOLE_SERVER = 'http://127.0.0.1:8787'
$env:MYWORMHOLE_INVITE_TOKEN = 'local-development-only-change-me' # 仅发送方
```

本地模拟器使用 Worker 流式代理读写模拟 R2，生产默认直接通过短期签名 URL 访问 R2。HTTP 只允许用于 loopback 地址。

```powershell
.\scripts\test.ps1          # Go、Worker 及真实双 CLI 端到端测试
.\scripts\test.ps1 -Large   # 额外传输并校验真实的 1 GiB 文件
.\scripts\build.ps1 -All   # 生成 Windows、Linux、macOS 二进制
.\scripts\cf.ps1 deploy --dry-run --outdir ../dist/worker
```

macOS / Linux 源码构建与测试：

```sh
go test ./...
go vet ./...
mkdir -p dist
go build -trimpath -o dist/mywormhole ./cmd/mywormhole
cd worker
npm ci
npm run check
npm test
npm run test:e2e
```

测试使用本地 Cloudflare 模拟器，不需要账户或云端资源。1 GiB 测试会在项目 `.cache/` 下留下源文件与接收文件用于检查，需约 2.5 GB 可用空间。测试内容及实际验证边界见 [验证记录](docs/VALIDATION.md)。

## 协议与速度

详细数据流、PAKE 依赖、密钥分离及威胁边界见 [协议说明](docs/PROTOCOL.md)。

中美速度取决于两端运营商和到 R2 的实际线路。首次部署后建议先传 32 MiB，再传 1 GB，比较 `--parallel 1/2/4/8` 与 `--chunk-mib 4/16`。程序显示的是各自上传或接收阶段的平均速度，应另外记录从发送到完整校验结束的耗时。本地模拟器性能不能用于预测跨境性能。
