'use strict';
let csrf='',state=null,editing=false,networkEditing=false,sessionBlocked=false,sessionRecovery=null;
const el=id=>document.getElementById(id);
const messages={driver_unavailable:'桌面驱动尚未连接。请检查权限或点击修复。',tunnel_runtime_key_missing:'请在本机输入运行密钥。',invalid_tunnel_id:'隧道编号应以 tunnel_ 开头。',paused:'已连接，桌面任务已暂停。',access_revoked:'访问已撤销；需在本机重新连接。',local_checks_not_ready:'驱动、活动检测或锁屏检查尚未通过，请先修复。',permissions_not_ready:'系统尚未确认 CuaDriver 的桌面权限；请授权后等待重新检查。',image_not_observed:'尚未收到插件的窗口图片调用，请先让 Dot 观察窗口。',signature_invalid:'驱动签名校验失败，安装已停止。请使用经过验证的发布包。'};
Object.assign(messages,{
 tunnel_network_error:'无法联系云端服务，请检查当前网络或代理。',
 tunnel_network_failed:'云端轮询失败；现有信息不足以确定是 DNS、TLS 还是其他网络问题。',
 tunnel_cloud_evidence_stale:'连接未确认：最近成功轮询已过期。',
 tunnel_health_unavailable:'无法读取本地隧道健康状态，云端连接未确认。',
 tunnel_health_invalid:'隧道健康信息不完整，云端连接未确认。',
 tunnel_http_failed:'云端请求被拒绝，请下载脱敏诊断。',
 tunnel_network_timeout:'联系云端服务超时，正在重试。',
 tunnel_credentials_rejected:'运行密钥被拒绝，请在本机更新密钥。',
 tunnel_permission_denied:'运行密钥没有此隧道的权限。',
 tunnel_tls_failed:'TLS 验证失败，请检查本机网络和证书。',
 tunnel_dns_failed:'域名解析失败，请检查本机网络。',
 invalid_network_mode:'请选择自动、直连或手动代理。',
 invalid_proxy_url:'请输入有效的 HTTP 或 HTTPS 代理地址。',
 requires_local_proxy_auth:'不要在地址中输入密码。请使用无需交互认证的本机 HTTP 代理。',
 manual_proxy_required:'请输入手动代理地址。',
 proxy_requires_manual_mode:'自动和直连模式不保存手动代理地址。',
 proxy_scheme_unsupported:'本版只支持 HTTP / HTTPS 静态代理。',
 proxy_pac_unsupported:'检测到 PAC；请在本机选择可用的 HTTP 静态代理。',
 proxy_socks_unsupported:'检测到 SOCKS；请在本机选择可用的 HTTP 静态代理。',
 system_proxy_unavailable:'无法读取当前用户的系统代理设置。',
 proxy_environment_missing:'指定的代理环境变量不存在。',
 network_unavailable:'当前程序不支持网络设置，请确认已升级。',
  driver_apps_probe_failed:'驱动启动后的应用或窗口读取检查失败。请查看系统权限并重新检查；仍失败时下载脱敏诊断。',
  driver_apps_probe_timeout:'应用或窗口读取检查超时。请重新检查；仍超时时下载脱敏诊断。',
  app_discovery_failed:'驱动未能读取应用列表。请查看系统权限并重新检查；仍失败时下载脱敏诊断。',
  window_discovery_failed:'驱动未能读取窗口列表。请查看系统权限并重新检查；仍失败时下载脱敏诊断。',
  driver_transport_failed:'驱动通信中断，正在尝试重新连接。',
  driver_empty_response:'驱动返回空响应。请重新检查；仍失败时下载脱敏诊断。'
});
Object.assign(messages,{
 open_from_dotcontrol:'当前浏览器尚未连接向导。请从 DotControl 在此浏览器重新打开一次；已保存配置不受影响。',
 session_rejected:'向导访问已过期或代理已重启。请从 DotControl 重新打开一次；已保存配置不受影响。',
 launch_expired_open_dotcontrol:'打开链接已过期或当前浏览器没有有效会话。请从 DotControl 重新打开一次。',
 history_preview_expired:'清单预览已过期，请重新生成并核对；当前共享范围未改变。',
 history_revision_conflict:'共享范围已在其他窗口更新，请重新生成预览后确认。',
 history_access_changed:'共享范围已改变，请重新查找会话；候选选择仍保留。',
 history_codex_unavailable:'尚未连接本机 Codex，请先在 Codex 区域检查连接。',
 history_codex_version_unsupported:'本机 Codex 版本与历史读取接口不兼容，请查看诊断。',
 history_local_interface_unavailable:'本机 Codex 的会话接口暂不可用，请检查连接后重试。',
 device_revoked:'此设备的远程访问已撤销。',
 device_paused:'远程访问已暂停，请在概览中恢复后重试。'
});
async function requestAPI(path,data){
 const r=await fetch('/onboard/api/'+path,{method:'POST',headers:{'Content-Type':'application/json','X-DotControl-CSRF':csrf},body:JSON.stringify(data)});
 let v;try{v=await r.json()}catch{v={error:'request_rejected'}}
 if(!r.ok){const e=Error(messages[v.error]||v.error||'操作失败');e.auth=(r.status===401&&v.error==='open_from_dotcontrol')||(r.status===403&&v.error==='session_rejected');throw e}return v
}
async function recoverSession(){
 if(!sessionRecovery)sessionRecovery=requestAPI('session',{}).then(v=>{if(!v.csrf)throw Error('无效的向导会话');csrf=v.csrf;sessionBlocked=false}).catch(e=>{sessionBlocked=true;throw e}).finally(()=>{sessionRecovery=null});
 return sessionRecovery
}
async function api(path,data={}){
 try{return await requestAPI(path,data)}catch(e){
  // Only explicit authentication rejections happen before backend dispatch.
  // Never replay a request after a network failure or a business error.
  if(path==='session'||!e.auth)throw e;
  await recoverSession();
  try{return await requestAPI(path,data)}catch(retryError){if(retryError.auth)sessionBlocked=true;throw retryError}
 }
}
function msg(s){
 const target=typeof consoleView==='string'&&!sessionBlocked?consoleView+'-message':'message';
 feedback(el(target)||el('message'),s);
}
function feedback(box,s){
 box.textContent=s;
 if(/^[a-z][a-z0-9_]+$/.test(s)){box.textContent='操作未完成，请检查本机配置。';const details=document.createElement('details'),summary=document.createElement('summary'),code=document.createElement('code');summary.textContent='技术详情';code.textContent=s;details.append(summary,code);box.append(details)}
}
function rows(id,items){const box=el(id);box.replaceChildren();for(const [label,value,ok] of items){const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=label;dd.textContent=label==='桌面驱动'?(messages[value]||value):value;dd.className=ok?'ok':'pending';box.append(dt,dd)}}
function cloudOnline(t){return !!(t?.ready&&['online','backpressured'].includes(t.cloud_state)&&t.last_successful_poll_at)}
function cloudLabel(t){
 if(cloudOnline(t))return t.cloud_state==='backpressured'?'已连接，正在处理积压请求':'已连接（已有成功轮询）';
 if(t?.error)return messages[t.error]||'连接尚未成功：'+t.error;
 return ({unconfirmed:'连接未确认',busy:'正在处理请求，连接状态单独核实',backpressured:'正在处理积压请求',offline:'云端未连接',stopped:'隧道未运行',unavailable:'连接未确认',degraded:'云端连接异常',proxy_blocked:'代理设置需要本机处理',connecting:'正在连接云端',starting:'正在连接云端'}[t?.cloud_state]||'等待云端连接');
}
function renderState(s){
 const first=!state;state=s;const terminal=!!s.terminal_only,online=cloudOnline(s.tunnel);if(first&&online)el('account-settings').open=false;
 for(const button of document.querySelectorAll('[data-action^=permissions]'))button.hidden=terminal||s.device.os!=='darwin';
 for(const button of document.querySelectorAll('[data-action]')){if(button.dataset.action==='confirm_image')button.hidden=terminal;if(button.dataset.action==='resume')button.textContent='恢复访问'}
 el('connection').textContent=s.revoked?'访问已撤销':online?(s.device.paused?'云端已连接 · 已暂停':'云端已连接 · 已启用'):'本地服务就绪 · 云端未确认';
 const desktopChecks=terminal?[['桌面组件','本模式不需要',true]]:[['桌面驱动',s.driver_connected?'已连接':(s.driver_error||'等待驱动或系统权限'),s.driver_connected],['系统权限',({granted:'已授权（仍需验证截图）',not_required:'无需额外授权',required:'需要授权',unknown:'尚未确认'}[s.permissions?.state]||'检查中'),['granted','not_required'].includes(s.permissions?.state)],['活动检测',s.device.activity.available?'可用':'等待输入监控权限',s.device.activity.available],['屏幕',s.device.activity.locked?'已锁定':'已解锁',!s.device.activity.locked]];
 rows('checks',[['设备',s.device.name,true],...desktopChecks,['随登录启动',s.background?.configured?'已设置':s.background?'已关闭':s.autostart?'已设置（旧入口）':'尚未设置',s.background?.configured??s.autostart],['后台管理',typeof backgroundLabel==='function'?backgroundLabel(s):'待确认',!!s.background?.managed&&!s.background?.reason],...(s.background?.reason?[['后台诊断代码',s.background.reason,false]]:[])]);
 const cap=s.codex?.capabilities?.codex_terminal;
 const terminalStatus=cap?.available?'已连接本机 Codex':({terminal_disabled:'等待点击连接 Codex',device_paused:'已暂停',runtime_acceptance_required:'此系统尚未开放',terminal_configuration_changed:'配置已变更，请重新连接'}[cap?.code]||(cap?.code?'需要在本机检查配置':'等待本机配置'));
 const validation=terminal?[['本机 Codex',terminalStatus,!!cap?.available],['源端工具目录','尚未验证；请在源 Dot 核对实际工具列表',false],...(cap?.code?[['Codex 诊断代码',cap.code,false]]:[]),['任务状态',s.device.paused?'已暂停':'已启用',!s.device.paused]]:[['窗口图片',s.plugin?.image_seen?'MCP 已返回图片':'等待源 Dot 观察窗口',s.plugin?.image_seen],['图片交付',s.plugin?.image_confirmed?'你已确认源 Dot 显示图片':'尚未确认源 Dot 实际显示',s.plugin?.image_confirmed],['桌面任务',s.device.paused?'已暂停，等待本机启用':'已启用',!s.device.paused]];
 rows('validation',[['本地服务','就绪',true],['云端隧道',cloudLabel(s.tunnel),online],['源 Dot 调用',s.plugin?.devices_seen?'已收到 MCP 设备查询；请在源 Dot 核对本次结果':'等待源 Dot 调用',!!s.plugin?.devices_seen],...validation]);
 if(!networkEditing){el('network-mode').value=s.network?.mode||'auto';el('network-proxy').value=s.network?.proxy_url||'';el('network-proxy-row').hidden=el('network-mode').value!=='manual'}
 const source=({system:'系统代理',environment:'进程代理',manual:'手动代理',direct:'直连',none:'直连',bypass:'按绕过规则直连'}[s.tunnel?.proxy_source]||s.tunnel?.proxy_source||'尚未确认');
 el('network-summary').textContent='当前连接方式：'+source+'；'+cloudLabel(s.tunnel)+(s.tunnel?.network_change_pending?'；新设置等待隧道下次启动':'')+(s.tunnel?.network_change_error?'；新设置需要处理：'+(messages[s.tunnel.network_change_error]||s.tunnel.network_change_error):'');
 el('codex').textContent=terminal?(cap?.available?'本机 Codex 已就绪；源端工具目录尚未验证。':terminalStatus):(s.codex?.reason||'请连接本机 Codex。');
 el('permission-note').textContent=terminal?'仅终端模式不要求桌面权限。':s.device.os==='darwin'?'桌面功能需要辅助功能、屏幕录制和输入监控权限。':'';
 el('verify-note').textContent=terminal?'本机就绪不代表源端已加载终端工具。升级后，请在源端官方插件设置点击 Refresh tools，再新建会话并复制测试指令核对。':'启用后再观察一个已打开的无害窗口，确认源 Dot 实际显示了图片。';
 if(!editing){el('device-name').value=s.device.name;el('tunnel-id').value=s.tunnel_id||''}
 if(typeof renderConsole==='function')renderConsole(s);
}
let refreshPending=null;
async function refresh(){if(!refreshPending)refreshPending=api('status').then(renderState).finally(()=>{refreshPending=null});return refreshPending}
el('network-form').addEventListener('input',()=>{networkEditing=true;el('network-proxy-row').hidden=el('network-mode').value!=='manual'});
el('network-form').addEventListener('submit',async e=>{e.preventDefault();const button=el('network-save');button.disabled=true;try{const mode=el('network-mode').value;await api('network',{mode,proxy_url:mode==='manual'?el('network-proxy').value.trim():''});networkEditing=false;msg('网络设置已保存，将在隧道下次启动时应用；当前连接继续运行，已提交任务不会重放。');await refresh()}catch(e){msg(e.message)}finally{button.disabled=false}});
el('connect').addEventListener('input',()=>editing=true);el('connect').addEventListener('submit',async e=>{e.preventDefault();const key=el('runtime-key');try{await api('connect',{name:el('device-name').value,tunnel_id:el('tunnel-id').value,runtime_key:key.value});editing=false;msg('连接设置已保存到本机，正在检查隧道。');await refresh()}catch(e){msg(e.message)}finally{key.value=''}});
for(const button of document.querySelectorAll('[data-action]'))button.onclick=async()=>{const action=button.dataset.action;if(action==='revoke'&&!confirm('撤销此设备的远程访问并删除本机保存的隧道运行密钥？'))return;button.disabled=true;try{await api('action',{action});msg(action==='resume'?'已恢复访问。':action==='pause'?'已暂停远程任务与会话读取。':'已处理。');await refresh()}catch(e){msg(e.message)}finally{button.disabled=false}};
el('copy-fields').onclick=async()=>{try{await navigator.clipboard.writeText('插件名称：'+el('device-name').value+'\n连接类型：Tunnel\n隧道编号：'+el('tunnel-id').value);msg('已复制。请在官方私有插件页面填写。')}catch{msg('请手动复制上方名称和隧道编号。')}};
el('copy-test').onclick=async()=>{if(!state)return;const text=state.terminal_only?'先检查当前源端会话实际可用的 '+state.device.name+' 插件工具列表，确认包含 codex_submit、codex_job、codex_continue、codex_cancel；缺少任一工具就停止测试，在源端官方插件设置点击 Refresh tools，再新建会话并重新检查。不要根据 devices 查询成功或 codex_terminal.available 推断工具已加载。升级后的插件应为 13 个工具。工具齐全后，保持官方 Dot 连接当前源电脑，不切换到目标电脑。通过同一插件调用 devices，核对 device_id 为 '+state.device.device_id+'，检查 capabilities.codex_terminal 和 codex_profiles.execution；仅在终端能力可用且配置存在时，选择已配置的终端任务，使用新的 request_id 调用 codex_submit，要求只报告本机系统信息，以 codex_job 查询实际结果。不连接 SSH 或部署实验；不要把 apps、observe 或桌面工具作为前置条件，也不要把终端命令放入 execute。':'保持官方 Dot 连接当前源电脑，不切换到目标电脑。通过 '+state.device.name+' 的 DotControl 插件调用 devices，核对 device_id 为 '+state.device.device_id+'，返回本次 nonce 和暂停状态。只验证连接，不执行桌面动作。启用后通过同一插件观察一个已打开的无害窗口，以 MCP 原始图片回传并确认我能看到；不能用文字报告代替截图。';try{await navigator.clipboard.writeText(text);msg('已复制连接测试指令。')}catch{msg(text)}};
el('diagnostics').onclick=async()=>{try{const v=await api('diagnostics'),url=URL.createObjectURL(new Blob([JSON.stringify(v,null,2)],{type:'application/json'})),a=document.createElement('a');a.href=url;a.download='DotControl-diagnostics.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}catch(e){msg(e.message)}};
(async()=>{const fragment=location.hash.slice(1),ticket=/^[a-f0-9]{64}$/.test(fragment)?fragment:'';if(ticket)history.replaceState(null,'',location.pathname);try{csrf=(await api('session',{ticket})).csrf;await refresh();setInterval(()=>{if(!sessionBlocked)refresh().catch(e=>msg(e.message))},4000)}catch(e){sessionBlocked=true;msg(e.message)}})();

el('check-update').onclick=async()=>{try{const v=await api('action',{action:'check_update'});msg('当前版本：'+v.current+'；发布版本：'+v.published+'。请通过下载安装包链接查看说明，更新不会自动安装。')}catch{msg('发布查询失败。可使用旁边的下载安装包链接手动查看。')}};
