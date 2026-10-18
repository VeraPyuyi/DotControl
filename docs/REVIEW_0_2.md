# 0.2 系列复盘与源码修复

审查基线：PR #1 的 `36bbfc9365cf138b846b9d1864c0aba7cc196ffb`。本轮只提交源码、测试和文档，不发布安装包、不升级设备、不合并 PR，不调整 SSH 或系统权限。已发布 alpha.6 的构建提交仍为 `e160c36912763c78cdfd84860a4b99f0ed4c6d6b`；下列新增修复不在其安装包中。

证据分为历史实测、当前代码与可重复测试。历史连接或计算器成功不证明新程序通过完整验收；归档、接口、执行回执和用户实际看到结果分别记录。以下不包含私人主机、密钥路径、任务标识、原始聊天或截图。

## 本轮确认并修复的七项缺陷

| 编号 | 症状及确定的代码原因 | 修复与边界 | 回归位置 |
| --- | --- | --- | --- |
| R1 | 保留配置卸载后，完整重装将已有配置一律当成非托管迁移，要求已经删除的旧可执行文件。 | 成功安装记录与状态目录绑定；正常卸载保留来源记录，允许原状态目录重装。空目录、失败安装和来源不明记录不能产生这项授权，换目录或再次卸载不能重新绑定。 | `cmd/dotcontrol/install_recovery_test.go`：`TestManagedUninstallAndReinstallPreservesState`；`internal/install/retained_state_test.go` |
| R2 | 修复入口对旧主程序先求摘要，文件确实不存在时还没启动修复便失败。 | 先核对托管清单，再区分缺失、被修改及非普通文件。缺失路径仍参加旧实例退出核实；新版本失败时恢复原配置与启动设置，明确返回 `rollback_previous_program_unavailable`，不假称已重启旧程序。完成恢复后允许再次修复。 | 同上：`TestManagedMissingExecutableRepairAndFailedHealthRetry`、`TestManagedMissingExecutableStillRequiresExitEvidence`、`TestManagedUntrustedExecutableIsNotMissing`、`TestMissingExecutableRepairRecoversInterruptedActivation` |
| R3 | `collectItem` 在 128 条回执后提前返回，导致之后的助手最终正文也丢失。 | 正文/图片收集与回执上限分离；增加可选截断标记；保持正文、回执输出与图片的容量上限。重复事件不重复保留结果，饱和且无变化的事件不再追加账本。 | `internal/codexbridge/result_limits_test.go`：最终正文、重复终止事件、中文边界、总输出与单条输出上限、饱和账本测试 |
| R4 | 重启恢复先跳过退役记录，未先失效仅存在内存中的结果，旧完成任务仍显示 `available`。 | 所有恢复记录先标记正文/图片不可恢复，再跳过退役任务恢复。保留原任务状态，不改账本，不创建、继续或派发任务。 | 同上：`TestRetiredCompletedResultUnavailableAfterRestart`；验证原始字节不变、重复请求返回原记录、零派发 |
| R5 | RPC 反序列化清除了原始消息，而调用方再靠错误字符串识别沙箱布局问题，专项分类无法命中。 | 解析时仅提取允许列出的安全原因 `terminal_sandbox_layout_unsupported`，调用方使用错误类型分类；其余仍为通用失败。原始错误正文不返回、不序列化，不放宽沙箱。 | `cmd/dotcontrol/terminal_setup_test.go`：`TestTerminalRPCFailureIsClassifiedWithoutPrivateMessage` |
| R6 | 共享列表的较早分页在新筛选之后返回，混入新结果并污染下一页游标。 | 请求和游标绑定搜索、工作目录、归档筛选及请求代次；异步返回前复核，旧结果/错误均忽略。编辑筛选立即失效旧游标，已勾选候选保留。 | `internal/onboard/testdata/history.test.cjs`：旧分页迟到、完整筛选变化、相同查询的请求代次；合计 21 项交互测试 |
| R7 | 历史读取的 ready 只由最初检查设置，后续连接或接口失败、Codex 路径变化不更新状态。 | 本机来源变更使检查失效；明确的连接、协议或内部超时撤销 ready，显式检查成功才恢复。来源代次和健康状态序号阻止迟到结果覆盖新状态。调用方取消、无效游标或未找到会话不等同于源故障。 | `internal/codexhistory/health_test.go`、`cmd/dotcontrol/history_test.go`：失败恢复、来源变更、旧检查/读取竞争、内部超时、取消检查不能隐藏真实失败、共享策略保留 |

