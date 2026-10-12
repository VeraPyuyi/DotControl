# DotControl v0.2.0-alpha.2

修复升级和驱动重连中的会话冲突，并改进本地安装诊断。安装器保留已有设备、隧道、凭据、暂停状态和动作记录。

- 每个驱动连接使用独立会话名，避免旧连接退出后，新连接继续使用已结束的会话；一个实例退出也不会结束另一个实例的会话。
- 旧版代理不支持自动退出时，明确提示正常退出后重试；不再把持有进程锁一概称为“正在执行任务”。
- 驱动启动检查保留脱敏失败分类，区分应用列表、窗口列表、超时和连接中断，向导显示可读提示。

该会话问题已在固定版本 Cua 0.34.1 的独立 Windows 守护进程上复现，回归测试覆盖多连接和重连。
[复现范围与证据](https://github.com/VeraPyuyi/DotControl/blob/main/docs/validation/driver-session-reconnect-2026-10-12.json)。

Mac 用户已确认 alpha.1 的设备迁移和插件连接成功；普通系统环境的三次检查确认 CuaDriver 稳定运行，新的诊断会话可读取应用。
此前受限任务环境中的“未运行”不能代表系统真实进程状态。
在同一个普通系统环境改用旧代理的 `dotcontrol` 会话后，原始错误确认为 `session has ended; tool call 'list_apps' was rejected`。
**Mac 原代理故障已确认属于已结束会话复用；alpha.2 安装后的 Mac 桌面及新版跨电脑图片闭环仍待真机验证。**
系统权限已授权、驱动正在运行、成功读取窗口和图片实际交付仍分别检查。

Windows 11 x64、Mac Apple Silicon 和 Intel 安装包均自带组件；无需开发工具。
目标端官方 Codex Computer Use 委派仍关闭。发布者仍未签名或公证，上游 Cua 签名检查保持开启。
此版本不自动解除暂停、不重放 unknown 动作、不修改 DNS、代理或系统安全设置。

三平台原生测试、竞态检测、构建和下载后的包内校验已通过。
Windows 实际 EXE 在独立中文及空格目录完成安装、两次修复、三次启动/退出及保留状态卸载。
首次测试曾出现一次退出命令返回 1，未保留原始错误；同一程序的三次单独退出复测和新的完整安装测试通过。
这次偶发退出错误的原因尚未确定，不能标为已修复，也不能据此认定进程挂起。
[构建与哈希](https://github.com/VeraPyuyi/DotControl/blob/main/docs/validation/installer-build-0.2.0-alpha.2.json) ·
[Windows 测试及首次失败记录](https://github.com/VeraPyuyi/DotControl/blob/main/docs/validation/windows-installer-0.2.0-alpha.2.json)。

升级时打开本次下载的安装器并选择保留原设备。已有 alpha.1 的隧道和运行密钥无需重建；
安装后在“检查运行状态”确认桌面驱动已连接。暂停状态仍由本机用户决定是否恢复。

发布后从公开 Release 重新下载的 Windows、Apple Silicon 和 Intel 安装包，均已在对应系统执行包内校验并通过，
哈希与已测试候选包一致：[下载验收](https://github.com/VeraPyuyi/DotControl/actions/runs/38202563161)。
