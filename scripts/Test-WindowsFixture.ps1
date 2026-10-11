param(
    [string]$Binary = '.\dist\dotcontrol.exe',
    [string]$State = '.state/windows-validation',
    [string]$OutputFile = '.state/fixture-output.txt'
)
$ErrorActionPreference = 'Stop'
function Invoke-Probe([string]$Method, $Request) {
    $json = $Request | ConvertTo-Json -Depth 20 -Compress
    $result = $json | & $Binary --state $State probe $Method
    if ($LASTEXITCODE -ne 0) { throw "Probe failed: $Method" }
    return ($result | ConvertFrom-Json)
}
$device = (& $Binary --state $State probe devices | ConvertFrom-Json)[0]
$apps = Invoke-Probe apps @{device_id=$device.device_id}
$windows = @($apps.windows.structuredContent.windows | Where-Object {$_.title -eq 'DotControl acceptance fixture'})
if ($windows.Count -ne 1) { throw 'Start exactly one disposable native fixture first.' }
$target = @{pid=$windows[0].pid;window_id=[string]$windows[0].window_id}
$task = 'fixture-' + [guid]::NewGuid().ToString('N')
function Observe-Fixture { return Invoke-Probe observe @{device_id=$device.device_id;target=$target} }
function Act-Fixture($Observation, [string]$Kind, $Arguments, [string]$Expected) {
    $script:lastRequest = @{device_id=$device.device_id;action=@{
        device_id=$device.device_id;task_id=$task;action_id=[guid]::NewGuid().ToString('N');
        observation_id=$Observation.observation_id;target=$target;kind=$Kind;args=$Arguments;mode='background';expect=$Expected
    }}
    return Invoke-Probe execute $script:lastRequest
}
function Find-Element($Observation,[string]$Label) {
    $elements = @((ConvertFrom-Json $Observation.view.text).elements | Where-Object {$_.label -eq $Label})
    if ($elements.Count -ne 1) { throw "Fixture element is missing or ambiguous: $Label" }
    return $elements[0].element_token
}
$before = & $Binary --state $State status | ConvertFrom-Json
$observation = Observe-Fixture
if ($observation.view.images.Count -lt 1) { throw 'No screenshot returned.' }
$text = 'DotControl 中文输入验证 你好，世界！'
$write = Act-Fixture $observation 'set_value' @{element_token=(Find-Element $observation 'DotControl test input');value=$text} $text
if ($write.state -ne 'succeeded') { throw "Text write was not verified: $($write | ConvertTo-Json -Compress)" }
$observation = Observe-Fixture
$save = Act-Fixture $observation 'click' @{element_token=(Find-Element $observation 'Save test text')} 'Saved test text'
if ($save.state -ne 'succeeded') { throw "Save was not verified: $($save | ConvertTo-Json -Compress)" }
if ([IO.File]::ReadAllText((Join-Path (Get-Location) $OutputFile)) -cne $text) { throw 'Saved file bytes do not match Chinese input.' }
$observation = Observe-Fixture
$increment = Act-Fixture $observation 'click' @{element_token=(Find-Element $observation 'Increment test counter')} 'Count: 1'
if ($increment.state -ne 'succeeded') { throw "Counter was not verified: $($increment | ConvertTo-Json -Compress)" }
$retry = Invoke-Probe execute $script:lastRequest
if ($retry.action_id -ne $increment.action_id -or $retry.state -ne 'succeeded') { throw 'Retry did not return original receipt.' }
$afterObservation = Observe-Fixture
if ($afterObservation.view.text -notmatch 'Count: 1' -or $afterObservation.view.text -match 'Count: 2') { throw 'Duplicate request repeated its side effect.' }
$after = & $Binary --state $State status | ConvertFrom-Json
$report = [ordered]@{
    tested_at=[DateTime]::UtcNow.ToString('o');source='local authenticated API (not official Dot)';
    screenshot=$true;chinese_write=$true;saved_file_verified=$true;duplicate_effect_count=1;
    foreground_before=$before.device.activity.foreground_window;foreground_after=$after.device.activity.foreground_window;
    focus_unchanged=($before.device.activity.foreground_window -eq $after.device.activity.foreground_window);
    task_id=$task;receipts=@($write,$save,$increment,$retry)
}
$report | ConvertTo-Json -Depth 8
