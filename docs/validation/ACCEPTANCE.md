# 验收记录

状态词：**通过**有实际证据；**未验证**没有运行证据；**阻塞**已尝试但缺少外部条件。SDK、本地 API 和普通 ChatGPT 调用不能替代官方 Dot 验收。

| 验收项 | 当前证据 |
| --- | --- |
| 官方 Dot 保持连接 Mac，经插件操作 Windows | 2026-10-12 核心计算与可见截图交付通过：官方 Mac 本地任务经 Windows 插件完成 4×9=36，父 Dot 报告结束时仍 attached Mac、Windows attached=false。本会话独立回读三条 succeeded/background/verified 和两条 unknown/background/execution_uncertain，并打开官方 Dot 最终原图，目视确认 4 × 9 =、36；原图字节与哈希未另行校验。清除未重发且历史时间戳未变；乘号 unknown 后通过新表达式观察确认现场才继续，没有重发。五步回执全部 verified 的检查项未通过。见 [反向跨电脑记录](mac-to-windows-2026-10-12.json)；此前 [取消处理修复](mac-to-windows-driver-cancellation-2026-10-12.json)、[清除不确定记录](mac-to-windows-clear-uncertain-2026-10-12.json)、[驱动恢复记录](mac-to-windows-driver-recovery-2026-10-12.json) 和 [隧道恢复记录](mac-to-windows-tunnel-recovery-2026-10-12.json) 保留为历史 |
| 官方 Dot 保持连接 Windows，经插件操作 Mac 并返回结果 | 2026-10-12 通过限定场景：官方 Windows 本地任务核实真实 Windows 身份，通过 DotControl Mac 后台完成 6×8=48；五条回执均 succeeded/background/verified，本会话独立回读一致。最终原图经原生 Library 接口返回并在官方 Dot 主对话实际渲染，本会话已目视确认并核对本地原图大小、SHA-256。父 Dot 结束时核实 Windows 仍 attached/authorized，Mac attached=false，未创建或继续 Mac 本地任务。见 [跨电脑记录](cross-device-2026-10-12.json) |
| Windows 原生截图及 UIA 控件发现 | 通过：独立 WinForms 测试窗口 |
| 后台中文输入与保存 | 通过：应用实际输出的 UTF-8 文件逐字相等 |
| 相同动作编号重试 | 通过：界面计数只增加一次，返回原动作回执 |
| 操作前后前台窗口 | 通过：前台 HWND 保持一致；这不代表已穷尽所有应用 |
| 人工接管、重启、记录损坏、并发、取消 | 自动化故障测试；物理接管真机测试仍未验证 |
| 官方账号插件创建与发现 | 通过：用户确认后创建并连接私有插件，个人列表和详情页均显示该插件；官方工具详情列出 Read 4（devices、apps、observe、task）、Write 2（execute、cancel） |
| 官方 Platform Tunnels 页面 | 通过：创建 Windows 独立验证隧道，仅关联当前组织与页面列出的工作区。运行凭据仅授予 Tunnels Read + Use、有效期 7 天，已写入 Windows 凭据库；编号和账号标识未提交仓库 |
| 官方 tunnel-client 运行 | 通过：0.0.16 的 doctor 在线检查通过，agent 报告 running=true、ready=true，客户端 /readyz 返回 HTTP 200。修复了 warn 日志级别缺少显式日志格式导致启动退出的问题；本轮连接使用已有本机代理 |
| 官方 Dot 的 devices / nonce | 通过：在官方 /dots/home 对话发送只读验收指令；Dot 返回与本机一致的设备 ID、Windows、paused=true 及新的完整随机 nonce。未把本地调试接口响应转贴为 Dot 结果 |
| 官方 Dot 的图片和 GUI 动作 | 通过限定场景：Dot 报告 observe 收到测试窗口截图，后台点击及同一完整请求重试均返回同一 succeeded/verified 回执；本机动作记录与独立 UI 树回读核对 Count: 1。聊天内图片展示工具返回 widget unavailable，未验证用户可在聊天里看到图片。此场景不等于计算器、文本编辑器和浏览器全套验收 |
| Mac 云端设备发现 | 用户返回官方 Dot 的实际 devices 结果：名称 DotControl Mac、darwin、paused=true，设备编号与先前 Mac 终端诊断一致。随后本会话通过已连接的 Mac 插件独立调用 devices 成功，得到同一设备及不同的随机 nonce。官方 Dot 结果是用户返回的证据；本会话 MCP 调用另行记录，不替代官方 Dot 验收 |
| Mac 计算器截图与后台操作 | 通过限定场景：本会话通过 Mac MCP 插件取得 460×816 窗口截图和 AX 控件，完成 6×7=42 及后续恢复测试 8×9=72；使用包含 U+200E 的精确 expect 时，两轮各五步均返回 succeeded/background/verified，task 与新截图核对一致。此前 plain expect="42" 的 unknown 原回执保留。本会话结果与官方 Dot 结果分别记录 |
| 官方 Dot 的 Mac 桌面操作 | 通过限定场景：2026-10-12 官方本地任务通过 DotControl 完成 7×8=56，五步明确传入含 U+200E 的精确 expect。本会话独立 task 回读确认五条均为 succeeded/background/verified；旧轮次五条仍为 unknown/background/execution_uncertain，时间戳未变。实际截图及 AX 一致、每步一次、新观察和控件均由用户报告；本会话未追加桌面动作 |
| 官方 Dot 的 Mac 截图交付 | 本地展示：用户报告三张原始 PNG 已通过 view_image 在原任务展示，新最终图 72,374 字节，哈希另记；本会话未读取原图。云端附件：未交付、无 library_file_id。原上传通道完整目录缺少所需 Library 接口；Python 和联网已通过，无需重复旧上传脚本。完整验收保持未通过 |
| Mac 官方客户端网络 | 历史用户诊断记录 ENOTFOUND、self signed certificate、rendezvous unavailable，以及 10:25 的部分重连；当时刷新仍离线。随后用户明确确认 Dot 电脑列表已显示“已连接”。本会话未读取 Mac 原始日志，未独立观察该官方列表；恢复原因和持续稳定性未验证。新回报与历史故障分别保留 |
| Mac 动作重试与焦点 | 通过限定场景：两轮各将“等于”的同一完整请求重试一次，返回原回执及时间戳，显示仍是 42；前后采样的前台进程均为 Terminal。没有持续监测瞬时焦点，也没有测试真人接管 |
| Mac SSH 连接 | 历史 Windows → Mac SSH 连接或握手超时；用户后来改用 Mac 本机安装。SSH 恢复未验证，不影响已成功的云端 devices 调用 |
| Mac 二进制构建 | Go darwin/arm64、darwin/amd64 构建通过；交叉编译不代表系统权限与桌面输入通过 |
| 真实 Tailscale 双机网关 | 未验证；自动化 mTLS 网关测试单独记录 |
| Windows 安装脚本 | 通过：隔离目录、已下载官方归档 SHA 核验、构建、创建身份、凭据库写入及 doctor |
| macOS 安装脚本和原生 helper | 初次诊断显示代理和活动监控运行，但驱动未连接、TCC unknown、隧道未配置；已修正 Cua 应用安装路径。用户随后报告代理和云端连接成功，并本机 resume。最新 devices 回读为 activity.available=true、locked=false、paused=false；计算器截图和 AX 后台点击已成功。TCC 状态命令未复查，不能据此宣称所有权限、系统版本和架构组合已验证 |

