# 诊断与验收边界

本文描述 `codex/release-diagnostics-v2` 的源码改进，基于发布分支 `971def0`。公开 alpha.7 的构建源、资产和历史验收仍以[发布支持记录](RELEASE_SUPPORT.md)及[发布 JSON](validation/published-0.2.0-alpha.7.json)为准；这些源码修改尚未发布为新安装包。

## 执行效果与回执

MCP 目录保持 13 个工具。`execute` 接收可选的结构化 `postconditions`，并将它们纳入动作的持久请求摘要。相同 `task_id/action_id` 和请求只返回原回执；更改后置条件会产生 `action_conflict`，不会再次提交原生输入。后续观察不改写历史 `unknown` 回执。

驱动原因码保留在回执的 `code` 中，诊断导出的 `execution_progress.state/code` 反映当前进程最近一次执行的最终回执。新增字段可选，既有状态、回执 JSON 字段和诊断 `schema: 1` 保持兼容。进度属于当前进程的瞬时信息，重启后应使用 `task` 查询持久回执。

| 驱动证据 | 状态 / 原因码 |
|---|---|
| effect confirmed | `succeeded / effect_confirmed`，若调用方提供验证条件，还必须通过这些条件 |
| effect none | `failed / effect_none` |
| effect refused | `needs_user / effect_refused` |
| effect partial | `unknown / effect_partial` |
| effect suspected_noop | `unknown / effect_suspected_noop` |
| 未识别或 unverifiable effect | `unknown / effect_unknown` |
| 结构化后置条件通过 | `succeeded / postcondition_verified` |
| 后置条件未通过或超时 | `unknown / postcondition_unverified` |

只允许审核过的固定原因码跨越驱动边界；任意原生消息、窗口内容和输入文本不会进入回执或诊断。取消、人工接管、超时或输入释放失败仍可使最终结果变为 `unknown`，不能用成功原因码覆盖这些边界。保留原因码本身不会将未确认效果升级成功。

## 结构化后置条件

以下为 `execute` 的附加参数示例。设备、目标窗口、动作和观察编号仍需来自本次已授权的实际查询。

```json
{
  "postconditions": [
    {
      "kind": "changed",
      "element": "observed-element-token",
      "property": "value",
      "expected": "new value",
      "max_wait_ms": 500
    }
  ]
}
```

最多 16 项；每项 `max_wait_ms` 为 1–2000，`expected` 最多 4096 字节。所有条件必须在各自时限内从新观察得到验证：

- `property`：指定元素的 `value`、`label` 或 `role` 精确匹配。
- `changed`：指定属性精确匹配，且与动作前观察中的值不同。
- `task_result`：新观察必须提供来源为 `application_state` 的独立任务结果。
- `text` 和兼容字段 `expect`：可见文本属于弱证据，不能将未确认效果提升为成功。`expect` 精确匹配时保留原驱动结果；对已确认效果，文本不匹配会留下 `postcondition_unverified`。Unicode 方向标记仍参与精确匹配。

当前适配器只对已确认的驱动效果进行结构化验证。partial、refused、suspected_noop 和 unknown 不自动重放，也不会靠后续文本被改写为成功。无效后置条件在原生输入前拒绝。

## 连接诊断与取消进度

驱动诊断包含采样时间、有效期和过期原因；过期样本不显示为已连接。隧道状态要求当前进程近期成功轮询。`tunnel_credentials_rejected`、`tunnel_permission_denied`、`requires_local_proxy_auth` 归入凭据层；DNS、TLS、超时和 HTTP 连接错误归入网络层。

`cancel_registered` 仅表示取消已登记；`native_pending` 包含原生调用和清理尚未结束。回执已返回 `unknown` 时，原生操作仍可能正在收敛。只有 `can_observe` 才表示允许重新观察；这些字段不能证明输入已撤回或任务结果成功。

## 验证范围

本分支使用合成窗口/驱动响应测试效果映射、结构化验证、MCP memory/HTTP 调用、幂等回执、日志恢复和脱敏诊断。MCP 端到端测试经过真实 schema、service、engine 与 Cua 验证器，只有原生输入和窗口读取使用 fixture。它不代表真实计算器、文本编辑器或表单已经通过验收。

可重复执行的检查：

```sh
go test -count=1 ./...
go vet ./...
go build -trimpath ./cmd/dotcontrol
python scripts/test_documentation_links.py
python scripts/test_release_status.py
python scripts/test_release_verifier.py
python scripts/test_macos_signature.py
```

本轮不安装或启动真实驱动、不推送或合并、不补造签名与真机证据。Windows race 需要 CGO 和可用 C 编译器；缺少编译器时应记录未执行，不能引用历史发布的 race 结果代替本分支结果。非 macOS 的签名测试会跳过原生 codesign 用例。

外部待验收包括三平台连续安装/恢复矩阵、代理与隧道唯一性、正常退出后不自动拉起、发布者签名/公证、官方 Dot 图片实际显示、用户 Mac 执行端及真实应用结果矩阵。PR #2 的其余安装界面和 LaunchAgent 路径仍需与 alpha.7 生命周期单独整合。更新检查保持用户主动触发，在发布者签名仍待验证时不会自动安装。
