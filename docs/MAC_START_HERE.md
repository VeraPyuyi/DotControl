# 在 Mac 本机开始验收

这是开发预览包，包含 Apple Silicon 和 Intel 两种代理、Cua Driver 0.34.1 原始归档、源码及许可证。安装器自动选择架构并核对 SHA-256。Windows 的官方 Dot 调用已验证；用户提供的 Mac 诊断已确认代理与活动监控运行，系统权限、桌面操作和官方 Dot 到 Mac 的连接仍待验收。

## 1. 复制和解压

把 `DotControl-Mac.zip` 复制到 Mac 的“下载”文件夹并解压，得到 `DotControl-Mac` 文件夹。这里没有 API key，也没有 Windows 的设备配置。不要移动 Windows 的 `.state` 或凭据到 Mac。

如果 Mac 尚未安装 Apple 命令行工具，先在终端运行：

```sh
xcode-select --install
```

按系统提示完成安装；如果显示已经安装，继续下一步。

## 2. 安装本机部分

在 Mac 的终端复制执行：

```sh
cd "$HOME/Downloads/DotControl-Mac"
DOTCONTROL_SKIP_TUNNEL=1 bash scripts/install-macos.sh 'DotControl Mac'
```

这个步骤使用包内的预编译代理，不需要另装 Go 或 Homebrew，也不连接云端。系统活动检测程序会在 Mac 本机编译并以固定应用标识签名。安装目录为 `~/Library/Application Support/DotControl`。CuaDriver.app 位于 `/Applications`，DotControlActivity.app 位于 `~/Applications`；已有设备身份和同版本 CuaDriver.app 会保留。

这不是公证过的 `.pkg` 安装器。如果系统阻止某个组件，请保留完整错误提示并停止该步骤；安装器不会关闭 Gatekeeper、删除 quarantine 或自动授予权限。Mac 真机验证之前，不应将“文件安装完成”视为桌面功能已通过。

## 3. 权限与本地诊断

按系统实际提示，在“系统设置 → 隐私与安全性”中检查：

- CuaDriver.app：辅助功能、屏幕录制。
- DotControlActivity.app：输入监控。

先启动正确位置的驱动，再触发它自己的授权流程：

```sh
open -n -g /Applications/CuaDriver.app --args serve
/Applications/CuaDriver.app/Contents/MacOS/cua-driver permissions grant
/Applications/CuaDriver.app/Contents/MacOS/cua-driver permissions status --json
```

在系统提示中开启 CuaDriver 的辅助功能与屏幕录制，并接受要求的退出重开；较新 macOS 还可能询问直接屏幕捕获。授权必须由你在本机完成；如果实际权限归属不是 CuaDriver，先记录提示。权限状态必须能读到实际布尔值，`unknown` 不代表已经授权。

**早期安装包的路径修复：** Cua 0.34.1 的授权程序固定打开 `/Applications/CuaDriver.app`，早期 DotControl 包错误地放到了 `~/Applications/CuaDriver.app`。先在代理终端 Ctrl+C，然后执行 `cua-driver stop`。通过 Finder 的“前往文件夹”打开 `~/Applications`，将 CuaDriver.app **移动**到 `/Applications`，按系统要求确认；如果目标已有同名应用，不要覆盖，先核对版本。DotControlActivity.app 留在原处。然后执行上面的启动与授权命令。设备配置和凭据无需重建。旧包里的启动脚本仍指向旧路径，因此修复后用下面的绝对路径启动代理，或改用更新后的启动脚本。

在第一个终端启动本地代理，并保持它打开：

```sh
cd "$HOME/Downloads/DotControl-Mac"
bash scripts/start-macos.sh local
```

在第二个终端执行：

```sh
DC="$HOME/Library/Application Support/DotControl/bin/dotcontrol"
"$DC" pause
"$DC" doctor
"$DC" status
```

将 `doctor` 和 `status` 的结果返回给协作者。它们不会输出 runtime key。首轮主要检查 `driver_connected`、`activity.available`、`activity_monitor_available` 与系统版本。`paused=true` 是上述命令主动设置的暂停；此时 observe/execute 会被拒绝，devices/status 和 apps 发现仍可调用。apps 成功不表示允许截图或输入。没有配置隧道时，tunnel id/key 缺失是预期结果。

如果 `driver_connected=false`，先完成 Cua 授权，再在代理终端 Ctrl+C 后重新启动：代理只在启动时尝试一次驱动连接，授权完成不会自动重连。旧包可直接运行：

