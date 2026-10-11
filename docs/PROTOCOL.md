# DotControl 0.1 接口

MCP Streamable HTTP 位于本机 `/mcp`，由官方 Go SDK 处理协商、初始化、工具列表与调用；本机调试还提供 `dotcontrol mcp` stdio 桥。外部通过官方隧道连接，不暴露匿名公网 HTTP。SDK 协议版本不是本项目接口版本。

| 工具 | 输入 | 输出 |
| --- | --- | --- |
| devices | `{}` | 当前入口设备列表，含 device_id/name/os/version/paused/activity/nonce |
| apps | device_id | 该设备的应用和窗口发现数据，包含 pid/window_id |
| observe | device_id、target | observation_id、created_at、expires_at、窗口可访问性数据、MCP image 内容 |
| execute | 下述 Action | 一条持久回执 |
| task | device_id、task_id | 该任务的动作回执 |
| cancel | device_id、task_id | 永久阻止该任务后续动作，并请求取消当前动作 |

`target` 是 `{"pid":1234,"window_id":"5678"}`，必须来自该设备发现结果。window_id 使用字符串避免整数精度丢失。名字不作为路由键。独立入口遇到其他 device_id 会拒绝，不猜测目的设备。

## Action

```json
{
  "device_id": "来自 devices",
  "task_id": "demo-20261010",
  "action_id": "click-1",
  "observation_id": "来自刚刚的 observe",
  "target": {"pid": 1234, "window_id": "5678"},
  "kind": "click",
  "args": {"element_token": "当前观察的控件 token"},
  "mode": "auto",
  "expect": "Count: 1"
}
```

设备、任务、动作编号只接受 1–128 个 ASCII 字母、数字、`.`、`_`、`:`、`-`，首位为字母或数字。任务编号应全局唯一；action_id 在同一任务内唯一。重试必须保持整个请求一致，包括观察编号。修正或重新观察后使用新的动作编号，仅在确认原动作未执行时再次修改。

| kind | args 字段 |
| --- | --- |
| click | element_token，或 x/y；可选 button |
| type_text | text，可选 element_token |
| set_value | element_token、value |
| press_key | key，可选 element_token |
| hotkey | keys 数组，可选 element_token |
| scroll | direction、amount、by；可选 element_token 或 x/y |
| drag | from_x/from_y/to_x/to_y；前台模式暂不开放 |

精确字段以 [适配器 allowlist](../internal/driver/cua.go) 和驱动 MCP schema 为准。未知字段全部拒绝，客户端不能传 pid、session、focus 或 capture_id 覆盖目标。坐标仅相对于本次截图，由代理附加对应 capture_id；优先结构化控件。`type_text` 最多 4096 个 Unicode 字符；单动作 args 最多 16KiB。

mode 为 auto/background/foreground。auto 尝试后台，是否可以前台由本机闲置状态与执行器能力决定；后台拒绝不会悄悄抢焦点。`expect` 可指定精确可见的新文本作为后置条件，只能确认此前不存在、之后新出现的值，不能用请求回显、隐藏字段或旧值证明成功。它不是完整业务任务断言。

`expect` 不会自动移除 Unicode 方向标记。例如本次 Mac 计算器真机返回的显示文本包含 U+200E（LEFT-TO-RIGHT MARK）：显示 `42` 时，实际 AX 字符串为 JSON `"\u200e42"`，仅填写 `"42"` 不会匹配。应根据新观察中的真实文本格式构造后置条件，不能靠截图外观猜测。收到 unknown 后先重新观察；核对已发生的效果后再决定下一步，原动作回执不会因后续观察而自动改写。