安装实现位于 `internal/install` 与 `cmd/dotcontrol/install_activation.go`；结果投影位于 `internal/codexbridge/runtime_events.go`；共享界面位于 `internal/onboard/web/history.js`。独立审查中发现并修正了新实现的来源绑定、大小写/链接别名、提交前中断、内部超时分类与饱和账本写入边界，未将未经处理的审查建议直接视为已修复。

安装状态绑定只接受规范路径相等或经过无链接路径检查的同一文件系统实体，不以不区分大小写的字符串比较授予归属。已有来源记录的拼写保持不变。此前成功卸载的证明在再次激活期间保留，中断恢复不会把它当成一次性令牌消耗。

## 对外兼容与隐私

- MCP 仍为 **13 个工具**，不新增 SSH、实验、审批或任意命令接口。
- `codex_job.runtime.text_truncated`、`evidence_truncated` 及每条 `evidence[].output_truncated` 为可选布尔字段。省略表示未报告截断；历史记录不会补写。正文上限 32,768 字节，回执最多 128 条，每条输出 8,192 字节、总输出 65,536 字节。UTF-8 截断不留下半个汉字。
- `available` 表示存在有界结果，不表示信息完整或任务目标已独立验证；调用方需读取截断标记、命令退出码和 `verification`。模型的“完成”不能代替验证。
- 正文和图片仍只在内存中，重启后（包括退役任务）显示 `unavailable_after_restart`。这是数据留存边界，不在本轮增加正文落盘。
- 不改设备、隧道、凭据、暂停/撤销、白名单或终端权限。首次历史启用默认全部、已保存白名单不变、空白名单全部拒绝仍有原测试覆盖。
- 新安装器能读旧清单；没有状态绑定的旧卸载记录无法自动证明配置归属，仍要求明确迁移。旧安装器不认识新增清单字段；不要用旧安装器管理新清单。旧代理运行不读取此清单，程序回滚与安装器降级是不同路径。

## 整个 0.2 系列的历史问题

本表回顾已有记录和基线实现，不表示本轮重跑了云端或真实桌面。版本与历史交付证据见 [支持矩阵](RELEASE_SUPPORT.md)、[早期实施记录](IMPLEMENTATION_0_2.md) 和 [验收矩阵](validation/ACCEPTANCE.md)。

