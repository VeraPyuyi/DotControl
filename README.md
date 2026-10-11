# DotControl

让官方 Dot 通过每台设备自己的私有 MCP 插件与安全隧道，操作另一台 Windows 或 Mac。无需自建云服务器，不复制官方账号凭据、私有记忆或会话。

**0.2.0-alpha.1：首个自动安装预发布版。** 本版提供自带组件的安装器、三阶段本地向导、登录启动、连接修复和 Codex 引导插件。当前支持状态以[发布支持矩阵](docs/RELEASE_SUPPORT.md)为准；构建通过不代表真机验收通过。

## 快速开始

从 [GitHub Releases](https://github.com/VeraPyuyi/DotControl/releases) 下载对应安装包：

1. **安装**：Windows 打开安装 EXE；Mac 打开对应架构的 DMG，再打开 DotControl Setup.app。无需 Go、Python、Homebrew 或 Xcode。已有设备身份和动作记录会保留。
2. **连接账号**：向导打开官方页面，为此设备建立独立隧道、运行密钥和私有插件。密钥仅填入本地隐藏输入框，保存在系统凭据库。
3. **验证并启用**：检查驱动、系统权限与隧道；从源 Dot 验证设备与图片，再由本机用户启用桌面任务。

本次 alpha 没有发布者签名或公证。系统提示、官方登录与权限确认仍需用户完成。第三方驱动签名失败会停止安装，不重新签名驱动或关闭系统安全检查。

关闭向导不会停止代理。托盘／菜单栏提供状态、暂停、继续、修复和退出。登录启动运行在当前用户会话中。修复和重启不解除暂停或撤销。

已有 Codex 可安装仓库中的 **DotControl 安装助手**，说“帮我安装并连接 DotControl”。它调用同一安装器，并通过宿主正常浏览器工具协助填写官方页面。详见[安装指南](docs/INSTALL.md)。

## 使用

保持 Dot 连接源电脑，对它说：

> 使用目标电脑的 DotControl 插件，先核对设备名称和本次 nonce。保持官方连接在当前源电脑，不切换目标。观察指定窗口，完成已授权操作，并回传实际结果与原始图片。

每次动作绑定设备、任务、稳定动作编号和新观察。网络重试保持相同编号；unknown 表示效果不确定，必须先核对，不得重新点击或改写历史。结果验证和源 Dot 图片显示分别记录。

有人使用电脑时，只允许经过执行器明确支持的后台动作，并避开正在使用的目标窗口。前台操作默认要求连续空闲 60 秒；人工接管、锁屏或活动检测失效时停止后续输入。后台能力因应用而异。

## 目标端 Codex 委派

计划路线：源 Dot → 目标 DotControl → 目标 Codex → 官方 Computer Use。

当前 Windows 官方 App Server 已实测完成 stdio 连接、模型轮次、官方原生 API 加载和应用发现；真实委派动作、审批、中断和图片回传尚未完成。因此原生委派保持关闭，不能以 Cua 执行替代。Mac 原生委派也未验收。详见[官方入口证据](docs/validation/codex-app-server-2026-10-12.json)。

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
