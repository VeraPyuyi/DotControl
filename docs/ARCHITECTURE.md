# 架构与决策

## 1. 从官方工具调用扩展设备

Dot 负责理解任务、读图、选择下一步。本地代理只实现确定性的设备、窗口、执行权限和动作状态，不持有模型账号，不增加本地模型。MCP 足以表达当前六组同步工具与查询操作。A2A/UFO 仅作为以后多代理调度的研究参考。

每台机器使用单独隧道 ID 和私有插件；同一隧道不放多个不同电脑执行端，否则云端分发不能保证设备选择。独立入口只报告自己。网关入口才汇总显式配对的设备。网关不拥有第二份动作记录，所有操作落到目的设备相同的 Engine。

### 官方执行入口的前提（2026-10-12 更新）

最新真机验证已跑通 **官方 Dot → 已授权 Windows 本地任务 → DotControl Mac 独立插件/隧道 → Mac 代理与计算器 → 官方 Dot 原生截图附件**。Windows 任务核实真实电脑名和 Win32NT，五个后台动作得到 6×8=48，本会话独立回读回执全部 succeeded/background/verified，并实际看到官方 Dot 对话渲染最终截图。父 Dot 结束时再次核实 attached 仍为 Windows，Mac attached=false，没有创建或继续 Mac 本地任务。此证据证明一个跨电脑场景可用，不要求第二台电脑成为官方 attached 电脑；其他应用和设备组合仍须验证。见 [跨电脑验收](CROSS_DEVICE_ACCEPTANCE.md)。

同日反向 **官方 Dot → 已授权 Mac 本地任务 → DotControl Windows 独立插件/隧道 → Windows 计算器 → 官方 Dot 原生截图附件** 也完成了 4×9=36。官方任务报告结束时仍 attached Mac、Windows attached=false；本会话独立回读三条 verified 和两条 unknown，并在官方 Dot 图片查看器看到最终结果 36。两个 unknown 保留，最终计算完成不等于五条动作回执全部验证成功。见 [反向跨电脑记录](validation/mac-to-windows-2026-10-12.json)。双向核心场景成立，仍不代表官方同时连接多台个人电脑或完整产品验收通过。

以下保留此前 Mac 本地入口的诊断过程和适用边界；它不是最新跨电脑验证的替代证据。

用户回传的 Mac 验收中，官方 Dot 确认 apps/observe/execute 都已暴露，但表示该会话只能通过官方本地任务操作用户桌面，因此只调用了 devices。这是 Dot 会话自述的执行方式限制，没有桌面工具的实际错误回执；不能扩大解释为所有产品环境都禁止该 MCP 服务。此前 Windows 官方 Dot 测试窗口结果保留为独立历史证据，不据此推定 Mac 当前会话允许执行。

官方帮助文档支持在目标 Mac 的 ChatGPT 桌面应用中授予电脑访问，再由 Dot 创建本地 Work/Codex 任务。执行路径为：官方 Dot → 已获授权的 Mac 本地任务 → 该任务实际允许的 DotControl 接口 → Mac 代理与执行器。2026-10-12 用户回传的新轮次通过这条路径操作计算器得到 56，并使用 devices/apps/observe/execute/task；本会话独立回读五条记录，均为 succeeded/background/verified。原轮次五条 unknown 的状态与时间戳保持不变。用户报告原图本地展示成功，云端附件仍未交付；此限定操作场景通过，完整验收尚未通过，见 [官方 Mac 计算器新轮次记录](validation/mac-official-calculator-2026-10-12.json)。其他任务仍须检查运行电脑、工具可见性和执行权限，不得假定插件自动继承；纯原生执行不能算作 DotControl 集成通过。

Secure MCP Tunnel 只提供工具传输，不替代官方电脑连接和任务授权。当前这台 Mac 的本地任务证据不能推广为新增隧道即可操作任意另一台电脑；不以调整提示词、改名工具或转移入口绕过当前执行限制。历史入口诊断见 [本地任务诊断](validation/mac-dot-local-task-2026-10-11.json)。

