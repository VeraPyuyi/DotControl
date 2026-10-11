# 官方 Dot 双向跨电脑验收

## Windows → Mac：6×8=48

本轮只验证核心跨电脑路径，暂不推进自动安装。此前官方 Dot 的 Mac 本地任务完成 7×8=56，不能替代本轮验收。

**2026-10-12 本轮通过：** 官方 Dot 在已授权 Windows 上运行本地任务，经 DotControl Mac 后台完成 6×8=48。五条持久回执均为 succeeded/background/verified，本会话独立回读一致。最终原始 PNG 经原生 Library 接口返回，已在官方 Dot 主对话实际渲染；本会话观察到图片中的 6×8、48，并核对原文件大小与 SHA-256。父 Dot 结束时再次确认 attached 仍为 Windows，Mac attached=false。

## 必须成立的证据

1. 官方 Dot 的个人电脑入口为源 Windows；记录 attached、任务授权以及实际执行环境身份。通过 Windows 本地任务执行时，电脑名与 Windows 系统须在该任务核实。
2. 执行端仅使用 DotControl Mac 插件访问目标 Mac；Mac 无需成为本轮官方 attached 电脑，不创建或继续 Mac 本地任务。
3. 在 Mac 计算器完成新的 6×8=48；每步新观察、新控件，显式传入精确顶层 expect，只允许后台动作。
4. 最终独立 observe 的截图与 AX 一致，task 查询五条持久回执；官方 Dot 返回实际结果。逐项记录截图到达模型、本地保存和用户可见交付，不混为一项。
5. 结束后确认官方 attached 电脑仍为源 Windows。不得以本会话直接操作 Mac、旧 Mac 任务或单独 devices 成功代替通过。

本轮调用时间、证据来源和图片哈希见 [跨电脑记录](validation/cross-device-2026-10-12.json)。公开记录不包含设备、环境、账号或云端文件标识。

## 本轮操作约束

- 新 task_id：`official-dot-win-to-mac-20261012-calc68-0038`。
- action_id：`clear-all`、`digit-6`、`multiply`、`digit-8`、`equals`。
- 先 devices 核验目标设备、暂停、活动检测和锁定状态，再 apps 重新发现计算器。不能复用旧 PID、window_id、observation_id 或控件 token。
- 依据现场 AX 格式传 expect。此前计算器格式对应的 JSON 为 `"\u200e0"`、`"\u200e6"`、`"\u200e6\u200e×"`、`"\u200e6\u200e×\u200e8"`、`"\u200e48"`；实际解码值包含 U+200E，不能传字面量反斜杠 u。现场已经存在的文本不能独自证明动作发生。
- 每个动作只发一次；不确定或超时后只读核对，保留原回执，不重发点击。暂停、锁屏、人工使用目标窗口、后台不支持或真实权限阻塞时明确报告。
- 不修改驱动、代理、隧道、权限或旧任务记录。若图片交付失败，保留原图，继续返回已有文字结果与回执，不为交付再次操作计算器。
- 不重试已确认缺少 Library prepare_uploads 的旧上传脚本；使用实际可用的原生图像交付能力，并如实记录接收位置。

## 判定范围

五步动作及结果返回通过，可证明这一次 Windows 官方执行入口到 Mac 插件的跨电脑操作。用户可见截图交付另有明确检查项；未交付时不得宣称全部验收通过。单次成功不代表多应用、物理接管、故障恢复、网关或自动安装已完成。

## Mac → Windows：4×9=36

2026-10-12，官方 Dot 的 Mac 本地任务经 DotControl Windows Validation 完成计算，并将最终截图返回官方 Dot。父 Dot 报告结束时 attached 仍为 Mac，Windows attached=false，未用 Windows 本地任务完成计算。本会话独立回读全部五条持久回执，并打开官方 Dot 中的 `calc49-final-36.png`，实际看到 `4 × 9 =` 和 `36`。AX 文本 `显示为 36` 来自官方任务报告；本会话没有重新观察或操作计算器。

| action_id | state | mode | code |
| --- | --- | --- | --- |
| clear-all | unknown | background | execution_uncertain |
| digit-4 | succeeded | background | verified |
| multiply | unknown | background | execution_uncertain |
| digit-9 | succeeded | background | verified |
| equals | succeeded | background | verified |

原清除没有重发或改写，状态和时间戳与历史记录一致。任务重新观察确认显示为 0、表达式为空后继续，每个后续动作只发送一次。乘号的 expect 为点击前就存在的 `显示为 4`，不能独自证明动作发生；任务在 unknown 后重新观察到表达式变为 `表达式为 4 × `，才继续输入 9，未重复点击乘号。

**反向核心计算与用户可见图片交付通过，五步回执全部 verified 的检查项未通过。** 最终结果证据不能倒推清除成功，也不改写乘号回执。原始 PNG 字节与哈希未在本会话另行下载校验；本地保存的是官方 Dot 图片查看器的截图证据。来源与时间戳见 [反向跨电脑记录](validation/mac-to-windows-2026-10-12.json)。此前隧道故障、驱动恢复和 unknown 记录均保留为历史，不用最终结果覆盖。

现在两个方向都已有实际跨电脑计算和可见图片交付。后续产品验收仍包括多应用、物理接管、持续稳定性、真实双机网关和自动安装，不能据此宣称全部实施计划完成。
