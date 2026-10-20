# Cloudflare 部署指南

这份指南使用私有 R2 bucket、Worker 和 SQLite Durable Objects，不需要购买 VPS 或绑定自己的域名。操作在你的 Cloudflare 账户内完成；不要把 R2 Secret Access Key、邀请令牌或 Wrangler 登录凭据贴进聊天或提交 Git。

## 1. 开通 R2，创建私有 bucket

1. 登录 [Cloudflare 控制台](https://dash.cloudflare.com/)。
2. 进入 **Storage & databases → R2 → Overview**，完成 R2 开通流程。R2 有免费用量，但需要开通按量计费订阅，可能要求添加付款方式。
3. 创建名为 **`mywormhole-transfers`** 的 bucket，选择 **Standard** 存储。
4. 保持 bucket 私有：不要启用公开 `r2.dev` 地址或 Public Access，也不需要自定义域名、CORS。
5. 为首次测试可选择 Asia-Pacific 位置提示；这是性能提示而非具体机房保证。后续可以新建另一个北美位置提示的 bucket 做对比；桶的位置不应视作跨境加速承诺。

若采用其他 bucket 名，需要同步修改 `worker/wrangler.jsonc` 中 `r2_buckets[0].bucket_name` 和 `vars.R2_BUCKET_NAME`。

官方资料：[开通 R2](https://developers.cloudflare.com/r2/get-started/)、[数据位置](https://developers.cloudflare.com/r2/reference/data-location/)。

## 2. 添加自动过期规则

在 bucket **Settings → Object lifecycle rules** 中添加规则：

| 字段 | 值 |
| --- | --- |
| Rule name | `expire-transfer-chunks` |
| Prefix | `transfers/` |
| Action | Delete objects |
| Age | 1 day |

正常完成后程序立即删除文件块；会话默认 2 小时过期时，Durable Object alarm 会再次清理。生命周期规则负责兜底，包括清理已签发 URL 的迟到上传；生命周期删除是异步操作，不保证精确在第 24 小时立即执行。

官方资料：[对象生命周期](https://developers.cloudflare.com/r2/buckets/object-lifecycles/)。

## 3. 创建 R2 访问凭据

在 R2 Overview 进入 **Manage R2 API Tokens → Create API token**。选择 **Object Read & Write**，权限仅限 `mywormhole-transfers` 这个 bucket。

保存以下三个值：

| 值 | 用途 |
| --- | --- |
| Account ID | `R2_ACCOUNT_ID` |
| Access Key ID | `R2_ACCESS_KEY_ID` |
| Secret Access Key | `R2_SECRET_ACCESS_KEY` |

这里使用的是 S3 兼容 Access Key ID / Secret Access Key，不是把控制台显示的 Cloudflare API Token 字符串当作 Access Key ID。Secret Access Key 通常仅在创建时显示。

长期凭据只放在 Worker secrets 中。客户端拿到的只是某个文件块的短期上传或下载 URL；上传 URL 签名还绑定块长度，不能用来任意增大对象。

官方资料：[R2 认证与令牌](https://developers.cloudflare.com/r2/api/tokens/)、[签名 URL](https://developers.cloudflare.com/r2/api/s3/presigned-urls/)。

## 4. 登录 Wrangler

在 `mywormhole` 项目目录打开 PowerShell。辅助脚本会自动选择已安装的 Node 或项目 `.tools/` 内的便携 Node：

```powershell
.\scripts\cf.ps1 login
.\scripts\cf.ps1 whoami
```

`login` 会打开浏览器，让你自行登录 Cloudflare 并授权 Wrangler。若是新环境，先在 `worker` 目录运行 `npm ci`。

macOS / Linux 可以在 `worker` 目录用 `npx wrangler ...` 运行本指南中的对应命令。

## 5. 设置四个 Worker secrets

先生成一个随机邀请令牌。也可以使用密码管理器生成至少 32 字节随机值。下面的 PowerShell 命令兼容 .NET 的随机数生成器：

```powershell
$inviteBytes = New-Object byte[] 32
$inviteRng = [Security.Cryptography.RandomNumberGenerator]::Create()
$inviteRng.GetBytes($inviteBytes)
$inviteRng.Dispose()
$myInviteToken = [Convert]::ToBase64String($inviteBytes)
```

将 `$myInviteToken` 保存到自己的密码管理器，再运行以下命令。Wrangler 会分别提示输入值：

```powershell
.\scripts\cf.ps1 secret put INVITE_TOKEN
.\scripts\cf.ps1 secret put R2_ACCOUNT_ID
.\scripts\cf.ps1 secret put R2_ACCESS_KEY_ID
.\scripts\cf.ps1 secret put R2_SECRET_ACCESS_KEY
```

第一个值输入刚生成的随机邀请令牌，其余三个输入上一步保存的 R2 凭据。若 Wrangler 提示 Worker 尚不存在并询问是否创建同名 Worker，选择创建。不要把本地开发示例令牌用到生产环境。

也可先运行 `deploy` 创建 Worker，再从 Dashboard → Workers & Pages → `mywormhole` → Settings → Variables and Secrets 添加这四个 Secret。完成配置前 `/health` 会显示 `ready: false`，传输接口会拒绝请求。

## 6. 部署

```powershell
.\scripts\cf.ps1 deploy
```

Wrangler 自动根据配置创建 `ROOMS` 的 SQLite Durable Object namespace，绑定已经创建的 `mywormhole-transfers` bucket。无需单独购买 Workers Paid。使用代码中的 `new_sqlite_classes` migration，不要替换为旧式 KV Durable Objects。

部署成功后记下打印的地址，例如 `https://mywormhole.your-subdomain.workers.dev`。如果第一次启用 Workers 时要求创建 workers.dev 子域，按提示创建。

检查：

```powershell
Invoke-RestMethod 'https://mywormhole.your-subdomain.workers.dev/health'
```

预期包含 `service: mywormhole`、`protocol: 1`、`ready: true`、`directR2: true`。`ready` 只检查配置是否齐全，R2 凭据权限是否正确仍需实际小文件传输确认。

**无需创建 TURN 服务、Appwrite 项目或公开 R2 bucket。**

## 7. 编译并开始传输

```powershell
.\scripts\build.ps1
$env:MYWORMHOLE_SERVER = 'https://mywormhole.your-subdomain.workers.dev'
$env:MYWORMHOLE_INVITE_TOKEN = $myInviteToken
.\dist\mywormhole.exe send 'C:\Files\test.zip'
```

朋友只需对应平台的 CLI 文件和服务地址：

```powershell
$env:MYWORMHOLE_SERVER = 'https://mywormhole.your-subdomain.workers.dev'
.\mywormhole.exe receive 483921-amber-brook-cloud-river
```

朋友若也要发送，才需要邀请令牌。分享程序时一并附上 `dist/THIRD_PARTY_NOTICES.md`。

## 免费额度与排错

R2 Standard 当前免费量为 10 GB-month 存储、每月 100 万次 Class A 和 1,000 万次 Class B 操作，出站流量不收费。超出免费存储或操作额度会收费；临时存储和及时清理降低存储占用，并不构成硬性零费用上限。建议在自己的控制台关注 R2 用量和账单提醒。[官方价格](https://developers.cloudflare.com/r2/pricing/)

| 现象 | 检查 |
| --- | --- |
| 创建传输返回 401 | 发送方 `MYWORMHOLE_INVITE_TOKEN` 是否与 Worker `INVITE_TOKEN` 一致 |
| 返回 503 / ready false | 四个 secrets 是否齐全、邀请令牌是否至少 24 个字符 |
| 上传返回 403 | R2 Account ID、Access Key / Secret、bucket 名和 bucket 范围权限；两台电脑的系统时间 |
| 接收码返回 409 / 410 | 已有接收方、错误码导致会话关闭或会话过期；让发送方重新生成码 |
| PAKE confirmation failed | 接收码密码不一致或协商被修改；该次配对不能重试，重新发送 |
| upload chunk / download chunk 失败 | 先检查网络，尝试 `--parallel 2`；关闭进程后可从各自状态文件恢复 |
| 无法 publish 文件 | 输出目录需支持硬链接（如 NTFS/APFS/ext4）；选择本地磁盘目录，程序不会覆盖同名文件 |
| 中国大陆连不上 workers.dev | 用自己的实际网络验证服务地址可达性；可测试自有域名，但域名本身不保证线路改善 |

首次实际云端传输先用一个小文件，确认 R2 签名 URL、上传、下载、SHA-256 和删除全部成功，再测 1 GB。为了比较线路，记录完整耗时和两端运营商；本项目不会承诺未经实测的跨境速度。
