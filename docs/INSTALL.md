# DotControl 0.2：安装和连接

先查看 [当前支持与验收状态](RELEASE_SUPPORT.md)。下载入口为 [GitHub Releases](https://github.com/VeraPyuyi/DotControl/releases)。只使用已发布的对应系统安装包，没有对应产物时不用开发构建代替。

## 1. 安装

- Windows 11 x64：打开 DotControl-0.2.0-alpha.2-windows-amd64-setup.exe。
- Mac Apple Silicon：打开 macos-arm64.dmg 内的 DotControl Setup.app。
- Mac Intel：使用 macos-amd64.dmg。

安装器自动校验随包组件、识别旧设备、安装到当前用户目录并启动本地向导。发现多个已有设备时选择要升级的设备，不删除状态目录重新安装。正常安装无需终端、Go、Python、Homebrew 或 Xcode。首次发布未配置发布者签名或公证，系统可能显示来源提示；上游 CuaDriver 签名校验失败则停止。

Mac 的 CuaDriver 固定放在 /Applications/CuaDriver.app。活动检测和菜单程序位于当前用户的 Applications。按向导打开辅助功能、屏幕录制和输入监控设置，由你完成系统授权。授权后点击“重新检查并修复连接”。若系统要求退出重开，应完成该步骤。

## 2. 连接账号

向导提供三个准确的官方入口，并可复制设备名称和隧道编号：

1. [创建 Secure MCP Tunnel](https://platform.openai.com/settings/organization/tunnels)：每台目标电脑使用独立的 tunnel_… 编号。
2. [创建运行密钥](https://platform.openai.com/settings/organization/api-keys)：仅授予 Tunnels 的 Read + Use 权限。把密钥填入本地向导的隐藏输入框，不要发到聊天、命令行、截图或日志中。
3. [添加私有插件](https://chatgpt.com/plugins)：选择自定义 MCP 服务器和 Tunnel，填写该设备名称与相同隧道编号，并完成官方确认。

点击“保存到本机并连接”。密钥保存在 Windows 凭据管理器或 Mac 钥匙串。已有配置时留空不会清除密钥。官方账号权限不足时保留当前进度，不需要组织管理员密钥。

安装 Codex 引导插件后，可以说“帮我安装并连接 DotControl”。它使用相同安装器和正常浏览器工具协助填写；登录、密钥录入和系统权限仍由你完成。插件源码位于 plugins/dotcontrol-onboarding，仓库同时提供 Codex marketplace 描述。

## 3. 验证并启用

点击“复制连接测试指令”，发给源电脑上的官方 Dot。保持官方连接在源电脑，让 Dot 通过目标设备的插件调用 devices，核对名称、设备 ID、随机值。

本机检查通过后，点击“启用桌面任务”。让 Dot 观察目标电脑已打开的无害窗口，再做一个有明确新状态的动作。动作使用新的观察与唯一动作编号；结果为 unknown 时先重新观察，不能重发。

只有实际在源 Dot 中看到原始图片后，才点击“我已在源 Dot 看到图片”。“隧道在线”“插件已调用”“MCP 返回图片”“源 Dot 显示图片”分别记录，不能相互替代。本机 Codex 委派在本版仍暂不可用：检测出官方 App Server 或工具不等于完成 Computer Use、审批、中断及图片验收。

## 日常管理与恢复

关闭网页不停止代理。通过 Windows 托盘或 Mac 菜单栏重新打开向导、暂停、继续、修复或退出。随用户登录启动，不作为系统后台桌面服务运行。修复保留身份、凭据、暂停/撤销状态和动作记录；连接失败退避重试，未知动作不重放。

脱敏诊断按钮下载版本、阶段、错误类别和必要状态，不包含密钥、输入正文或截图。签名失败、密钥失效、DNS、TLS、权限未知和暂停分别显示；不要通过关闭 TLS 校验、重签 CuaDriver 或改系统安全策略解决。

再次运行完整安装包可修复程序文件。旧版正在执行时先正常停止任务；没有新管理接口的 0.1 代理须先退出。已有网关配置的迁移目前拒绝执行，原配置保持不变。

## 高级维护

以下为维护命令，不是正常用户安装步骤。对同一设备始终使用同一个 --state 目录：

- dotcontrol install --bundle <预构建组件目录>
- dotcontrol onboard
- dotcontrol repair
- dotcontrol repair --bundle <预构建组件目录>
- dotcontrol uninstall
- dotcontrol codex-probe --binary <官方 Codex 绝对路径> --working-dir <独立诊断目录>

uninstall 只移除本项目管理的程序文件和登录启动设置，保留设备记录与凭据；不提供隐式清除。请勿手工删除账本以解决结果不确定。安装器的 --verify-payload 仅校验实际包内文件，不安装、不启用桌面任务。

0.1 源码安装和历史迁移细节已移到 [旧版说明](INSTALL_0_1_LEGACY.md)。