本轮 Windows 详细结果见 [windows-2026-10-10.json](windows-2026-10-10.json)。不要将待验收项改成通过，除非填写实际调用、设备、版本、输入和结果的对应证据。

2026-10-10 的自动化记录见 [automated-2026-10-10.json](automated-2026-10-10.json)：75 个顶层测试通过，连同子项共 177 项通过；两个显式跳过的是独立运行的进程崩溃辅助入口和需要 opt-in 的现场观察测试。竞态检查、静态检查通过，Windows 原生演示使用该记录对应的发布二进制运行通过。后续隧道修复及实测单独记录，旧记录中的哈希仅对应当时产物。CI 配置已提供，但未声称 GitHub 托管执行器已实际运行。

2026-10-11 的官方隧道、插件及 Dot 验证见 [tunnel-2026-10-11.json](tunnel-2026-10-11.json)。设备/账号标识保留在本机忽略目录，公开记录只保留核验结论和测试元数据。

同日用户提供的 Mac 本地诊断与安装路径调查见 [mac-startup-2026-10-11.json](mac-startup-2026-10-11.json)。这是用户返回的运行证据，不是本机远程控制 Mac 的结果。

后续 Mac 云端设备发现结果见 [mac-connection-2026-10-11.json](mac-connection-2026-10-11.json)：分别保存用户返回的官方 Dot 结果与本会话的独立 MCP 回读结论。先前启动诊断保留为历史记录。

