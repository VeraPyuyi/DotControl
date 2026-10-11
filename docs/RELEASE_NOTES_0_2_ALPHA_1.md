# DotControl v0.2.0-alpha.1

首个自动安装预发布版：将代理、固定版本 Cua Driver、官方 tunnel-client 与必要组件放入安装包，提供本地三阶段向导。

## 下载与使用

- Windows 11 x64：`DotControl-0.2.0-alpha.1-windows-amd64-setup.exe`
- Mac Apple Silicon：`DotControl-0.2.0-alpha.1-macos-arm64.dmg`
- Mac Intel：`DotControl-0.2.0-alpha.1-macos-amd64.dmg`
- Codex 安装助手：`DotControl-onboarding-0.2.0-alpha.1.zip`；也可使用仓库的 Codex marketplace 描述。

安装 → 连接账号 → 验证并启用。每台目标设备使用自己的隧道与私有插件，无需自建云服务器。
用户无需 Go、Python、Homebrew、Xcode 或手工配置。官方登录、密钥本机录入、系统授权和确认仍需用户完成。
密钥仅保存在本机系统凭据库。详见[安装指南](https://github.com/VeraPyuyi/DotControl/blob/main/docs/INSTALL.md)。

## 已实现

共享安装引擎与图形向导、托盘/菜单栏、登录启动、连接退避恢复、修复及卸载留存设备记录。
升级保留设备身份、凭据、暂停/撤销和动作账本，失败尝试恢复旧程序。
连接、权限、暂停、插件调用和图片交付独立显示；历史 unknown 不改写、不自动重发。
新增能力检查及 codex_submit / codex_job / codex_cancel 协议，原生 Codex 执行仍关闭。

## 实际验证与限制

三个目标平台的构建、单元测试、竞态检测与 vet 已通过。三个安装包均从 GitHub Actions 下载后，在目标系统执行包内校验。
Windows 的相同实际 EXE 另外通过中文/空格路径安装、两次修复、驱动连接、暂停/撤销重启保留及卸载留存测试。
Mac 两种架构的上游 Cua 签名原生验证通过，未重签或绕过检查。

**这不是完整真机验收通过的版本。** 干净系统安装、Mac 用户桌面安装/权限、新版双向多应用控制与源 Dot 图片显示仍待验证。
**目标端官方 Codex Computer Use 委派暂不可用**：真实委派动作、审批、中断和图片验收未完成，不会用 Cua 冒充该能力。
安装包未配置发布者签名/公证，系统可能显示来源提示。旧版代理若缺少正常退出接口，需要先退出再迁移。

[支持矩阵与证据](https://github.com/VeraPyuyi/DotControl/blob/main/docs/RELEASE_SUPPORT.md) · [构建测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38170001656) · [安装包构建](https://github.com/VeraPyuyi/DotControl/actions/runs/38170002825)

所有安装包包含第三方许可证。`SHA256SUMS.txt` 和独立 `.sha256` 文件用于核对下载完整性。
程序文件构建自 `339b16031ac7b0b7e4f2d42a94d684ed33c4e547`；后续发布提交只更新工作流、文档和验证证据。
