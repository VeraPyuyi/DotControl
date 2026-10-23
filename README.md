# DotControl

本地生命周期重建与内部配对基础见 [macOS 生命周期与配对说明](docs/MACOS_LIFECYCLE_PAIRING.md)。当前仍以官方私有插件/隧道为主路径；一次性设备配对接口仅是内部准备，不代表已实现无插件控制。签名公证、Mac 真机、源 Dot 图片与用户 Mac 执行仍待验收。

让官方 Dot 通过每台设备自己的私有 MCP 插件与安全隧道，操作另一台 Windows 或 Mac。无需自建云服务器，不复制官方账号凭据或私有记忆；本地会话只在用户启用共享后按已保存范围只读返回。

**0.2 系列：跨电脑终端任务与只读会话共享。** 页面分为概览、连接设置、Codex 和会话共享。内置 SSH／实验助手和旧审批转发已下线；用户原有配置、密钥及记录保留。[alpha.7](https://github.com/VeraPyuyi/DotControl/releases/tag/v0.2.0-alpha.7) 已发布，包含[复盘中的七项修复](docs/REVIEW_0_2.md)和用户级后台恢复。安装包及实际验证范围见[发布支持矩阵](docs/RELEASE_SUPPORT.md)。

## 版本状态

默认分支 `main@a5d66d0` 保留 alpha.2 历史；公开安装包为 [alpha.7](https://github.com/VeraPyuyi/DotControl/releases/tag/v0.2.0-alpha.7)，构建源 `3424e41`。本地改进基于 PR #1 最新发布分支 `971def0`，不是新的已发布安装包。PR #2 的原生失败脱敏分类已复用；旧 LaunchAgent 安装接口不能覆盖 alpha.7 的后台状态机。

事实入口为 [发布支持记录](docs/RELEASE_SUPPORT.md)和其中的发布 JSON。运行 `python scripts/release_status.py` 从本地 tag 与记录生成可重复的分支／Release／资产／待验收表；`--api-json releases.json` 可读取保存的 GitHub releases API 响应，测试使用离线 fixture。13 个工具不等于 13 项均通过真机验收，终端回传不证明桌面、图片和 Mac 执行通过。新增能力及外部待验收见[诊断与验收边界](docs/RELEASE_DIAGNOSTICS.md)。

## 快速开始

从 [GitHub Releases](https://github.com/VeraPyuyi/DotControl/releases) 下载对应安装包：

1. **安装**：Windows 打开安装 EXE；Mac 打开对应架构的 DMG，再打开 DotControl Setup.app。无需 Go、Python、Homebrew 或 Xcode。已有设备身份和动作记录会保留。
2. **连接账号**：向导打开官方页面，为此设备建立独立隧道、运行密钥和私有插件。密钥仅填入本地隐藏输入框，保存在系统凭据库。
3. **验证并启用**：Windows 可选择仅连接本机 Codex，不要求桌面驱动或 SSH 配置；需要窗口操作时再检查驱动和系统权限。历史读取单独启用，源 Dot 需刷新插件工具目录。

本次 alpha 没有发布者签名或公证。系统提示、官方登录与权限确认仍需用户完成。第三方驱动签名失败会停止安装，不重新签名驱动或关闭系统安全检查。

网络通常无需修改。自动模式先读取进程中适用于 HTTPS 控制连接的代理，再读取当前用户系统中适用于该连接的静态代理及绕过规则；本机连接始终直连。新设置在隧道下次启动时应用，不热切换正在运行的客户端。PAC、仅 SOCKS 和需要交互认证的代理不受支持，具体状态及处理入口见[网络连接说明](docs/NETWORK.md)。云端在线要求当前隧道进程有近期成功轮询，`readyz` 或 `doctor` 配置检查通过不能代替。

关闭向导不会停止代理。托盘／菜单栏提供状态、暂停、继续、修复和退出。登录启动运行在当前用户会话中，alpha.7 增加系统管理的异常退出恢复。发送 @ 目标插件的连接请求后，先用 devices 核对目标和能力；完全停止或休眠的目标不能仅靠 @ 唤起。修复和重启不解除暂停或撤销。

已有 Codex 可安装仓库中的 **DotControl 安装助手**，说“帮我安装并连接 DotControl”。它调用同一安装器，并通过宿主正常浏览器工具协助填写官方页面。详见[安装指南](docs/INSTALL.md)。

## 使用

还可启用 [Codex 会话只读共享](docs/HISTORY_SHARING.md)：首次启用默认共享目标电脑全部本地会话；本机页面或 `share-codex-history` 技能可按聊天、项目或 Sidebar Organizer 分组选出清单，预览确认后才生效。分组变化不会自动授权。升级后在官方插件设置刷新工具，核对新增的三个历史接口，共 13 个工具。

保持 Dot 连接源电脑，对它说：

> 使用目标电脑的 DotControl 插件，先核对设备名称和本次 nonce。保持官方连接在当前源电脑，不切换目标。观察指定窗口，完成已授权操作，并回传实际结果与原始图片。

每次动作绑定设备、任务、稳定动作编号和新观察。网络重试保持相同编号；unknown 表示效果不确定，必须先核对，不得重新点击或改写历史。结果验证和源 Dot 图片显示分别记录。

有人使用电脑时，只允许经过执行器明确支持的后台动作，并避开正在使用的目标窗口。前台操作默认要求连续空闲 60 秒；人工接管、锁屏或活动检测失效时停止后续输入。后台能力因应用而异。

## 可选 Dot 任务桥接（开发分支）

[Dot 任务桥接](docs/DOT_BRIDGE.md)允许本机客户端提交持久任务，由连接 ChatGPT 的 Dot 通过 MCP Events 收到通知，再领取并回写结果。回答与后台任务分别验证；Magpie 集成使用独立配对凭据。该功能尚未发布，也尚未完成真实 ChatGPT／Dot 往返验收。它与下面的目标端 Codex 委派使用不同的执行路径。

## 目标端 Codex 委派

实现路线：源 Dot → 目标 DotControl → 目标 Codex → 官方 Computer Use／本机 SSH。

本版支持[Windows 终端安装](docs/TERMINAL_INSTALL.md)：本机点击“连接并启用 Codex”，无需先配置 SSH、服务器或桌面权限。源 Dot 通过四个 Codex 接口提交、查询、续聊和取消任务。已配置设备不会因升级扩大权限；旧审批依赖配置需要在本机明确重新配置。官方 Computer Use 继续关闭。Windows Codex 可以按用户要求自行使用本机网络和 SSH。详见[协议与限制](docs/CODEX_DELEGATION.md)。

## 已有跨电脑证据

0.1 安装实例已在真实官方 Dot 中完成双向核心场景，源 Dot 的官方连接保持在源电脑：

- Windows → Mac：计算器 6×8=48，五条回执 verified，原图在源 Dot 实际显示。
- Mac → Windows：4×9=36，三条 verified、两条 unknown；通过新观察继续，没有重发未知动作，最终原图实际显示。

这些记录不能替代 0.2 新安装包的验收。中文编辑、浏览器、多屏、物理接管、长期稳定性等完整矩阵仍见[验收记录](docs/validation/ACCEPTANCE.md)与[跨电脑验收](docs/CROSS_DEVICE_ACCEPTANCE.md)。

## 开发

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/dotcontrol
```

构建机使用 Go 与 Python；Mac 构建机另需 Swift。用户安装包包含预编译组件。使用 `scripts/package_release.py` 构建本机平台的自包含安装包，GitHub Actions 覆盖 Windows x64、Mac arm64 与 Intel。

[架构](docs/ARCHITECTURE.md) · [协议](docs/PROTOCOL.md) · [安装引擎](docs/INSTALL_ENGINE.md) · [实施记录](docs/IMPLEMENTATION_0_2.md) · [锁定依赖](dependencies.lock.json) · [许可证](THIRD_PARTY_NOTICES.md)

MIT License。DotControl 是独立开源项目，与 OpenAI、Cua 没有官方隶属关系。可选旧网关仍属开发功能，本版不做网关产品化。