Mac 计算器真机结果见 [mac-calculator-2026-10-11.json](mac-calculator-2026-10-11.json)：包含首次不确定回执、逐步现场核对、精确后置条件通过和相同请求重试。本次没有修改或部署代理程序，没有保存截图或完整 AX 树；测试结束时代理仍为恢复状态。这些新记录不在此前已打包的 Mac ZIP 内。

随后用户回传的官方 Dot Mac 验收阻塞见 [mac-dot-blocker-2026-10-11.json](mac-dot-blocker-2026-10-11.json)。隧道接入成功不等于产品允许所有桌面工具调用；诊断应确认实际可用工具、可见审批请求及受支持的执行入口，不通过改名工具或放宽权限掩盖限制。

进一步诊断见 [mac-dot-local-task-2026-10-11.json](mac-dot-local-task-2026-10-11.json)：工具可见，Dot 自述当前会话需要官方本地任务。待验证目标 Mac 的官方电脑授权与任务通道，并检查该任务是否实际使用 DotControl；本次没有创建本地任务，也没有发出桌面动作。

用户后续照片显示目标 Mac 为“离线”，另一台个人电脑为“已连接”且已选中；提供的桌面版本为 26.1007.30912（2026-10-10 发布）。官方本地接入指南要求 26.929 或更高，因此没有版本低于该门槛的证据；未验证该 Mac 当前可安装的最新版本。官方电脑连接指南明确只允许同时连接一台个人电脑，这限制了原始多设备目标，但不能单凭该限制确定照片中 Mac 离线的具体原因。未切换或撤销现有电脑连接。

Mac 本机只读网络诊断摘要见 [mac-official-network-2026-10-11.json](mac-official-network-2026-10-11.json)。后续证据优先指向历史 DNS/TLS 故障，且已出现部分恢复；单机连接上限、会话访问拒绝与网络故障分别记录，不互相推定原因。未关闭证书校验，未修改 DNS、代理、凭据或权限。

后续连接恢复见 [mac-official-recovery-2026-10-11.json](mac-official-recovery-2026-10-11.json)：用户明确确认官方 Dot 电脑列表已显示目标 Mac“已连接”，本会话独立调用 DotControl devices 成功，paused=false、locked=false、活动监控可用。本轮只检查连接，没有创建官方本地任务或执行桌面动作；官方 Dot Mac GUI 验收仍未通过。

恢复后的官方任务尝试见 [mac-official-driver-blocker-2026-10-11.json](mac-official-driver-blocker-2026-10-11.json)：用户回传已在正确 Mac 的本地任务中调用 DotControl，阻塞于 apps 的 driver_unavailable。本会话复现同一错误，尚无底层驱动原因的证据；设备发现成功不代表桌面驱动健康。

随后用户报告 CuaDriver arm64 原生代码签名验证失败，见 [mac-signature-2026-10-11.json](mac-signature-2026-10-11.json)。本机已核对交付包内归档与上游校验和一致，并补充安装/启动签名检查；三个模拟控制流测试及 shell 语法检查通过。尚待 Mac 上已安装副本与新解压原件的原生验证比较；新检查尚未部署，签名故障、apps 和 GUI 验收均未恢复。

