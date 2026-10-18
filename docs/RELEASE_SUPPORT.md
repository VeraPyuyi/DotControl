# 0.2 支持与验收

[预发布列表](https://github.com/VeraPyuyi/DotControl/releases) · [安装指南](INSTALL.md) · [alpha.7 说明](RELEASE_NOTES_0_2_ALPHA_7.md)

alpha.6 之后的 [0.2 系列复盘与七项源码修复](REVIEW_0_2.md)单独记录安装恢复、结果容量、
退役记录、脱敏错误与会话共享竞争问题。修复源码纳入 alpha.7；以下历史发布证据保持原样。

## alpha.7 发布与验证

`v0.2.0-alpha.7` 已从 `3424e41a05b1858e12dc2137b3e0c3ceece078f0` 构建并发布，
Windows x64 EXE、Mac ARM／Intel DMG、安装助手、许可证及校验和共 11 个文件。全部
公开文件已重新匿名下载，与候选字节一致。后续验收文档提交不改变 tag 或安装包。

三平台完整 Go 测试、竞态检查、vet 和构建通过；原生后台生命周期均实际执行，验证异常
退出恢复、正常退出保持停止及隔离启动项清理。Windows 本机还在嵌套子进程管理条件下重复
通过隔离检查。每个平台的候选包和公开包均完成 8 项隔离安装／回滚／清理／保留配置检查，
包括保留配置卸载后重装和缺失旧程序修复；固定依赖摘要与上游 Mac 应用签名检查通过。

**Mac Intel 公开包首次清理检查曾阻塞**：返回 `startup_references_unavailable`，新版指针
和旧 manifest 尚在，当次删除被保护检查阻止；此前清理阶段可能已删除部分已校验文件，
不能声称旧文件全部保留。仅重试该失败 job 后，同一公开字节通过。首次失败的具体原因尚未确定，
不能据此声称已修复；未放宽启动引用检查。系统服务枚举或读取无法完成时，清理仍可能保守
地停在待完成状态。见[首次失败日志](https://github.com/VeraPyuyi/DotControl/actions/runs/38509150092/job/115673413936)。

本机 Windows 已使用公开 EXE 升级至 alpha.7。升级前旧 alpha.6 代理和隧道已经不在运行，
安装器再次确认退出后激活新版；健康检查成功后旧托管目录清理完成，无待清理项。当前代理、
后台管理进程和隧道各一个。普通交互用户的登录计划任务指向新版，旧 Run 项移除。
真实云端成功轮询和现有插件 devices 调用均通过；未以进程存在或启动项存在代替在线证据。

设备、原隧道、凭据库命名空间、端口、终端权限绑定、暂停／撤销保持不变。白名单仍为
allowlist、修订号 3、2 个会话。新验收任务提交前，共享、连接配置与历史记录原始字节一致，
主配置只更新驱动和隧道程序路径；后续新作业正常追加记录。认证控制台刷新和新标签页正常，
已保存的共享模式未被覆盖。没有重新启用或扩大权限。

现有 Windows 插件通过官方“刷新工具”更新，目录为 13 项，devices 说明已包含先连接检查、
连接请求不建任务和不支持离线唤醒的约束。源 Dot 首先完成仅连接请求，取得 alpha.7 和新
nonce，未提交任务。随后按本次授权创建一个 Windows 官方 Codex 只读任务，收到真实线程
编号和一条 commandExecution：退出码 0，原始输出为 `Microsoft Windows 10.0.26200`、
`X64`。同一作业的独立托管插件查询一致，任务 completed、结果 available；verification
原值仍为 unverified，命令文本没有单独出现在执行事件字段中。

开始及结束均由源 Dot 确认 Mac connected／attached、Windows 未 attached，未切换电脑。
本次通过限定的终端结果回传验收；不证明消息由 Mac 物理设备发送，不包含 SSH、桌面操作、
图片或审批。后台呈现未主动打开任务窗口；没有续聊、重复提交或重放旧任务。

用户 Mac 未修改；Mac 原生 runner 测试不等于用户 Mac 权限、执行端或云端验收。完全停止、
休眠或离线的目标不能仅靠 @ 唤起。原生 Computer Use 仍关闭；发布者签名／公证尚未配置。
未知任务恢复和真实 Windows 自卸载文件占用仍按既有保护处理，不通过扩大权限消除限制。
PR #1 保持草稿未合并，历史发布资产与验收记录未覆盖。

证据：[推送测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38507418962)、
[PR 测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38507422366)、
[原生候选包](https://github.com/VeraPyuyi/DotControl/actions/runs/38507429655)、
[公开包复验](https://github.com/VeraPyuyi/DotControl/actions/runs/38509150092)、
[发布摘要](validation/published-0.2.0-alpha.7.json)、
[后台生命周期](validation/background-0.2.0-alpha.7.json)、
[Windows 升级](validation/windows-upgrade-0.2.0-alpha.7.json)、
[源 Dot 回传](validation/windows-public-dot-0.2.0-alpha.7.json)。

## alpha.6 历史发布验证

`v0.2.0-alpha.6` 已从 `e160c36912763c78cdfd84860a4b99f0ed4c6d6b` 构建并发布，
包含 Windows x64 EXE、Mac ARM／Intel DMG、安装助手、许可证和校验和，共 11 个发布文件。
全部公开文件已重新匿名下载，与候选字节一致。三个原生 runner 的测试、竞态检查、vet、
包内校验及各 6 项隔离安装／重复安装／回滚／清理／配置保留检查通过；公开安装包又在
各自系统完成同样的复验。PR #1 保持草稿，alpha.5 及更早资产未覆盖。

会话共享明确提供“全部会话（默认）”和“仅白名单中的会话”。首次启用默认全部，包含
以后新增会话；已有设备显示实际保存的模式。候选仅在白名单模式展示，两种模式统一
预览确认，空白名单拒绝全部读取。状态刷新和多标签页保留草稿，编辑或修订变化使旧预览
失效。MCP 仍为 13 个工具，历史启用、暂停、撤销和异常配置拒绝读取保持不变。

独立审查无阻断问题，19 项共享交互测试通过；隔离浏览器覆盖 1280/640/360px、键盘、
分页、确认、状态刷新和多标签页。见[共享界面审查](validation/sharing-review-0.2.0-alpha.6.json)。

本机 Windows 使用公开 EXE 升级至 alpha.6。旧 alpha.5 代理与隧道正常退出，新版健康检查
成功后旧托管目录已清理，无待清理项；新代理和隧道各一个，登录启动项指向新版。设备、
原隧道、系统凭据命名空间、端口、暂停／撤销及终端绑定保留。白名单仍为修订号 3、2 个
会话；共享、连接配置和历史任务记录原始字节一致，主配置只更新驱动与隧道的程序路径。

新版已取得真实云端成功轮询。现有 Windows 托管插件的 devices 调用返回原设备、alpha.6、
新 nonce、就绪终端能力及原 allowlist 修订号。认证 HTTP MCP 目录确认为 13 个工具。
实际浏览器打开新版控制台，刷新和新标签页均通过，页面显示原白名单 2 个会话；未点击
启用或保存共享范围。此次只验证升级及只读连接，未新建任务，也未重做 Mac 源端完整闭环。

用户 Mac 未修改；原生 runner 构建与安装检查不代表用户 Mac 真机权限或云端验收。
发布者签名／公证尚未配置，原生 Computer Use 保持关闭。

证据：[推送测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38396876638)、
[PR 测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38396882703)、
[原生候选包](https://github.com/VeraPyuyi/DotControl/actions/runs/38396878955)、
[公开包复验](https://github.com/VeraPyuyi/DotControl/actions/runs/38398142383)、
[发布摘要](validation/published-0.2.0-alpha.6.json)、[Windows 升级](validation/windows-upgrade-0.2.0-alpha.6.json)。

## alpha.5 发布验证

alpha.5 将控制台拆成四个区域，并删除内置 SSH／实验及审批转发。MCP 为 13 个工具；
旧用户文件和历史记录保留，旧审批依赖配置需要本机明确重新配置。原生 Computer Use
保持关闭。以下早期版本的相关能力与证据是历史记录，不代表 alpha.5 仍提供这些入口。

`v0.2.0-alpha.5` 已从 `14586337ce70b092bbe83fadcbee8a86a13afee1` 构建并发布，
包含 Windows x64、Mac ARM／Intel 安装包和安装助手，共 11 个发布文件。公开下载的
逐文件摘要与候选包一致，三个原生 runner 的测试、竞态检查、包内校验及各 6 项隔离
安装／重复安装／失败回滚／清理重试／状态保留检查通过。PR #1 保持草稿，旧发布文件未覆盖。

控制台在 1280、640、360px 宽度完成浏览器检查，无横向溢出；覆盖首次设置、连接正常、
暂停、撤销、网络错误和会话过期。导航、状态刷新、编辑内容、焦点、共享候选及分页分别
验证，旧审批和 SSH 页面脚本不再加载。见[界面验证记录](validation/console-0.2.0-alpha.5.json)。

本机 Windows 已使用公开 EXE 升级。旧 alpha.4 代理和隧道正常退出，旧托管程序目录清理
完成，新代理与隧道各一个，登录启动项指向新版。设备、端口、凭据命名空间、暂停／撤销
及终端绑定保留；共享清单为实际升级时的 allowlist、修订号 3、2 个会话。共享、连接配置
和任务记录原始字节未变，主配置只更新驱动与隧道的程序路径。认证控制台刷新和新标签页
通过；真实云端成功轮询、托管插件 devices 原设备和新 nonce 已确认。

本机 `/mcp`、官方插件目录、源 Dot 本轮实际目录均为 13 个工具，codex_review 已移除。
首次检查因源 Mac 离线而未提交；Mac 恢复连接后，源 Dot 使用同一未提交的请求编号
创建了新的 Windows 官方 Codex 终端任务，并收到真实线程编号及一条 `commandExecution`：
退出码 0，原始输出为 `Microsoft Windows 10.0.26200`、`X64`。对同一作业的独立托管插件
查询与源 Dot 收到的结果一致。开始和结束均由官方 Dot 确认 Mac connected／attached，
Windows 未 attached，未切换电脑。作业为 completed，`verification` 原值仍为 unverified；
命令全文仅在模型文字中，执行事件直接提供退出码和输出。没有重发、续聊或重放旧任务。
这通过本轮限定的只读终端回传验收，不证明消息由 Mac 物理设备发送，也不涉及 SSH、
桌面操作、图片或审批。见[源 Dot 记录](validation/windows-public-dot-0.2.0-alpha.5.json)。

用户 Mac 本轮未修改；上述源端连接验收不代表 Mac 执行端、权限或 Mac 插件云端验收。
原生 Computer Use 继续关闭；旧任务原始记录保留，按原有隐私设计未持久化的运行输出
在重启后仍可能返回 `unavailable_after_restart`，不会自动重放任务。

证据：[推送测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38383741624)、
[PR 测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38383751685)、
[原生候选包](https://github.com/VeraPyuyi/DotControl/actions/runs/38383766777)、
[公开安装包复验](https://github.com/VeraPyuyi/DotControl/actions/runs/38385399772)、
[发布摘要](validation/published-0.2.0-alpha.5.json)、[Windows 升级](validation/windows-upgrade-0.2.0-alpha.5.json)。

## alpha.4 发布与升级实测（2026-10-13）

`v0.2.0-alpha.4` 已发布，增加默认自动代理、本机网络设置和基于当前进程成功轮询的云端状态。Windows x64、Mac Apple Silicon、Mac Intel 与安装助手均从源提交 `728d152430fb6a800b570e78c1f74e6d3700aee4` 构建；后续验收文档提交不改变安装包字节。PR #1 仍为草稿，未合并；用户 Mac 未修改，旧版本资产与记录保留。

| alpha.4 检查 | Windows x64 | Mac Apple Silicon | Mac Intel |
|---|---|---|---|
| 原生测试、竞态检测、vet、构建 | 通过 | 通过 | 通过 |
| 实际安装包、包内文件和隔离安装检查 | 44 个文件、6 项检查通过 | 47 个文件、6 项检查通过 | 47 个文件、6 项检查通过 |
| 固定依赖哈希 / Cua 上游应用签名 | 哈希通过 / 不适用 | 哈希及上游签名通过 | 哈希及上游签名通过 |
| 公开 Release 下载字节与原生包内验证 | 通过 | 通过 | 通过 |
| 当前进程真实成功轮询及托管插件设备查询 | 公开安装包升级及正常重启后通过 | 用户真机未验收 | 用户真机未验收 |
| 原生 Computer Use、远程审批、源 Dot 图片显示 | 未验收，能力继续受限 | 未验收，能力继续受限 | 未验收，能力继续受限 |

Windows 已使用重新下载的公开安装包升级，原设备、隧道、端口、凭据引用、终端权限、暂停／撤销及共享范围保留。仅更新 `driver_path`、`tunnel_path`；共享清单和任务记录的原始字节未变。旧代理及隧道正常退出，旧 alpha.3 托管目录和两份已核对归属的候选程序清理完毕，登录启动项指向新版。正常重启后只有一个代理和一个隧道；系统代理来源为 `system`，成功轮询时间为 `2026-10-13T08:24:31.4923977Z`，真实托管插件返回原设备及新 nonce。仍为 14 个工具，allowlist 修订号 2、1 个会话。向导刷新及多标签页检查通过。

候选阶段官方 Dot 在开始和结束均保持 Mac connected/attached、Windows 未 attached，通过 Windows 插件创建只读系统信息任务；实际命令退出码 0，输出 Windows 10.0.26200 / X64。源消息由现有浏览器发送，不将消息发送物理电脑声称为 Mac。升级重启后 Dot 再次核实设备和原线程仍在，但命令输出按原有隐私设计未持久化，返回 `unavailable_after_restart`，不算重新取得输出。 随后在正式安装版上单独新建只读验收任务，源 Dot 实际收到新的官方线程编号及 `commandExecution`：退出码 0，输出同为 Windows 10.0.26200 / X64，开始结束仍保持 Mac attached；没有重放旧任务。见[正式安装版源 Dot 结果](validation/windows-public-dot-0.2.0-alpha.4.json)。

证据：[三平台测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38366303456)、[候选包](https://github.com/VeraPyuyi/DotControl/actions/runs/38366305748)、[公开安装包原生复验](https://github.com/VeraPyuyi/DotControl/actions/runs/38368202657)、[发布字节记录](validation/published-0.2.0-alpha.4.json)、[本机升级记录](validation/windows-upgrade-0.2.0-alpha.4.json)、[候选云端及源 Dot 记录](validation/windows-tunnel-0.2.0-alpha.4.json)。以下 alpha.3 及更早证据仅属于相应旧版本。

本地就绪、云端轮询与源 Dot 本次调用分别判断。云端在线要求当前进程本地就绪并有近期成功轮询；`/readyz` 或 `official_tunnel_doctor=configuration_validated` 均不能替代。`doctor --online` 的 `tunnel_cloud_connection` 单独返回运行中 supervisor 状态。固定客户端 0.0.16 无法保证代理热切换时请求无损结束，因此保存网络方式或检测到代理变化后只标记待应用，不结束运行中的客户端；最新设置在自然退出后的重启或下一次明确的本机重启时应用。PAC、仅 SOCKS 和交互认证限制见[网络说明](NETWORK.md)。

## alpha.3 发布与升级实测（2026-10-13）

发布源提交为 `9faaf28322bf123d4b97769faa537206e3b2f564`，后续验收文档提交不改变安装包字节。
PR #1 仍为草稿；alpha.2 文件未覆盖。

| 检查 | Windows x64 | Mac Apple Silicon | Mac Intel |
|---|---|---|---|
| 原生测试、竞态检测、vet、构建 | 通过 | 通过 | 通过 |
| 下载候选包后验证实际包内文件 | 通过，44 个文件 | 通过，47 个文件 | 通过，47 个文件 |
| 实际安装包的隔离安装、重复安装、健康失败回滚、清理重试及状态保留 | 6 项通过 | 6 项通过 | 6 项通过 |
| 固定依赖哈希 / Cua 上游应用签名 | 哈希通过 | 两项通过 | 两项通过 |
| 公开 Release 重新下载及原生包内验证 | 通过 | 通过 | 通过 |
| 用户正在使用的旧代理迁移及健康检查 | 通过 | 本轮不修改用户 Mac | 本轮不修改用户 Mac |
| 用户桌面权限、原生 Computer Use、源 Dot 结果显示 | 本轮不验收 | 本轮不验收 | 本轮不验收 |

隔离安装测试使用包内程序和合成旧版本，未启动真实驱动、隧道或用户会话；不能替代真机桌面验收。
最终构建首次遇到 Mac 挂载资源忙和 Windows 测试子进程启动超时，未改源码重跑失败作业后通过。
此前发现的下载目录层级导致验证循环跳过的问题已修复：现在缺少安装包或找到多个安装包都会导致检查失败，
仅最终提交生成的实际 `candidate-payload.json` 计入上述结果。

证据：[原生测试](https://github.com/VeraPyuyi/DotControl/actions/runs/38353479661)、
[构建及候选包验证](https://github.com/VeraPyuyi/DotControl/actions/runs/38353480505)、
[公开下载原生验证](https://github.com/VeraPyuyi/DotControl/actions/runs/38354688776)、
[逐文件摘要与验收范围](validation/published-0.2.0-alpha.3.json)。

本机 Windows 已使用公开下载的 EXE 完成非托管旧程序迁移。旧代理及其隧道子进程正常退出，
新版代理和隧道各一个实例，登录启动项指向新版，原状态目录和端口保留。
设备身份、系统凭据引用、暂停／撤销、终端权限配置均保留；配置只变更驱动和隧道的程序路径。
共享清单仍为 allowlist、修订号 2、1 个会话，清单和动作记录的原始字节未变。
健康检查后按显式迁移清单删除 9 个已核对摘要且无进程引用的旧程序文件，10 个未列入清单的文件保持原样。
本机认证 MCP 实测仍为 14 个工具。见[脱敏迁移记录](validation/windows-upgrade-0.2.0-alpha.3.json)。

向导已通过正常本机启动入口重新打开；运行中服务的首次认证、刷新、第二标签页及返回原标签页协议检查通过。
当前侧栏打开请求被宿主排队，不能据此声称已完成新页面的浏览器渲染验收。

**云端连接未通过。** 升级前后真实 Windows 插件均返回 HTTP 404 / `tunnel_client_not_seen`。
官方隧道健康详情显示控制服务 `network_error`、尚无成功轮询；本地 `/readyz` 为 200 不能证明云端在线。
本轮直连控制服务超时，现有系统代理路径可收到 HTTP 响应；未修改 DNS、TLS、代理或凭据配置。
因此安装升级完成与云端调用恢复分开记录，不能宣称升级后跨电脑调用已通过。

## alpha.3 委派与共享能力

[PR #1](https://github.com/VeraPyuyi/DotControl/pull/1) 增加官方 App Server 任务、
本机公钥授权及人工审批页面、可取消的空闲等待、确认停止后的任务交接，以及固定 CPU
实验助手。实验编号绑定服务器配置，响应不确定时只能查询，不能重复启动。
Windows 图形程序经 PowerShell 调用的输出与退出码已覆盖真实子进程测试，包括中文和特殊引号路径。
本机审批页会自动显示到期状态、禁用失效表单，并在按钮旁保留提交回执；网络响应不确定时不重发回答。
终端任务收到官方停止回执且连接清理完成后，可用新的追加请求继续原会话；任务和实验编号不变，旧审批不复用。

alpha.3 从 PR 分支独立发布，保留 alpha.2 文件。Windows 终端委派须本机启用；历史读取另行启用。未验收的平台和官方桌面委派继续关闭。
官方桌面动作、物理接管、Mac 源端审批和图片显示仍需真实闭环验证。
Windows 当前保守的活动检测会将注入输入计入活动，可能中断原生委派自身；保持保护机制，
不通过屏蔽人工输入检测强行启用。详细状态见[最新验收记录](validation/native-ssh-progress-2026-10-12.json)。

## alpha.2 修复状态

alpha.2 修复了驱动连接复用已结束会话、旧代理退出错误提示和启动诊断丢失分类的问题。
[说明与边界](RELEASE_NOTES_0_2_ALPHA_2.md) · [会话故障复现](validation/driver-session-reconnect-2026-10-12.json)。
Mac alpha.1 已完成旧设备迁移并通过插件返回新版设备信息；用户随后报告权限与活动检测通过。
普通系统环境确认驱动稳定运行、新诊断会话读取成功；受限任务环境的“未运行”不能用于判断真实进程状态。
同一普通环境使用原代理的固定会话时返回 `session has ended`，已确认会话复用根因；alpha.2 安装后仍需真机复验。
alpha.2 构建和发布文件检查单独记录，以下 alpha.1 的证据不自动计为 alpha.2 验收。

| alpha.2 检查 | Windows x64 | Mac Apple Silicon | Mac Intel |
|---|---|---|---|
| 原生测试、竞态检测、vet、构建 | 通过 | 通过 | 通过 |
| 下载 CI 安装包并在目标系统验证包内文件 | 通过，43 个文件 | 通过，46 个文件 | 通过，46 个文件 |
| 下载公开 Release 并在目标系统验证包内文件 | 通过 | 通过 | 通过 |
| 固定上游归档哈希 / Cua 应用签名 | 哈希通过 | 两项通过 | 两项通过 |
| 实际安装、两次修复、暂停/撤销保留、卸载保留数据 | 隔离目录通过 | 未验证 | 未验证 |
| 用户桌面新版跨电脑动作和图片闭环 | 未验证 | 未验证 | 未验证 |

安装包源提交：`ec65d7275a816398a91397d53c89b6a5edf38336`。
见[构建及下载校验](validation/installer-build-0.2.0-alpha.2.json)、[Windows 安装测试](validation/windows-installer-0.2.0-alpha.2.json)。
Windows 首次测试在已连接驱动后出现一次退出命令失败（退出码 1），未捕获底层错误。
随后同一程序三次退出复测和重新执行的完整安装测试均通过，未改变安装包字节；偶发错误原因仍未确定。
首次清理脚本没有独立等待代理退出，不能把它记录的清理标签解释为已证明退出超时。

发布后的[三平台原生下载校验](https://github.com/VeraPyuyi/DotControl/actions/runs/38202563161)全部通过，
实际下载文件的哈希与已测试候选包一致。见[公开发布文件记录](validation/published-0.2.0-alpha.2.json)。

## alpha.1 已验证范围

本版交付 Windows EXE、Mac 两种架构的 DMG 和 Codex 安装助手。
目标端原生 Codex 委派仍关闭。以下“通过”仅覆盖所列检查，不代表完整计划验收。

| 检查 | Windows x64 | Mac Apple Silicon | Mac Intel |
|---|---|---|---|
| 原生构建、单元测试、竞态检测、vet | 通过 | macOS 14 runner 通过 | macOS 15 runner 通过 |
| 自包含安装包 | EXE，43 个清单文件 | DMG，46 个清单文件 | DMG，46 个清单文件 |
| Cua 上游签名、固定归档哈希 | 归档哈希通过 | 原生签名与哈希通过 | 原生签名与哈希通过 |
| 从 GitHub Actions 下载后执行包内校验 | 通过 | 原生挂载 DMG 后通过 | 原生挂载 DMG 后通过 |
| 从公开 Release 下载后执行包内校验 | 通过 | 通过 | 通过 |
| 实际包安装、两次修复、卸载保留状态 | Windows 11 隔离目录通过 | 未验证 | 未验证 |
| 暂停/撤销重启保留、驱动与活动检测 | Windows 11 通过 | 用户桌面未验证 | 用户桌面未验证 |
| 干净系统、安装图形流程、登录重启 | 未验证 | 未验证 | 未验证 |
| 新版双向操作与源 Dot 图片交付 | 未验证 | 未验证 | 未验证 |
| 官方 Codex 原生应用发现 | 当前官方客户端已验证 | 未验证 | 未验证 |
| Codex 委派动作、审批、中断、图片 | 未验证，关闭 | 未验证，关闭 | 未验证，关闭 |

安装包构建源提交：`339b16031ac7b0b7e4f2d42a94d684ed33c4e547`。
公开证据：[三平台构建与下载校验](validation/installer-build-0.2.0-alpha.1.json)、
[Windows 实际安装包测试](validation/windows-installer-0.2.0-alpha.1.json)。
公开下载的三个安装包及安装助手与已测试候选文件逐字节一致，见[发布下载记录](validation/published-0.2.0-alpha.1.json)。
源码与安装包保留第三方许可证；未发布本地凭据、设备标识或私人截图。

Windows 在公开 Release 发布后重新下载并安装实际 EXE，在中文及空格路径中安装并验证全部组件哈希；
三次启动/正常退出、暂停和撤销保留均通过。卸载保留配置、账本与撤销标记原始字节。
测试时全局登录启动项原本不存在，确认它未被更改；不能据此声称实测了另一台已登记设备的启动项。
本轮没有发送桌面输入、截图或使用实际隧道凭据。

Mac 原生构建环境重新验证了 0.34.1 上游应用签名，未重签 CuaDriver。
这不能倒推之前用户 Mac 上的签名错误原因，也不能替代用户 macOS 26 环境的实际安装与权限测试。
初始 Mac CI 发现的系统临时目录别名、codesign 内联参数与竞态测试退出时限问题均已修复并复验。

## 已知限制与升级

- 登录、运行密钥本机录入、系统权限及官方确认仍需用户完成；无需开发工具或手工编辑配置。
- 发布者未签名或公证。系统来源提示可能出现；不删除隔离属性、不关闭系统安全检查。
- Mac 必须通过 Cua 签名检查。未知权限不显示为已授权；权限授权不等于截图已经成功。
- 0.1 的成功跨电脑记录只适用于旧实例。中文文本、浏览器、多屏、物理接管、长时间断网恢复仍待新版真机验收。
- 关闭向导不停止代理。自动修复不恢复暂停、撤销，不重放不确定动作。升级等待执行结束再替换程序。
- 老代理若没有正常关闭接口，需要用户先退出；安装器不会强杀可能正在产生副作用的桌面任务。旧网关配置迁移目前拒绝执行。
- 旧版自定义状态目录不会通过扫描全盘发现。自动发现覆盖默认目录与安装器当前工程 `.state` 下的设备子目录；其他旧目录须由安装助手明确选择，不能假定“新设备”就是迁移成功。
- 本机查看、MCP 图片返回、云端附件与源 Dot 实际显示分别记录。未捆绑旧 Python 上传脚本。
- 官方 Computer Use 委派仍关闭；Windows 终端委派可在本机明确启用。构建、终端任务、桌面操作和源端结果显示分别验收。
- 首发不包含网关产品化、公共插件市场上架或强制更新。更新只提示，由用户选择安装。

发布后已使用 [verify-published-installers](https://github.com/VeraPyuyi/DotControl/actions/runs/38170631830) 再次下载公开 Release 资产，三个目标系统的包内校验均通过；该检查同样不等于桌面验收。