2026-10-11 核对的[官方电脑连接指南](https://learn.chatgpt.com/docs/dots/computers-and-apps)还明确规定，Dot 同一时间只能连接一台个人电脑，云端电脑仍保持连接。通过官方界面将个人电脑连接切换到 Mac，只能验证该 Mac 的受支持本地任务路径，不能算作多台电脑同时接入的目标达成。用户当时截图显示一台个人电脑已连接并被选中、目标 Mac 离线；这一事实与单机限制相关，但不足以确定此前 Mac 离线的具体原因。独立隧道和网关的实现不会自行扩大官方 Dot 的授权范围。

## 2. 单写者与不确定性

进程文件锁避免同一状态目录多开，执行门串行化观察与动作。动作发送前将请求的带密钥摘要和 `dispatching` 状态写入链式 HMAC 日志并 fsync。相同 task_id/action_id、相同完整请求返回已有回执；不同请求返回冲突。进程重启时尚未完成的 dispatching 转为 unknown，不再次调用执行器。

这是“避免重复发出”的机制，不是分布式 exactly-once 保证：原生应用可能处理了输入但无法提供确认；磁盘损坏或用户删除状态目录也不能恢复历史。因此不自动重放 unknown，日志损坏拒绝执行。不要通过新建状态目录绕过锁或重试动作。

动作队列等待也会消耗观察有效期；执行前重新检查 TTL、窗口特征、暂停/取消、人工活动。观察默认 30 秒过期，界面或窗口改变后重新采集。优先使用当前观察的控件 token；视觉坐标携带执行器 capture_id，绑定窗口和截图。

## 3. 人优先

Windows 活动监控在登录会话中使用 Raw Input，不读键值或正文；系统最后输入时间补充未分类输入。Mac helper 使用 HID 活动时间、前台进程和锁屏状态。监控超过一秒没有更新视为不可用。不可用或锁屏时拒绝动作，不假定空闲。

有人操作时，拒绝修改前台应用（按进程保守判断，比仅判断窗口更严格）；其他窗口只能走 background-or-refuse 路径。auto 模式优先后台。前台执行期间监控输入 generation；变化即撤销、停止后续步骤。已交给原生系统的输入不能倒退撤回。未知结果先观察，不直接重放。

适配器不开放原始 key_down/mouse_down 或全屏任意进程 API。当前 Cua 的单次点击和按键调用自行配对释放；显式前台 drag 被拒绝。实际工具 schema 不支持 capture_id 时，坐标 scroll/drag 也拒绝，不删掉截图绑定再发出动作。非控件文本逐字符有取消边界；Mac 控件长文本不得假定 AX 插入一定原子。Mac set_value 仅允许当前观察中已启用的原生文本字段，拒绝网页、Popup、数值与增减回退；审查过的字符串 AXValue 分支才移除该平台不接受的 delivery_mode 参数。系统活动的保守补充可能把模拟输入视为活动，使长前台任务提前停止；优先保证用户接管。

## 4. 信任边界

本地 HTTP 仅绑定字面 loopback 地址，校验 Host/Origin 和独立随机令牌。管理员令牌与 MCP 令牌分离。runtime key、管理员令牌、MCP 令牌、网关私钥存系统凭据库。隧道子进程使用受限环境传递凭据，不使用含密钥的命令行。DotControl 不打印子进程原始日志。

网关仅允许 Tailscale 地址范围或测试 loopback，TLS 1.3 双向认证，双方固定对端证书与设备身份。每次请求重读配对信息，撤销影响已存在的连接。不跟随重定向，不做失败后自动重发。Tailscale 解决网络连通，证书固定解决应用身份；两者分别校验。

操作系统同一用户下的恶意进程、被接管的官方账号、被替换的驱动/代理及用户主动泄露凭据不在此版防护能力内。UI 内容始终是数据，不能成为扩权指令。当前是整台已配对电脑的窗口操作授权，没有实现按应用的细粒度权限策略。

## 5. macOS 身份与分发

Cua 使用上游 CuaDriver.app 的稳定身份，依照系统流程授予辅助功能和屏幕录制。DotControlActivity.app 在固定用户 Applications 路径构建；用户自行授予输入监控。开发版仅 ad-hoc 签名，升级后权限可能需要重新确认；生产发布需要稳定 Developer ID 签名与公证。代理只在已登录用户会话运行，不作为 root 系统服务。

此版没有自动安装后台常驻服务、图形托盘或平台签名发布流水线；CLI 向导提供核心管理能力，安装脚本不改变系统权限。

## 研究依据（2026-10-10 核对）

- [官方 Dot 电脑接入](https://help.openai.com/en/articles/20001530-getting-started-with-your-dot)
- [私有插件与 Tunnel 连接](https://developers.openai.com/plugins/deploy/connect-chatgpt)
- [Secure MCP Tunnel 与权限](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)
- [官方 tunnel-client v0.0.16](https://github.com/openai/tunnel-client/tree/v0.0.16)
- [Cua Driver 平台差异](https://cua.ai/docs/cua-driver/concepts/platform-support)、[MCP 工具](https://cua.ai/docs/cua-driver/reference/mcp-tools)、[权限](https://cua.ai/docs/cua-driver/guides/permissions)
- [Windows Raw Input](https://learn.microsoft.com/en-us/windows/win32/inputdev/about-raw-input)
- [Tailscale 连接类型](https://tailscale.com/docs/reference/connection-types)
- [A2A 与 MCP](https://a2a-protocol.org/latest/topics/a2a-and-mcp/)、[UFO](https://microsoft.github.io/UFO/)

文档支持技术接入路径，不证明当前账号具备权限，也不证明官方 Dot 实际调用成功。后两者单独记录真机证据。