| 类别与症状 | 证据、根因确定程度 | 已有处理与自动检查 |
| --- | --- | --- |
| Mac 驱动路径、系统权限为 unknown、活动监控不可用 | 进程存在不证明 TCC 权限；受限任务内“未运行”也不能代表普通登录会话状态。 | 固定应用位置和稳定身份，单独检查权限与真实应用查询；未知能力拒绝执行。检查环境须与实际代理一致。 |
| 归档摘要正确，但 CuaDriver 签名失败 | 安装副本和原归档解包均失败且主程序相同，不支持“仅安装后主程序损坏”；早期记录不能唯一定位发布包或系统验证环境。 | 摘要、原生签名与实际启动分开验证。CI 的后续成功不能倒推出早期失败根因；不重签上游驱动或关闭系统校验。`scripts/test_macos_signature.py` |
| 空闲 0.1 代理升级也显示等待任务停止 | 旧进程占有生命周期锁且没有新版退出接口，不能据此推断有活动桌面任务。 | 核实身份、正常退出与子进程；不删除锁、不强杀。`TestLegacyShutdownIsNotReportedAsActiveTask`、`TestUpgradeShutdownNeverFallsBackToTaskCancellation` |
| `driver_unavailable` 掩盖原因、取消读取后连接失效、session has ended | 原错误分类缺失及固定会话复用都有代码/驱动证据；不是每次都由权限不足导致。 | 保留安全分类，隔离会话名，恢复连接后失效旧观察。`TestDriverReconnectAndIndependentClientsDoNotReuseEndedSession`、`TestSDKReadCancellationKeepsSessionUsable`、`TestProbeKeepsReadFailureBeforeClosingSession` |
| paused 被当成断线；终端被驱动问题阻塞 | 暂停、隧道、驱动、终端能力是不同状态。 | terminal_only 不检查桌面驱动/空闲；恢复连接不解除本机暂停，终端与历史仍受暂停/撤销保护。 |
| 隧道 404、系统代理未被采用、readyz 假在线 | 控制连接直连失败与已有静态代理差异有实测；本地 HTTP 200 不等于成功云端轮询。 | alpha.4 引入静态代理选择、回环直连和当前隧道进程成功轮询/新失败/证据过期判定；配置切换等待客户端退出，不热切换正在处理的请求。见 [网络说明](NETWORK.md) 及 `internal/tunnel` 测试。 |
| DNS、TLS、任务禁网和密钥问题混淆 | 相同上层错误可能来自不同网络阶段；仅 network_error 无法推出 DNS 或密钥失败。 | 分层诊断，保留安全错误类别，不自动修改 DNS、代理或 TLS 校验。 |
| 工具目录停留在旧六个窗口接口 | 本地 MCP 与官方插件缓存/会话已加载目录不一致。 | 使用官方刷新工具入口；当前核对 13 个接口，缺少提交接口立即停止，不能把终端命令塞进 execute。 |
| unknown 动作及相同 expect | 缺少后置条件或条件没有证明新状态，会导致无法验证；unknown 不等于动作必定未发生。 | 保留原回执，重新观察后决定下一步；不重发未知动作。计算器成功与每一步验证、截图显示分别记录。 |
| Python 3.9 上传脚本导入失败，之后 Library 接口缺失 | 类型注解版本错误、任务禁网、完整目录缺少 prepare_uploads 是先后不同阻塞；不是一个依赖升级能全部解决。 | 删除旧上传脚本依赖，使用原生 MCP 图片和宿主实际可用的附件能力；本地显示、附件存储与源 Dot 显示不互相替代。 |
| 本机审批页失效、反复过期、旧权限配置 | 本机受保护会话和审批期限是不同边界；聊天“通过”不等于提交表单。 | 会话续期、多标签页、迟到响应保护已有回归；alpha.5 下线旧审批转发，额外人工要求安全中断。旧 on-request 不自动改 never。 |
| 长向导、模式含义不清、刷新覆盖编辑 | alpha.5 改为四区控制台，alpha.6 明确默认全部/显式空白名单拒绝与预览确认。 | 保留草稿、候选和认证多标签页；`internal/onboard/session_test.go` 和共享交互测试。本轮 R6 进一步修复分页竞争。 |
| 失败升级、旧文件占用、清理中断 | 新版健康检查前不得清旧程序；开始清理后不得回滚到部分删除的旧版。 | 安装事务、恢复快照、进程引用和摘要核对；清理失败保持新版并重试。R1/R2 补齐保留配置卸载与主程序缺失的路径。 |

## SSH 预检：环境差异不合并成一个代码回归

alpha.5 起内置 SSH/实验助手已下线。这里仅总结用户提供的后续 Windows Codex 任务回执，不恢复旧实现、不连接服务器，也不将聊天中的密码、地址或密钥引用写入项目。

| 观察到的证据 | 能得出的结论 | 不能得出的结论 |
| --- | --- | --- |
| ssh 解析到 `.sbx-denybin`；版本检查没有 OpenSSH 版本 | 当前任务受限入口未执行真实 SSH。 | 系统没有 OpenSSH；外层检查脚本退出 0 就证明 SSH 可用。不得绕过限制脚本。 |
| SOCKS 错误 5 / connection refused | 当次转发被代理拒绝，尚未完成目标 SSH 登录。 | 密码或公钥错误。 |
| UNKNOWN port 超时、local output errno=22 | 当次连接/转发或本地输出存在问题，需保持代理与实际配置逐层核实。 | 与上轮拒绝是同一根因，或应自动改直连。 |
| 握手及主机校验通过，agent socket 不存在 | 当前任务无法通过当次 agent 通道提供身份。 | Windows 整机没有 agent、密钥无效或服务器已接受公钥。 |
| agent socket Permission denied | 当前任务访问所选 agent 通道被拒绝。 | 文件密钥认证也必然失败；修复方式是放宽全局权限。 |
| 早期授权/指定文件认证成功与后续 agent 失败并存 | 这些回执属于不同任务上下文/认证入口，不能互相覆盖。 | 已证明 DotControl 的文件密钥认证发生代码回归；后续轮次一定已登录。 |