驱动连接处理区分只读取消与动作传输不确定：`list_apps`、`list_windows`、`get_window_state` 的调用若明确返回取消或截止时间错误，只终止本次请求，允许后续新的查询；不会自动重试本次请求。动作调用的通信错误及其他不明传输错误仍封闭连接，要求重建后再观察，不自动重发动作。已取消的请求不会派发到驱动。认证本地 `status` 的 `driver_status` 提供最近失败的操作名、分类、可选 RPC 数字码及时间，不记录原始错误正文、参数或界面内容。`driver_connected` 表示最近已知连接状态，不是一次新的应用或窗口读取证明；诊断信息只驻留当前进程，应在重启前读取。

计算器验收必须检查初始状态，不能机械执行“清除”：若新观察确认显示为 0 且没有待计算表达式，可直接开始所需计算，记录“无需清除”，不制造一条无法由界面变化验证的动作。如果已经发出清除并得到 unknown，保留原回执，不换编号重发；重新核对完整现场后，仅在后续步骤所需前提成立时执行不同的下一步。即使最终计算成功，也不能把这条 unknown 改称成功。若要单独验证清除功能，应另设测试，先建立并核验非零状态，再验证清除到 0 的变化。

在计算器严格验收中，如果驱动自身不能确认效果，应在每次 execute 时给出可核验的精确后置条件。基于本次已观察到的 AX 格式，7×8 这一轮的 JSON 字符串分别为：

| 动作 | expect 的 JSON 表示 |
| --- | --- |
| 全部清除 | `"\u200e0"` |
| 7 | `"\u200e7"` |
| 乘 | `"\u200e7\u200e×"` |
| 8 | `"\u200e7\u200e×\u200e8"` |
| 等于 | `"\u200e56"` |

JSON 解析后这些转义须成为真正的 U+200E 字符；`"\\u200e56"` 会传入反斜杠字面文本，含义不同。条件还须与当时新观察的格式一致，不能直接套用到其他版本或应用。该表供核对请求参数和诊断后的新测试使用，不授权重发未知动作或修改历史回执。

MCP observe 返回截图与聊天附件交付是两层能力。DotControl 使用 Go 返回 MCP ImageContent，不依赖 Library 上传；任务另行保存/上传附件时，应记录上传工具自己的错误及运行环境。仅保存本地文件不能算用户已收到图片，附件失败也不能反推截图采集失败。本地任务预览与 Dot 云端附件分别验收。当前 Mac 原上传脚本访问的完整目录没有所需 Library 接口，停止重复该路径；附件交付不再阻止独立的动作回执复测，完整验收仍须包含用户可见图片。具体步骤见 [官方 Mac 复测](MAC_OFFICIAL_RETEST.md)。

## 状态与恢复

| state | 含义及下一步 |
| --- | --- |
| succeeded | 驱动确认了效果，或明确的界面后置条件通过；最终任务继续核对产出 |
| failed | 已知没有效果或参数不支持；根据 code 修正 |
| needs_user | 人正在使用目标、缺少权限、背景路径不支持等；解释具体阻塞，用户处理后重新观察 |
| unknown | 已发出但效果无法确定，或执行进程/连接丢失；先观察与核对，禁止直接重放 |
| dispatching | 内部已落盘的发送状态；重启恢复为 unknown |
| unsupported | 没有允许的后台执行路径；解释限制，不视为成功 |
| cancelled | 在发出前取消，或取消记录；已发出的副作用不能撤回 |

同一 task/action 的重复请求返回原回执；同编号异参拒绝。暂停、恢复、窗口移动、观察过期、任务取消不会让原动作再次执行。网络客户端不得自动生成新编号重试。

`Receipt` 包含 device_id、task_id、action_id、state、实际 mode、code、message、updated_at。不返回输入正文或内部摘要密钥。稳定错误码可见实现和回归测试；预览版新增错误码不算成功，客户端对不认识的状态必须停止修改并报告。

## 数据保留

截图和可访问性信息只在内存中短暂保留并作为工具结果返回 Dot。动作日志保存必要元数据和带密钥摘要；没有截图/键盘正文。调用方收到工具结果后可能有自己的保留政策。请勿把真实桌面内容写入演示日志或公开仓库。
