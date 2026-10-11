# 安装、连接与诊断

## Windows

使用当前登录用户的 PowerShell 运行 `scripts/Install-Windows.ps1`。可以用 `-Go` 指定已有 Go 编译器，`-InstallDir` 指定程序目录，`-StateDir` 指定隔离测试状态目录，`-DeviceName` 指定显示名称，`-TunnelId` 填已有隧道编号。脚本下载固定 Cua/tunnel-client 官方发布包，核对 SHA-256 后解压，构建代理；没有管理员、服务或注册表修改。

默认程序目录 `%LOCALAPPDATA%\DotControl`，状态目录 `%APPDATA%\DotControl`。不要删除状态目录来重试失败动作。凭据保存在 Windows 凭据管理器，状态目录受当前用户 ACL 保护。通过 `--state ABSOLUTE_DIRECTORY` 可以使用隔离的演示身份；同一身份的每个命令都应使用相同目录。

安装完成后执行 `dotcontrol doctor`，检查驱动、隧道版本与 activity.available。`serve --no-tunnel` 启动本地验证。真实操作必须在登录会话进行，不要把代理作为 Session 0 的系统服务运行。锁屏会阻止动作。

## macOS 14+

已有 `DotControl-Mac.zip` 时，先按 [Mac 本机开始验收](MAC_START_HERE.md) 操作。包内含双架构代理与固定版驱动，首轮可通过 `DOTCONTROL_SKIP_TUNNEL=1` 跳过云端安装；这条路径不需要 Go/Homebrew，仍需 Apple 命令行工具编译活动 helper。设置 `DOTCONTROL_FROM_SOURCE=1` 可明确选择下面的源码构建路径。

需要 Xcode Command Line Tools、Go、官方 Homebrew。`bash scripts/install-macos.sh 'MacBook'` 下载固定 Cua 包、安装稳定位置的 CuaDriver.app，编译 DotControlActivity.app 和代理。官方 tunnel-client 使用官方 Homebrew 分发；版本不等于本项目固定版本时停止，请先核对新版，不移除 quarantine、不跳过 Gatekeeper。

默认程序与状态目录都是 `~/Library/Application Support/DotControl`。CuaDriver.app 必须位于 `/Applications/`：固定版本 0.34.1 的授权辅助程序使用这个绝对路径。DotControlActivity.app 仍位于 `~/Applications/`。安装脚本保留已有 CuaDriver.app 并核对版本，避免另一项目的驱动被覆盖。若早期安装包已将 CuaDriver 放在 `~/Applications/`，先停止代理和驱动，再通过 Finder 移到 `/Applications/`；目标已有应用时先核对版本，不要覆盖。

依上游说明，在系统设置中由用户为 **CuaDriver.app** 授予辅助功能和屏幕录制权限，为 **DotControlActivity.app** 授予输入监控。权限归属以实际系统提示为准；开发版 helper 为 ad-hoc 签名，升级可能要求重新确认。代理不会尝试绕过权限。

先以稳定应用身份启动 Cua 服务：

```sh
open -n -g /Applications/CuaDriver.app --args serve
/Applications/CuaDriver.app/Contents/MacOS/cua-driver permissions grant
/Applications/CuaDriver.app/Contents/MacOS/cua-driver permissions status --json
"$HOME/Library/Application Support/DotControl/bin/dotcontrol" doctor
"$HOME/Library/Application Support/DotControl/bin/dotcontrol" serve --no-tunnel
```

用户提供的 Mac 诊断已确认代理与原生活动监控运行，但本轮安装路径修复、Cua 的真实系统权限及桌面操作尚待 Mac 验收；Go 的双架构交叉构建不能替代这些步骤。若 activity.available=false，停止操作，先解决权限/会话归属。

## 官方 Dot 连接