进一步对照见 [mac-signature-comparison-2026-10-11.json](mac-signature-comparison-2026-10-11.json)：用户确认已安装与新解压副本都在 arm64 原生校验中失败，两个主程序哈希一致。Windows 侧局部静态检查未发现原归档代码页哈希或 CMS 数学签名不匹配；这不代替 macOS 原生校验，也不确立 -10827 的根因。整个安装副本的 bundle 比较、Mac 系统/架构及原生评估细分结果仍待返回；没有启动、覆盖或重新签名应用。

后续原生细分检查见 [mac-signature-context-2026-10-11.json](mac-signature-context-2026-10-11.json)：用户返回 macOS 26.6.2（25G83）、arm64，不加 deep/strict 仍验证失败，指定 x86_64 的严格检查也失败；Gatekeeper 报签名子系统内部错误。整包比较在路径、文件类型、内容哈希、链接目标和可执行位范围内无差异，扩展属性未比较。已请求普通终端与本地任务的同机对照，并以 Apple TextEdit 为参照；任务环境因素仍是假设。没有修改安全设置，官方 Dot Mac GUI 验收仍阻塞。

用户报告可能恢复后，本会话于 2026-10-11 20:05（北京时间）通过 Mac 插件重新验证，见 [mac-driver-recheck-2026-10-11.json](mac-driver-recheck-2026-10-11.json)。devices 与 apps 成功，底层 list_apps/list_windows 返回 complete，未再复现 driver_unavailable；但代理 paused=true、activity.available=false，计算器未运行，因此没有调用 observe/execute 或发送桌面输入。本次不等于签名检查恢复，也不计为官方 Dot Mac GUI 验收。

用户恢复代理后，本会话于 2026-10-11 22:06–22:07（北京时间）完成 Mac 计算器恢复验证，见 [mac-calculator-recovery-2026-10-11.json](mac-calculator-recovery-2026-10-11.json)。通过 DotControl 的 devices、apps、observe、execute、task，后台依次完成全部清除、8、乘、9、等于；五条持久回执均为 succeeded/background/verified，逐步新截图与 AX 文本确认最终结果 72。前后采样的前台 PID 相同且不是计算器；未持续监测焦点或测试物理接管。没有部署新程序或重新校验签名，也不将本会话成功计为官方 Dot 入口复测或项目全面验收通过。

最新官方 Dot 尝试见 [mac-official-calculator-2026-10-11.json](mac-official-calculator-2026-10-11.json)：2026-10-11 22:17–22:19（北京时间）官方 Mac 本地任务报告计算出 56；本会话只读回验五条回执，均为 unknown/background/execution_uncertain。实际执行与后续观察有用户报告，完整截图交付失败，未知动作没有重发。这次进展不能仍记为 apps 阻塞，也不能判定完整验收通过。下一步只读核对原 execute 的 expect 参数及附件失败 Traceback/解释器；本会话没有追加桌面动作或修改旧回执。

原任务只读回查进一步确认五次 execute 均未传 expect，排除了“已填写 expect 但方向标记不匹配”的解释；上传脚本在导入 `str | None` 类型注解时失败，历史命令使用 `/usr/bin/python3`，当前核验版本为 3.9.6。见 [诊断记录](mac-official-calculator-diagnosis-2026-10-11.json) 和 [具体修复与复测指令](../MAC_OFFICIAL_RETEST.md)。仓库补强工具说明、五种后置条件回归及 Unicode MCP 往返验证，`go test ./...` 与 `go vet ./...` 通过；未改变状态语义、旧回执或 Mac 环境。原截图交付和新的官方验收仍待完成。

上传重试后续见 [mac-official-upload-dns-2026-10-11.json](mac-official-upload-dns-2026-10-11.json)：用户报告 Python 3.12.14 导入通过，但附件上传的 hosted apps 工具发现报 DNS 错误，尚无附件 ID。任务按要求停止，未调用 DotControl，显式 expect 的新轮次未开始，旧 unknown 回执保持不变。此错误不确立 DotControl 隧道或 Mac 全局 DNS 故障；正在请求上传目标和底层解析证据。