```sh
"$HOME/Library/Application Support/DotControl/bin/dotcontrol" serve --no-tunnel
```

再在另一个终端检查 `status`。若仍不连接，请返回 `cua-driver permissions status --json`、`cua-driver doctor --json` 和 `dotcontrol status`；不要返回凭据或完整私人桌面内容。`authorization host: unavailable` 单独不能证明系统权限缺失。

**devices 成功，但 apps 返回 driver_unavailable：** 当前代理会将驱动传输、应用发现和窗口发现的失败合并为这个错误，外层 `INVALID_ARGUMENT` 也不能据此证明设备编号错误。先收集以下只读诊断，不要重建隧道或运行密钥：

```sh
"$HOME/Library/Application Support/DotControl/bin/dotcontrol" status
/Applications/CuaDriver.app/Contents/MacOS/cua-driver --version
/Applications/CuaDriver.app/Contents/MacOS/cua-driver status --json
/Applications/CuaDriver.app/Contents/MacOS/cua-driver permissions status --json
/Applications/CuaDriver.app/Contents/MacOS/cua-driver doctor --json
```

`driver_connected` 当前仅表示代理启动时是否建立驱动连接，并非实时健康检查：`false` 表示启动连接未成功，`true` 仍不能排除之后的传输断开或枚举失败。先核对这些结果，再决定是否需要重启代理或处理明确的系统授权错误；不要把一个通用错误直接归因为权限不足。

若出现 `invalid signature (code or signature have been modified)`，按 [签名异常定位与恢复](MAC_SIGNATURE_RECOVERY.md) 比较已安装副本和经过哈希校验的新解压原件。仓库新版脚本已加入签名与发布者校验，失败即停止；旧安装 ZIP 尚未包含这项改动。不要通过重新签名掩盖来源尚未确定的差异。

`tunnel_id_configured=false` 和 `runtime_key_in_os_vault=false` 表示云端配置未完成；`tunnel_version=missing_or_failed` 表示客户端不存在或版本命令失败，需单独检查。`mac_real_device` / `official_dot_invocation` 是这个预览版保守固定返回的验收占位字段，不会随本机诊断自动变成通过。

如果 activity 不可用，重新检查权限及当前登录会话，然后停止并重启代理；不能把不可用当成空闲。请勿在锁屏或未登录的用户会话验收。

## 4. 本机通过后连接官方 Dot

Mac 要使用独立隧道和独立插件，不能复用正在连接 Windows 的 tunnel ID。

1. 在 Mac 安装官方 Homebrew（如果尚未安装，使用 <https://brew.sh> 的官方说明），然后运行 `brew install openai/tools/tunnel-client`。本包固定要求 0.0.16；版本不符时先核对，不绕过版本检查。
2. 重新运行 `bash scripts/install-macos.sh 'DotControl Mac'`。已有设备身份会保留；核对配置里的 tunnel 程序路径。
3. 在官方 Platform 创建 Mac 的独立隧道，以及仅具 Tunnels Read + Use 的运行 key。将 Mac 的 tunnel ID 填入 `~/Library/Application Support/DotControl/config.json` 的 `tunnel_id` 字段；不改设备 ID，不在这个文件中写 key。
4. 在 Mac 运行 `"$HOME/Library/Application Support/DotControl/bin/dotcontrol" credential`，按隐藏输入提示录入运行 key，存入 Mac 钥匙串。
5. 在运行本地代理的终端按 Ctrl+C，再运行 `bash scripts/start-macos.sh tunnel`。另一个终端检查 `doctor --online` 和 `status`；必须看到 tunnel ready。
6. 在 ChatGPT Plugins 创建“DotControl Mac”私有插件，连接 Mac 的隧道，认证选择“无需身份验证”。这表示不另加应用 OAuth，仍有官方隧道授权和本机令牌校验。
7. 先让真正的官方 Dot 调用 devices 返回 Mac 的设备信息与 nonce。然后只对专用测试文件和窗口运行 `resume` 后的桌面验收；完成后 `pause`。

前台操作需要至少 60 秒无人输入；后台能力不足会返回需要用户处理，不应强制抢焦点。Mac 即使完成本机诊断，也不等于这些端到端项目已经通过。

## 退出

先执行 `"$HOME/Library/Application Support/DotControl/bin/dotcontrol" pause`，再在运行代理的终端按 Ctrl+C。预览包不设置开机自启。撤销访问与故障诊断见 [INSTALL.md](INSTALL.md)。
