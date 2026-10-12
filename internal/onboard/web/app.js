'use strict';
let csrf='',state=null,editing=false;
const el=id=>document.getElementById(id);
const messages={driver_unavailable:'桌面驱动尚未连接。请检查权限或点击修复。',tunnel_runtime_key_missing:'请在本机输入运行密钥。',invalid_tunnel_id:'隧道编号应以 tunnel_ 开头。',paused:'已连接，桌面任务已暂停。',access_revoked:'访问已撤销；需在本机重新连接。',local_checks_not_ready:'驱动、活动检测或锁屏检查尚未通过，请先修复。',permissions_not_ready:'系统尚未确认 CuaDriver 的桌面权限；请授权后等待重新检查。',image_not_observed:'尚未收到插件的窗口图片调用，请先让 Dot 观察窗口。',signature_invalid:'驱动签名校验失败，安装已停止。请使用经过验证的发布包。'};
Object.assign(messages,{
  driver_apps_probe_failed:'驱动启动后的应用或窗口读取检查失败。请查看系统权限并重新检查；仍失败时下载脱敏诊断。',
  driver_apps_probe_timeout:'应用或窗口读取检查超时。请重新检查；仍超时时下载脱敏诊断。',
  app_discovery_failed:'驱动未能读取应用列表。请查看系统权限并重新检查；仍失败时下载脱敏诊断。',
  window_discovery_failed:'驱动未能读取窗口列表。请查看系统权限并重新检查；仍失败时下载脱敏诊断。',
  driver_transport_failed:'驱动通信中断，正在尝试重新连接。',
  driver_empty_response:'驱动返回空响应。请重新检查；仍失败时下载脱敏诊断。'
});
async function api(path,data={}){const r=await fetch('/onboard/api/'+path,{method:'POST',headers:{'Content-Type':'application/json','X-DotControl-CSRF':csrf},body:JSON.stringify(data)});let v;try{v=await r.json()}catch{v={error:r.status===401?'请从 DotControl 重新打开向导。':'本地会话失效，请重新打开向导。'}}if(!r.ok)throw Error(messages[v.error]||v.error||'操作失败');return v}
function msg(s){el('message').textContent=s}
function rows(id,items){const box=el(id);box.replaceChildren();for(const [label,value,ok] of items){const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=label;dd.textContent=label==='桌面驱动'?(messages[value]||value):value;dd.className=ok?'ok':'pending';box.append(dt,dd)}}
function renderState(s){
 const first=!state;state=s;const terminal=!!s.terminal_only;if(first&&s.tunnel?.ready)el('account-settings').open=false;
 for(const button of document.querySelectorAll('[data-action^=permissions]'))button.hidden=terminal||s.device.os!=='darwin';
 for(const button of document.querySelectorAll('[data-action]')){if(button.dataset.action==='confirm_image')button.hidden=terminal;if(button.dataset.action==='resume')button.textContent=s.history_enabled?'恢复已启用功能':terminal?'恢复终端任务':'启用桌面任务'}
 el('connection').textContent=s.revoked?'访问已撤销':s.tunnel?.ready?(s.device.paused?'已连接 · 已暂停':'已连接 · 已启用'):'等待连接';
 const desktopChecks=terminal?[['桌面组件','本模式不需要',true]]:[['桌面驱动',s.driver_connected?'已连接':(s.driver_error||'等待驱动或系统权限'),s.driver_connected],['系统权限',({granted:'已授权（仍需验证截图）',not_required:'无需额外授权',required:'需要授权',unknown:'尚未确认'}[s.permissions?.state]||'检查中'),['granted','not_required'].includes(s.permissions?.state)],['活动检测',s.device.activity.available?'可用':'等待输入监控权限',s.device.activity.available],['屏幕',s.device.activity.locked?'已锁定':'已解锁',!s.device.activity.locked]];
 rows('checks',[['设备',s.device.name,true],...desktopChecks,['随登录启动',s.autostart?'已设置':'尚未设置',s.autostart]]);
 const cap=s.codex?.capabilities?.codex_terminal;
 const terminalStatus=cap?.available?'已连接本机 Codex':({terminal_disabled:'等待点击连接 Codex',device_paused:'已暂停',runtime_acceptance_required:'此系统尚未开放',terminal_configuration_changed:'配置已变更，请重新连接'}[cap?.code]||cap?.code||'等待本机配置');
 const validation=terminal?[['本机 Codex',terminalStatus,!!cap?.available],['源端工具目录','尚未验证；请在源 Dot 核对实际工具列表',false],['任务状态',s.device.paused?'已暂停':'已启用',!s.device.paused]]:[['窗口图片',s.plugin?.image_seen?'MCP 已返回图片':'等待源 Dot 观察窗口',s.plugin?.image_seen],['图片交付',s.plugin?.image_confirmed?'你已确认源 Dot 显示图片':'尚未确认源 Dot 实际显示',s.plugin?.image_confirmed],['桌面任务',s.device.paused?'已暂停，等待本机启用':'已启用',!s.device.paused]];
 rows('validation',[['隧道',s.tunnel?.ready?'在线':(s.tunnel?.error||'尚未连接'),s.tunnel?.ready],['插件调用',s.plugin?.devices_seen?'已收到 MCP 设备查询；请核对源 Dot':'等待源 Dot 调用',s.plugin?.devices_seen],...validation]);
 el('codex').textContent=terminal?(cap?.available?'本机 Codex 已就绪；源端工具目录尚未验证。':terminalStatus):(s.codex?.reason||'请连接本机 Codex。');
 el('permission-note').textContent=terminal?'仅终端模式不要求桌面权限。':s.device.os==='darwin'?'桌面功能需要辅助功能、屏幕录制和输入监控权限。':'';
 el('verify-note').textContent=terminal?'本机就绪不代表源端已加载终端工具。升级后，请在源端官方插件设置点击 Refresh tools，再新建会话并复制测试指令核对。SSH 等工作之后在 Windows 中处理。':'启用后再观察一个已打开的无害窗口，确认源 Dot 实际显示了图片。';
 if(!editing){el('device-name').value=s.device.name;el('tunnel-id').value=s.tunnel_id||''}
}
async function refresh(){renderState(await api('status'))}
el('connect').addEventListener('input',()=>editing=true);el('connect').addEventListener('submit',async e=>{e.preventDefault();const key=el('runtime-key');try{await api('connect',{name:el('device-name').value,tunnel_id:el('tunnel-id').value,runtime_key:key.value});editing=false;msg('连接设置已保存到本机，正在检查隧道。');await refresh()}catch(e){msg(e.message)}finally{key.value=''}});
for(const button of document.querySelectorAll('[data-action]'))button.onclick=async()=>{const action=button.dataset.action;if(action==='revoke'&&!confirm('撤销此设备的远程访问并删除本机保存的隧道运行密钥？'))return;button.disabled=true;try{await api('action',{action});msg(action==='resume'?(state?.terminal_only?'已恢复终端任务。':'已启用桌面任务。请使用新的观察继续。'):'已处理。');await refresh()}catch(e){msg(e.message)}finally{button.disabled=false}};
el('copy-fields').onclick=async()=>{try{await navigator.clipboard.writeText('插件名称：'+el('device-name').value+'\n连接类型：Tunnel\n隧道编号：'+el('tunnel-id').value);msg('已复制。请在官方私有插件页面填写。')}catch{msg('请手动复制上方名称和隧道编号。')}};
el('copy-test').onclick=async()=>{if(!state)return;const text=state.terminal_only?'先检查当前源端会话实际可用的 '+state.device.name+' 插件工具列表，确认包含 codex_submit、codex_job、codex_continue、codex_cancel；缺少任一工具就停止测试，在源端官方插件设置点击 Refresh tools，再新建会话并重新检查。不要根据 devices 查询成功或 codex_terminal.available 推断工具已加载。若列出 codex_review，也仅表示工具存在，不表示远程审批已验收。工具齐全后，保持官方 Dot 连接 Mac，不切换到 Windows。通过同一插件调用 devices，核对 device_id 为 '+state.device.device_id+'，检查 capabilities.codex_terminal 和 codex_profiles.execution；仅在终端能力可用且配置存在时，选择已配置的终端任务，使用新的 request_id 调用 codex_submit，要求只报告本机系统信息，以 codex_job 查询实际结果。不连接 SSH 或部署实验；不要把 apps、observe 或桌面工具作为前置条件，也不要把终端命令放入 execute。':'保持官方 Dot 连接当前源电脑，不切换到目标电脑。通过 '+state.device.name+' 的 DotControl 插件调用 devices，核对 device_id 为 '+state.device.device_id+'，返回本次 nonce 和暂停状态。只验证连接，不执行桌面动作。启用后通过同一插件观察一个已打开的无害窗口，以 MCP 原始图片回传并确认我能看到；不能用文字报告代替截图。';try{await navigator.clipboard.writeText(text);msg('已复制连接测试指令。')}catch{msg(text)}};
el('diagnostics').onclick=async()=>{try{const v=await api('diagnostics'),url=URL.createObjectURL(new Blob([JSON.stringify(v,null,2)],{type:'application/json'})),a=document.createElement('a');a.href=url;a.download='DotControl-diagnostics.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}catch(e){msg(e.message)}};
(async()=>{const fragment=location.hash.slice(1),ticket=/^[a-f0-9]{64}$/.test(fragment)?fragment:'';history.replaceState(null,'',location.pathname);try{csrf=(await api('session',{ticket})).csrf;await refresh();setInterval(()=>refresh().catch(e=>msg(e.message)),4000)}catch(e){msg(e.message)}})();

el('check-update').onclick=async()=>{try{const v=await api('action',{action:'check_update'});msg('当前版本：'+v.current+'；发布版本：'+v.published+'。请通过下载安装包链接查看说明，更新不会自动安装。')}catch{msg('发布查询失败。可使用旁边的下载安装包链接手动查看。')}};
