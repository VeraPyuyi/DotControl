# CuaDriver 签名异常的定位与恢复

适用现象：Mac 原生校验报告 `invalid signature (code or signature have been modified)`，例如指出 `In architecture: arm64`。这证明被检查对象的签名验证失败，但尚不能确定损坏发生在上游发布、安装复制、后续修改，还是检查了另一份文件。保留完整命令、路径和输出。

## 比较已安装副本与原始归档

以下命令只校验文件并解压到新临时目录，不启动或覆盖应用，不读取凭据。请在 Mac 的 bash 中执行。此示例对应已固定的 0.34.1 arm64 归档；如果该归档不存在，先定位原安装包中的 `bundled` 文件，不用其他版本替换。

```sh
DC_ARCHIVE="$HOME/Library/Application Support/DotControl/downloads/cua-driver-rs-0.34.1-darwin-arm64.tar.gz"
DC_EXPECTED=83ffed8d41ef0f133976d594b02eb86dd59ef4a8721396b6e25078a59c6056d6
if [ ! -f "$DC_ARCHIVE" ]; then
  printf '%s\n' 'Archive missing; locate the original bundled archive first.'
else
  if printf '%s  %s\n' "$DC_EXPECTED" "$DC_ARCHIVE" | shasum -a 256 -c -; then
    DC_STAGE=$(mktemp -d -t dotcontrol-cua-signature)
    if tar -xzf "$DC_ARCHIVE" -C "$DC_STAGE"; then
      DC_FRESH_APP="$DC_STAGE/cua-driver-rs-0.34.1-darwin-arm64/CuaDriver.app"
      printf 'Fresh copy: %s\n' "$DC_FRESH_APP"
      codesign --verify --deep --strict --verbose=4 "$DC_FRESH_APP"
      printf 'Fresh verification exit: %s\n' "$?"
      codesign --verify --deep --strict --verbose=4 /Applications/CuaDriver.app
      printf 'Installed verification exit: %s\n' "$?"
      shasum -a 256 "$DC_FRESH_APP/Contents/MacOS/cua-driver" /Applications/CuaDriver.app/Contents/MacOS/cua-driver
      diff -rq "$DC_FRESH_APP" /Applications/CuaDriver.app
      printf 'Bundle comparison exit (0=same, 1=different, >1=error): %s\n' "$?"
      printf 'Diagnostic copy retained at: %s\n' "$DC_STAGE"
    fi
  fi
fi
```

Windows 侧本次核对的上游归档主程序 SHA-256 为 `12d4f43b9db80af6fe1dc295f362a50891121be964ec52b032f84b0ffd50640a`。哈希一致只说明字节相同，不能代替 macOS 原生签名、发布者、公证或系统运行检查。主程序相同而 bundle 校验不同，还须比较 Info.plist、嵌套组件和资源签名。

## 根据比较结果恢复

2026-10-11 当前案例已取得对照结果：用户确认已安装 app 和新解压 app 都在 arm64 原生签名校验中失败，主二进制 SHA-256 均匹配上述归档原件。因此当前不能按“只有安装副本损坏”的分支替换后宣告修复。

- 原件校验通过、安装副本失败：先停止 DotControl 代理和 CuaDriver，保留故障副本备份，再用通过校验的完整原件替换安装副本。替换后的 `/Applications/CuaDriver.app` 须重新通过原生签名和发布者检查，再按系统正常授权流程启动。保留设备配置、钥匙串和隧道设置。
- 原件也失败：停止安装，保留原始归档哈希、完整命令和错误。应调查该固定上游发布包及本机验证环境，不能将强制重新签名当成恢复原始发布者签名。
- 两份均通过：原先的失败可能针对其他文件，或当前状态已变化。核对最初失败路径、配置中的独立 CLI 路径，再检查代理与 daemon 状态；不能直接宣布修复完成。

恢复后按顺序验证：原生签名 → Cua daemon/权限状态 → 重启 DotControl 并检查状态 → 官方任务的 apps → observe → 一个计算器动作及新观察。代理当前不会自动重建已失败的驱动连接，因此只修复文件仍不足以证明现有会话恢复。任何 unknown 动作均须先核对界面，不得直接重放。

