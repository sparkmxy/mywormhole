# 验证记录

日期：2026-10-20。运行环境：Windows amd64，Go 1.27.3、Node.js 24.22.0，Cloudflare 本地 workerd / Miniflare，依赖版本由 go.sum 与 package-lock.json 固定。

## 已通过

| 检查 | 验证内容 |
| --- | --- |
| `go test ./...` | SPAKE2 正确密码共享密钥、错误密码与不同会话拒绝、角色分离确认、畸形 PAKE 输入、密文篡改与块交换拒绝、接收码格式、文件路径与大小限制 |
| `go vet ./...` | Go 静态检查 |
| `npm run check` | Worker TypeScript 类型检查 |
| `npm test` | 模拟器中四组服务端测试：权限与一次领取、消息不可变、文件计划限制、对象读写、ready/长度检查、删除清理、签名 URL 绑定长度与内容类型、缺少配置时拒绝服务 |
| 真实双 CLI 端到端 | 32 MiB + 137 字节文件、中文文件名、末尾不足一块的处理，上传/下载完成且 SHA-256 一致 |
| 空文件 | 0 字节传输、SHA-256 一致、正常完成回执 |
| 错误接收码 | 用词表中另一个合法单词替换密码词，双方拒绝协商，未上传数据块 |
| 不覆盖已有文件 | 同名输出文件保留原始内容 |
| 接收端恢复 | 收到部分块后终止进程；修改部分文件中的已完成块；新进程重新验证并下载损坏块，最终哈希一致 |
| 发送端恢复 | 上传部分块后终止进程；从原会话本地状态恢复，接收端正常完成，最终哈希一致 |
| 1 GiB 真实文件 | 1,073,741,824 字节，默认 16 MiB 块、4 路并发，完整传输后 SHA-256 一致，模拟 R2 对象清空 |
| Worker 打包 | `wrangler deploy --dry-run` 成功，识别 R2 binding、SQLite Durable Object binding 和 migration |
| 跨平台构建 | Windows amd64、Linux amd64/arm64、macOS amd64/arm64 交叉编译成功；仅在当前 Windows 上执行 CLI |

1 GiB 那次本地测试从创建测试文件到双端完成、校验与清理约 **97.9 秒**。它包含本机磁盘 I/O、模拟 R2、加密和多次哈希，不是中国至北美的网络测速，也不代表真实云端速度。

## 尚需真实部署验证

- 账户中的 R2 S3 凭据和 bucket 权限。
- Cloudflare 线上预签名 URL 的真实 PUT / GET 与时钟容差；已在本地检查签名字段，尚未调用用户的 R2。
- 中国和北美两端到 Workers / R2 的可达性、稳定性与完整耗时。
- 线上真实的生命周期规则和 alarm 执行时延。
- Linux 与 macOS 二进制的实际运行；当前仅完成交叉编译。

本次未登录用户的 Cloudflare 账户，未创建、修改或部署线上资源，也未进行独立密码学安全审计。

## 重跑

PowerShell：

```powershell
.\scripts\test.ps1
.\scripts\test.ps1 -Large
.\scripts\cf.ps1 deploy --dry-run --outdir ../dist/worker
```

测试数据写入项目 `.cache/e2e-*`。1 GiB 测试需要约 2.5 GB 空间。只在不再需要检查时删除自己生成的测试目录，勿将真实文件放进这些目录。

## Mac 配置脚本验证（2026-10-20）

`scripts/install-mac.sh` 经 Bash 语法检查，并在 Windows Git Bash 中使用模拟的 Mac 系统信息、假程序和假 `/health` 响应运行五组测试：

- Apple Silicon、Intel 和 Rosetta 场景的架构选择。
- 隐藏输入的令牌不会出现在脚本输出或 shell profile 中；包含引号、美元符和反斜杠的配置按纯数据传给程序。
- 含空格和特殊字符的安装路径、命令参数转发、重复安装保留原有 profile 且不重复添加 PATH。
- 配置更新采用单文件原子替换；服务未就绪时保留已有配置，离线选项可跳过服务检查。
- SHA-256 不匹配或程序无法启动时不保存新的凭据，临时文件会清理。

测试只使用项目 `.cache/` 内的隔离目录，没有修改真实用户主目录或调用线上 Cloudflare 服务。文件权限命令位于安装脚本中，POSIX 权限与 macOS 安全提示仍需在真实 Mac 上确认。

`scripts/package-mac.ps1 -Rebuild` 已重新编译两个 Mac 架构。最终 ZIP 按固定文件清单打包，并检查脚本与源码一致、LF 换行、两个二进制的 Mach-O CPU 类型，以及包内 SHA-256 清单。包内不含个人配置或云端凭据。

重新运行安装逻辑测试：`python test/mac_installer_test.py`（开发环境需 Python 3；Windows 另需 Git Bash）。

## Windows 配置脚本验证（2026-10-20）

`test/windows-installer.test.ps1` 已分别在 Windows PowerShell 5.1 与 PowerShell 7 下通过。测试使用真实 Windows 当前用户 DPAPI、真实 CMD → Windows PowerShell → 测试 EXE 的启动链，以及项目 `.cache/` 内的隔离安装目录：

- 配置中不出现明文令牌；私有配置目录停止继承 ACL，只保留当前用户的访问规则。
- 启动器可解密配置、传入正确的服务地址和令牌，并完整传递带空格的参数、保留 CLI 退出码。
- 带中文、单引号、美元符的安装路径正常；命令入口与底层 EXE 分目录，避免 EXE 覆盖 CMD 的命令查找。
- 重复运行可保留原配置并原子更新；服务未就绪或地址不合法时不改写原设置。
- 离线配置、错误 SHA-256 拒绝均通过；测试未修改真实用户 PATH 或实际安装目录。

通过显式加载当前 PowerShell 主机的内置安全模块，避免从 PowerShell 7 启动 Windows PowerShell 5.1 时误载另一版本模块。DACL 持久化只更新访问权限，不要求普通用户持有审计或所有者变更特权。

Windows 安装包包含重新编译的 amd64 和 arm64 EXE、PowerShell 安装器、CMD 入口、校验清单与说明。ZIP 检查包含文件白名单、源码一致性、PowerShell UTF-8 BOM、PE 架构和二进制 SHA-256；ARM64 只完成交叉编译和文件检查，尚未在 ARM64 Windows 机器运行。安装包仍属于 `dist/` 本机构建产物，不提交 Git。