再次预检应沿用用户授权的任务、代理、主机信任和认证方式；逐项记录实际 SSH 退出码与服务器登录证据。后期授权已收窄为“能登录即可”，不因此继续 GPU、模型或部署检查。本轮只做源码测试。

## 未关闭的限制

1. **官方 Computer Use 未完成验收，继续关闭。** App Server 线程创建、应用发现、Cua 计算器操作和某一工具直接调用均不能替代官方真实轮次的授权、前台接管、中断与图片回传验证。
2. **未知任务恢复仍有限制。** 缺少确定线程/轮次或退役任务无法安全恢复时，不重放、不删除账本、不伪造成功。这些记录可能继续阻塞配置变化或升级。终端任务没有全设备串行锁：已有保护限制同任务续聊、配置变更/升级及相关桌面执行权，并非已证明所有新终端请求都被全局禁止。调用方不得用新编号替代未知的旧任务。
3. **Windows 正在执行的安装程序自卸载文件占用仍待实际核实。** 本轮测试模拟安装入口和退出证据，没有删除真实运行程序；不将隔离清理测试写成真实自卸载通过。
4. **任务结果不是完整审计日志。** 当前投影保留退出码和有界输出，没有保存完整 command/cwd。历史记录中关于“命令事件没有命令字段”的描述仅能证明当时保留的投影缺字段，不能证明官方协议永远不提供。原始历史文件不改写。
5. **来源不明或更旧的安装记录仍需明确迁移。** 不以“缺文件”为由信任被替换目录、用户附加文件或已更改主程序。不扩大卸载删除范围。

## 本轮验证记录

测试使用临时目录、模拟 App Server 与脱敏固定数据；未操作真实桌面、代理状态、SSH 或历史共享权限。完整安装入口覆盖中文路径、原配置语义与独立账本字节保留、正常重装、失败回滚、再次修复、中断恢复、未知退出拒绝、来源绑定与别名拒绝。

提交前运行对应回归、完整 Go 测试、竞态检测、静态检查及 21 项共享界面测试。原有白名单、空清单拒绝、旧游标拒绝、会话续期、多标签页和慢响应测试继续执行。三平台原生 CI 结果以本次 PR/提交关联的 `verify` 记录为准；构建测试不代表真实 Mac 桌面或新的跨电脑验收。

本轮 Windows 本地实际结果（2026-10-14）：

| 检查 | 结果 |
| --- | --- |
| `go test -timeout 3m -count=1 ./...` | 通过 |
| `go test -race -timeout 3m -count=1 ./...` | 通过 |
| `go vet ./...`、`go build -trimpath ./cmd/dotcontrol` | 通过；构建产物仅用于检查，未安装 |
| `node internal/onboard/testdata/history.test.cjs internal/onboard/web/history.js` | 21/21 通过 |
| `python scripts/test_macos_signature.py` | 4 项，1 项原生 Mac 检查在 Windows 跳过，其余通过 |
| `python scripts/test_release_verifier.py` | 3/3 通过；不等于本轮重新打包或下载发布文件 |
| 独立审查 | 安装、任务结果、会话共享三组复审通过；具体反馈经核证后修复并补测试 |
| 变更检查 | `git diff --check` 通过；待提交文件的敏感模式扫描及文档相对链接检查通过 |

原生 CI 在 [PR #1 的检查记录](https://github.com/VeraPyuyi/DotControl/pull/1/checks) 中按提交区分，包含 Windows、Mac ARM 和 Intel 的完整测试、竞态、vet 与构建；原生 Mac 另编译活动组件。不要把旧 alpha.6 的通过记录当成本次提交的结果。