上传网络只读诊断见 [mac-official-upload-network-policy-2026-10-11.json](mac-official-upload-network-policy-2026-10-11.json)：chatgpt.com:443 在原任务中解析失败，代理未启用，任务联网明确禁用。dscacheutil 的退出码 0 伴随空输出，不是解析成功证据。已给出原任务范围的正常网络权限申请步骤；本会话未修改任何任务/系统权限、未上传或追加桌面动作，完整验收仍待完成。

后续工具发现记录见 [mac-official-upload-tool-discovery-2026-10-11.json](mac-official-upload-tool-discovery-2026-10-11.json)：用户报告正常联网许可经系统自动审核通过，tools/list 返回 306 个工具，不存在可展示的人工审批卡或公开审批 ID。上传在图片传输前报 Library prepare_uploads is not available，原图未变且未获 library_file_id。当前核对原 Mac 目录的分页完整性、相关工具 schema 和脚本匹配逻辑；本 Windows 会话的工具目录不能替代该证据。旧 unknown 回执未修改，新计算器轮次仍未开始。

完整接口对照见 [mac-official-upload-interface-2026-10-11.json](mac-official-upload-interface-2026-10-11.json)：用户核实目录完整、Library 连接器及准备接口零匹配，脚本已有分页处理并按完整工具名称调用。此路径缺少所需能力；其他上传工具目的地和参数不同，不构成同契约替代。原始服务接口的文件对象和宿主工具的本地路径参数分别记录，不编造文件引用或推定 Mac 宿主已经支持上传。停止此路径的重复诊断，将原任务图片预览与明确 expect 的动作复测分开推进；未修改原图、脚本、权限或历史回执，完整验收仍未通过。

2026-10-12（北京时间）新轮次结果见 [mac-official-calculator-2026-10-12.json](mac-official-calculator-2026-10-12.json)：官方任务在 00:05–00:09 完成五步后台操作，精确 expect 回执全部通过。本会话只读核对新旧两个 task：新五条 succeeded/background/verified；旧五条 unknown 的状态及时间戳与历史记录逐项一致。用户报告输入 7 后有一次 observe 超时（-32603），重做只读观察确认 7 后继续，没有重发点击；这是观察恢复证据，不是动作响应丢失或全面故障注入测试。原任务已本地展示三张图，新的原始 PNG 大小及 SHA-256 由用户报告，本会话未独立验证文件字节。官方计算器操作场景通过，云端图片交付和整体项目验收仍未完成。

## 官方 Dot 的验收步骤

1. 在每台设备创建独立 Secure MCP Tunnel，并取得仅具 Read + Use 的运行凭据。
2. `dotcontrol credential` 在本机隐藏输入，写入系统凭据库。配置文件仅填写 tunnel_id 与程序路径。
3. `dotcontrol serve`；另一终端运行 `dotcontrol doctor --online` 与 `dotcontrol status`。本地 running 和隧道 ready 分别核对。
4. ChatGPT → Plugins → Add custom MCP server → Tunnel，选择该设备的隧道；以设备名创建私有插件。
5. **在官方 Dot 中**要求：调用该插件的 devices，返回设备名、device_id 和随机 nonce。保留调用时间、工具调用及结果；不以另一个 MCP 客户端替代。
6. 启动专用测试窗口，要求 Dot 通过 apps 找到它，observe 并显示截图；点击一次计数按钮。比对真实窗口结果。
7. 重发同一 action_id（相同完整请求），验证计数没有增加。让 Dot 在目标窗口外有人使用时后台写中文并保存，检查焦点与文件。
8. 分别对计算器、文本编辑器、浏览器测试页执行任务；再测锁屏、缺少权限、多屏缩放、移动窗口、物理接管和断网。

## 记录模板

```json
{
  "entry": "official Dot",
  "device_os_version": "",
  "dotcontrol_version": "",
  "driver_version": "",
  "tunnel_client_version": "",
  "at": "",
  "device_nonce_call_evidence": "",
  "image_call_evidence": "",
  "action_id": "",
  "actual_ui_or_file_result": "",
  "human_takeover_result": "",
  "status": "not_verified"
}
```

只保留测试资料；不要把账号密钥、真实用户文档、完整桌面截图或私人窗口标题提交到开源仓库。