1. 访问 [Platform Tunnels](https://platform.openai.com/settings/organization/tunnels)，为每台设备创建一个独立隧道。管理者需要 Read + Manage；运行凭据和连接用户需要 Read + Use。
2. 在状态目录的 `config.json` 填 `tunnel_id`。这里仅保存编号和路径，不填任何 key。账号创建的是 runtime API key，不把管理 key 用于常驻进程。
3. 在交互终端执行 `dotcontrol credential`，使用隐藏输入保存 runtime key。不要粘贴到聊天、命令行参数、Git 或日志。高级自动化可通过临时 `DOTCONTROL_RUNTIME_KEY` 进程环境输入，使用后清除。
4. `dotcontrol serve` 会管理自己的官方 tunnel-client 子进程。不要另开同一隧道的进程指向其他设备。运行 `dotcontrol status`，区分 agent running、tunnel running、tunnel ready；再运行 `doctor --online`。
5. 在 [ChatGPT Plugins](https://chatgpt.com/plugins) 的自定义 MCP 入口选择 Tunnel，选中该设备独立隧道并命名，例如“DotControl 办公室 Windows”。如果账户没有入口或权限，记录阻塞，不改用其他助手冒充验收。
6. 按 [验收步骤](validation/ACCEPTANCE.md) 从真正的官方 Dot 调用 devices/nonce、observe 图片和测试窗口动作。插件创建成功仍不等于 Dot 完成了任务。

本地 `/mcp` 的认证令牌仅用于隧道到本机代理，不需要贴入插件文本描述。官方 tunnel-client 接收 `env:` 凭据引用，DotControl 通过子进程环境注入。

插件的身份验证选择“无需身份验证”：这里表示不另加应用 OAuth。访问仍受官方隧道的账号/工作区授权约束，本机 `/mcp` 仍校验隧道注入的令牌；不要将本机端口公开到互联网。

若网页能访问 Platform，而 `doctor --online` 连接超时，检查终端是否使用了已有网络代理。官方客户端继承当前进程的 `HTTPS_PROXY`、`HTTP_PROXY` 和 `NO_PROXY`，不会自动读取 Windows 浏览器代理。仅在当前终端填写自己已配置的代理地址，并将 `localhost,127.0.0.1,::1` 加入 `NO_PROXY`，再启动代理。不要把含代理密码的地址写进仓库或报告。

## 常驻与退出

预览版显式运行 `serve`；停止时先 `pause`，再在运行代理的终端按 Ctrl+C。关闭后暂停状态保留，下次启动需要 `resume`。终端不会自动设置登录启动。可自行把此命令配置为**用户登录后**的任务/LaunchAgent，启动仍遵循暂停状态；不要让系统服务控制未登录桌面。

`revoke` 会暂停、关闭隧道、删除本机 tunnel key 并轮换本机 MCP token。云端的 key 和插件还需在官方平台撤销；网关配对需在双方 `pair revoke DEVICE_ID`。这些是不同的授权边界。

## 常见结果

| 结果 | 处理 |
| --- | --- |
| activity 不可用或 locked | 检查当前用户会话、系统权限与 helper；不要强制视为空闲 |
| driver_needs_user | 背景路径、权限或动作能力不足；说明目标步骤，重新观察；Mac 长控件文本改用 set_value |
| unknown | 核对界面/文件；不要换编号直接重复发送 |
| stale observation / window changed | 重新 observe，不复用旧坐标或 token |
| state_locked | 使用已运行实例；不要删除锁和动作记录绕过 |
| doctor 缺少 tunnel id/key | 补齐官方平台配置；本地工作不证明云端权限 |
| Mac SSH 超时 | 网络入口不可达，尚未进入密码认证阶段；检查 VPN/局域网/远程登录 |

驱动背景支持因应用而异。Cua 的部分 Mac 控件操作存在前台副作用，预览版会保守拒绝不能证明不改变焦点的路径。完整平台差异见 [上游说明](https://cua.ai/docs/cua-driver/concepts/platform-support)。