## 原件同样失败时的下一组只读检查

Windows 侧已对原始归档进行局部静态检查：主程序、嵌套光标程序、独立 CLI 的两种架构均未发现代码页哈希或 CMS 数学签名不匹配；主程序签名关联的 Info.plist、CodeResources、权限数据以及清单中的图标/配置描述文件哈希也匹配。这不检查 macOS 信任、公证、时间戳、运行策略或所有严格布局规则，不能代替原生 codesign 的失败结果。

请在 Mac 返回以下命令的退出码和输出，并完成上述整个 bundle 的 diff 比较：

```sh
sw_vers
uname -m
codesign --verify --verbose=4 --arch arm64 /Applications/CuaDriver.app
codesign --verify --deep --strict --verbose=4 --arch x86_64 /Applications/CuaDriver.app
codesign -dvvv --arch arm64 /Applications/CuaDriver.app
spctl --assess --type execute --verbose=4 /Applications/CuaDriver.app
```

用于区分是否仅某个架构失败、是否与深度/严格校验相关，以及系统版本和评估策略是否提供更明确的错误。当前没有证据证明上游包一定损坏，也没有证据证明本机验证环境就是根因；不能将历史本地源码构建的 ad-hoc 重新签名办法套用到这个 Developer ID 发布包。

## 当前案例：比较普通终端与任务运行环境

上述检查已由用户返回，详见 [原生检查记录](validation/mac-signature-context-2026-10-11.json)：macOS 26.6.2（25G83）、arm64；arm64 不带 deep/strict 仍失败，指定 x86_64 的严格检查也失败。签名描述显示 `Authority=(unavailable)`、`Info.plist=not bound`，Gatekeeper 返回签名子系统内部错误。已安装与新解压 bundle 的路径、文件类型、内容哈希、链接目标及可执行位均无差异；未比较扩展属性。

这些结果不能单独确定系统版本、证书信任或任务环境是根因。归档离线解析已发现发布者证书，不能把 `Authority=(unavailable)` 等同于“未附带证书”；显示已 stapled 也不等于公证评估通过。

下一步由用户在 Mac 桌面手动打开系统“终端”，以同一登录用户运行以下命令，保留每条退出码和输出；再在 Mac 本地 Codex 任务中运行相同的 TextEdit 检查。不要使用 sudo 或修改钥匙串/信任设置。

```sh
/usr/bin/codesign --verify --deep --strict --verbose=4 /Applications/CuaDriver.app
printf 'CuaDriver exit: %s\n' "$?"
/usr/bin/codesign --verify --deep --strict --verbose=4 /System/Applications/TextEdit.app
printf 'TextEdit exit: %s\n' "$?"
/bin/date -u
```

- CuaDriver 在普通终端通过、任务中失败：支持进一步调查执行环境差异，仍需单独复测驱动连接。
- TextEdit 在普通终端也失败：异常范围超出 CuaDriver，应调查本机验证环境并保留原始错误。
- TextEdit 通过、CuaDriver 在两种环境均失败：继续调查 CuaDriver 的签名与系统评估环节；这仍不能仅凭对照判定上游包损坏。

此对照尚待返回，不能据此宣布驱动或官方 Dot 验收恢复。

## 本次代码改动与验证边界

仓库安装脚本现在在执行、保留或复制 CuaDriver 时校验 bundle/独立 CLI；启动脚本在打开应用前校验 bundle。校验还要求上游固定发布者 Team ID `YCK386LBJ7`，不重新签名、不修改 Gatekeeper。辅助文件 `scripts/macos-signature.sh` 必须与安装和启动脚本一起使用。

三个控制流测试通过，验证成功继续、失败停止、校验器不可用停止，且路径按单个参数传递。这些测试模拟 codesign，不是 Mac 原生签名验收。新脚本尚未部署到用户 Mac，也未更新此前交付的完整 Mac ZIP。

依据：[Apple 代码签名说明](https://developer.apple.com/library/archive/technotes/tn2206/)、[Cua 0.34.1 发布及校验和](https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.34.1)、[该版本上游签名验证器](https://github.com/trycua/cua/blob/cua-driver-rs-v0.34.1/.github/scripts/verify_cua_driver_release_signatures.py)。
