'use strict';
let csrf = '', state = null, editing = false, epoch = 0, statusRequest = 0;
let expired = false, busy = 0, pollTimer = null;
const requests = new Set();
const el = id => document.getElementById(id);
const controls = [...document.querySelectorAll('button, input')];
const messages = {
  driver_unavailable: '桌面驱动尚未连接。请检查权限或点击修复。',
  tunnel_runtime_key_missing: '请在本机输入运行密钥。',
  invalid_tunnel_id: '隧道编号应以 tunnel_ 开头。',
  paused: '已连接，桌面任务已暂停。',
  access_revoked: '访问已撤销；需在本机重新连接。',
  local_checks_not_ready: '驱动、活动检测或锁屏检查尚未通过，请先修复。',
  permissions_not_ready: '系统尚未确认 CuaDriver 的桌面权限；请授权后等待重新检查。',
  image_not_observed: '尚未收到插件的窗口图片调用，请先让 Dot 观察窗口。',
  signature_invalid: '驱动签名校验失败，安装已停止。请使用经过验证的发布包。',
  driver_apps_probe_failed: '驱动启动后的应用或窗口读取检查失败。仍失败时请下载脱敏诊断。',
  driver_apps_probe_timeout: '应用或窗口读取检查超时。仍超时时请下载脱敏诊断。',
  app_discovery_failed: '驱动未能读取应用列表。请重新检查；仍失败时下载脱敏诊断。',
  window_discovery_failed: '驱动未能读取窗口列表。请重新检查；仍失败时下载脱敏诊断。',
  driver_transport_failed: '驱动通信中断，正在尝试重新连接。',
  driver_empty_response: '驱动返回空响应。请重新检查；仍失败时下载脱敏诊断。',
  autostart_disabled: '随登录启动已被你关闭。请在系统设置的登录项中启用 DotControl 后重新打开向导。',
  autostart_ownership_mismatch: '启动服务与本次安装不一致。请使用安装包检查，避免启动另一份代理。',
  agent_unmanaged_instance: '当前代理未由本次安装的启动服务管理。请先完成安装迁移。'
};
const activityReasons = {
  permission_missing: '等待 DotControl 的输入监控权限',
  helper_unavailable: '活动辅助程序未运行，请重新检查',
  awaiting_sample: '等待活动样本',
  sample_stale: '活动样本已失效，请重新检查',
  invalid_sample: '活动样本无效，请重新检查',
  no_foreground_app: '暂时无法识别前台应用',
  sample_unavailable: '活动检测暂不可用，请重新检查'
};
const startupReasons = {
  not_configured: '尚未设置', not_registered: '启动项已写入，服务尚未注册',
  disabled_by_user: '已在系统登录项中关闭', ownership_mismatch: '服务路径不匹配',
  query_failed: '暂时无法核实启动服务', registered: '服务已注册'
};
function msg(text) { el('message').textContent = text; }
function updateControls() {
  for (const control of controls) control.disabled = expired || !state || busy > 0;
  el('confirm-image').disabled = expired || !state || busy > 0 || !state.plugin?.image_seen || state.plugin?.image_confirmed === true;
}
function clearStatus(label) {
  state = null;
  el('connection').textContent = label;
  rows('checks', [['运行状态', label, false]]);
  rows('validation', [['连接验证', label, false]]);
  el('image-delivery-state').textContent = label;
  el('image-delivery-state').className = 'pending';
  el('codex').textContent = label;
  el('permission-note').textContent = '';
  updateControls();
}
function expireSession() {
  if (expired) return;
  expired = true; epoch++; csrf = '';
  clearTimeout(pollTimer); pollTimer = null;
  for (const request of requests) request.abort();
  el('runtime-key').value = '';
  el('device-name').value = ''; el('tunnel-id').value = '';
  clearStatus('本地会话已失效');
  msg('请从 DotControl 菜单重新打开向导，恢复安全的本机会话。');
}
function staleResponse() { return Object.assign(new Error('过时响应'), {stale: true}); }
function report(error) { if (!expired && !error.stale) msg(error.message); }
async function api(path, data = {}) {
  if (expired) throw staleResponse();
  const requestEpoch = epoch, requestCSRF = csrf, controller = new AbortController();
  requests.add(controller);
  try {
    const response = await fetch('/onboard/api/' + path, {
      method: 'POST', headers: {'Content-Type': 'application/json', 'X-DotControl-CSRF': requestCSRF},
      body: JSON.stringify(data), signal: controller.signal
    });
    if (requestEpoch !== epoch) throw staleResponse();
    if (response.status === 401 || response.status === 403) { expireSession(); throw staleResponse(); }
    let value;
    try { value = await response.json(); } catch { throw new Error('本机响应暂不可用，请重新检查。'); }
    if (requestEpoch !== epoch) throw staleResponse();
    if (!response.ok) throw new Error(messages[value.error] || '操作失败，请下载脱敏诊断。');
    return value;
  } finally { requests.delete(controller); }
}
function rows(id, items) {
  const box = el(id); box.replaceChildren();
  for (const [label, value, ok] of items) {
    const dt = document.createElement('dt'), dd = document.createElement('dd');
    dt.textContent = label; dd.textContent = label === '桌面驱动' ? (messages[value] || value) : value;
    dd.className = ok ? 'ok' : 'pending'; box.append(dt, dd);
  }
}
function permissionText(s) {
  if (s.permissions?.state === 'granted') {
    if (s.plugin?.image_confirmed) return '已授权，图片显示已确认';
    if (s.plugin?.image_seen) return '已授权，已返回窗口图片';
    return '已授权，等待窗口图片验证';
  }
  return {not_required: '无需额外授权', required: '需要授权', signature_check_failed: '签名检查未通过', unknown: '尚未确认'}[s.permissions?.state] || '检查中';
}
function render(s) {
  state = s;
  for (const button of document.querySelectorAll('[data-action^=permissions]')) button.hidden = s.device.os !== 'darwin';
  el('connection').textContent = s.revoked ? '访问已撤销' : s.tunnel?.ready ? (s.device.paused ? '已连接 · 已暂停' : '已连接 · 已启用') : '等待连接';
  rows('checks', [
    ['设备', s.device.name, true],
    ['桌面驱动', s.driver_connected ? '已连接' : (s.driver_error || '等待驱动或系统权限'), s.driver_connected],
    ['系统权限', permissionText(s), ['granted', 'not_required'].includes(s.permissions?.state)],
    ['活动检测', s.device.activity.available ? '可用' : (activityReasons[s.activity_status?.reason] || '活动检测暂不可用，请重新检查'), s.device.activity.available],
    ['屏幕', s.device.activity.locked ? '已锁定' : '已解锁', !s.device.activity.locked],
    ['随登录启动', startupReasons[s.autostart_status?.reason] || (s.autostart ? '已设置' : '尚未设置'), s.autostart]
  ]);
  rows('validation', [
    ['隧道', s.tunnel?.ready ? '在线' : (s.tunnel?.error || '尚未连接'), s.tunnel?.ready],
    ['插件调用', s.plugin?.devices_seen ? '已收到 MCP 设备查询；请核对源 Dot' : '等待源 Dot 调用', s.plugin?.devices_seen],
    ['窗口图片', s.plugin?.image_seen ? 'MCP 已返回图片' : '等待源 Dot 观察窗口', s.plugin?.image_seen],
    ['桌面任务', s.device.paused ? '已暂停，等待本机启用' : '已启用', !s.device.paused]
  ]);
  el('image-delivery-state').textContent = s.plugin?.image_confirmed ? '你已确认源 Dot 显示图片' : '尚未确认源 Dot 实际显示';
  el('image-delivery-state').className = s.plugin?.image_confirmed ? 'ok' : 'pending';
  el('codex').textContent = s.codex?.reason || 'Codex 桌面委派暂不可用：尚未通过官方 Computer Use 真机验收。';
  el('permission-note').textContent = s.device.os === 'darwin' ? 'Mac：为 CuaDriver 开启辅助功能和屏幕录制；输入监控对应已安装的 DotControl 应用。授权后按系统要求退出重开，并重新检查。' : '';
  if (!editing) { el('device-name').value = s.device.name; el('tunnel-id').value = s.tunnel_id || ''; }
  updateControls();
}
async function refresh() {
  const sequence = ++statusRequest, requestEpoch = epoch;
  try {
    const result = await api('status');
    if (!expired && requestEpoch === epoch && sequence === statusRequest) { render(result); msg(''); }
  } catch (error) {
    if (!expired && !error.stale && requestEpoch === epoch && sequence === statusRequest) {
      clearStatus('连接中断，正在重新检查'); report(error);
    }
  }
}
function schedulePoll() {
  clearTimeout(pollTimer);
  if (expired) return;
  pollTimer = setTimeout(async () => { await refresh(); schedulePoll(); }, 4000);
}
async function withBusy(action) {
  if (expired || !state || busy) return;
  busy++; updateControls();
  try { await action(); } catch (error) { report(error); }
  finally { busy--; updateControls(); }
}
el('connect').addEventListener('input', () => { editing = true; });
el('connect').addEventListener('submit', event => {
  event.preventDefault();
  void withBusy(async () => {
    const key = el('runtime-key');
    try {
      await api('connect', {name: el('device-name').value, tunnel_id: el('tunnel-id').value, runtime_key: key.value});
      editing = false; await refresh();
    } finally { key.value = ''; }
  });
});
for (const button of document.querySelectorAll('[data-action]')) button.onclick = () => withBusy(async () => {
  const action = button.dataset.action;
  if (action === 'revoke' && !confirm('撤销此设备的远程访问并删除本机保存的隧道运行密钥？')) return;
  await api('action', {action}); await refresh();
});
el('copy-fields').onclick = () => withBusy(async () => {
  try { await navigator.clipboard.writeText('插件名称：' + el('device-name').value + '\n连接类型：Tunnel\n隧道编号：' + el('tunnel-id').value); msg('已复制。请在官方私有插件页面填写。'); }
  catch { msg('请手动复制上方名称和隧道编号。'); }
});
el('copy-test').onclick = () => withBusy(async () => {
  const text = '保持官方 Dot 连接当前源电脑，不切换到目标电脑。通过 ' + state.device.name + ' 的 DotControl 插件调用 devices，核对 device_id 为 ' + state.device.device_id + '，返回本次 nonce 和暂停状态。只验证连接，不执行桌面动作。启用后通过同一插件观察一个已打开的无害窗口，以 MCP 原始图片回传并确认我能看到；不能用文字报告代替截图。';
  try { await navigator.clipboard.writeText(text); msg('已复制连接测试指令。'); } catch { msg(text); }
});
el('diagnostics').onclick = () => withBusy(async () => {
  const value = await api('diagnostics'), url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], {type: 'application/json'})), link = document.createElement('a');
  link.href = url; link.download = 'DotControl-diagnostics.json'; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
});
el('check-update').onclick = () => withBusy(async () => {
  const value = await api('action', {action: 'check_update'});
  msg('当前版本：' + value.current + '；发布版本：' + value.published + '。请通过下载安装包链接查看说明，更新不会自动安装。');
});
updateControls();
(async () => {
  const fragment = location.hash.slice(1), ticket = /^[a-f0-9]{64}$/.test(fragment) ? fragment : '';
  history.replaceState(null, '', location.pathname);
  try { csrf = (await api('session', {ticket})).csrf; await refresh(); schedulePoll(); }
  catch (error) { report(error); }
})();
