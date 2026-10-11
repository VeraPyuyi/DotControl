# 可复现演示

这里的测试素材与普通个人文件分开。不要将本地演示结果写成官方 Dot 验收成功。

## Windows 原生窗口

在项目目录使用 PowerShell，先构建代理和专用窗口：

```powershell
New-Item -ItemType Directory -Path dist,.state -Force | Out-Null
go build -trimpath -o dist/dotcontrol.exe ./cmd/dotcontrol
& "$env:WINDIR/Microsoft.NET/Framework64/v4.0.30319/csc.exe" /nologo /target:winexe /out:dist/DotControlFixture.exe /reference:System.Windows.Forms.dll /reference:System.Drawing.dll demos/windows/Fixture.cs
./dist/dotcontrol.exe --state .state/windows-demo setup --name 'DotControl 测试电脑' --listen 127.0.0.1:19317 --driver '替换为安装的 cua-driver.exe 完整路径'
```

在一个终端运行代理：

```powershell
./dist/dotcontrol.exe --state .state/windows-demo serve --no-tunnel
```

在另一个终端运行：

```powershell
Start-Process ./dist/DotControlFixture.exe -ArgumentList ('"' + (Join-Path $PWD '.state/fixture-output.txt') + '"') -WindowStyle Normal
./dist/dotcontrol.exe --state .state/windows-demo resume
./scripts/Test-WindowsFixture.ps1 -State .state/windows-demo
```

保持只有一个测试窗口；每次重新测试前关闭并重新打开它，使计数从 0 开始。测试通过 authenticated API 发现窗口、截图和控件、写中文、保存并逐字核对文件、增加计数并重发相同动作。预期 `duplicate_effect_count=1`、`focus_unchanged=true`。用户应在另一个应用中操作；这个测试只检查前后焦点，不能证明瞬间从未改变焦点。

## 浏览器

打开 `demos/browser/index.html`。它没有外部网络请求，包含计数和文本控件。让官方 Dot 通过目标设备插件定位该浏览器窗口，观察后点击并核对。浏览器后台路径可能被驱动拒绝；保留拒绝证据，不能为了演示通过而自动抢焦点。

## 真机验收

另测试计算器计算、原生文本编辑器保存、中文、双屏不同缩放、移动窗口、锁屏、权限拒绝和物理接管。每项记下具体系统、版本、入口和效果；见 [验收记录模板](../docs/validation/ACCEPTANCE.md)。本地自动化故障测试覆盖的错误情形与真机验收分开记录。
