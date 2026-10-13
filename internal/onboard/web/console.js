'use strict';
let consoleView='overview';
const initializedViews=new Set();
const viewNames={overview:'概览',connection:'连接设置',codex:'Codex',sharing:'会话共享'};
function showView(name,remember=true){
 consoleView=Object.hasOwn(viewNames,name)?name:'overview';
 for(const panel of document.querySelectorAll('[data-panel]'))panel.hidden=panel.dataset.panel!==consoleView;
 for(const link of document.querySelectorAll('[data-view]'))link.setAttribute('aria-current',link.dataset.view===consoleView?'page':'false');
 if(remember)history.replaceState(null,'',location.pathname+'#'+consoleView);
 if(!initializedViews.has(consoleView)){
  const load=consoleView==='codex'?()=>loadConnections():consoleView==='sharing'?()=>historyStatus():null;
  if(load){initializedViews.add(consoleView);load().catch(e=>{initializedViews.delete(consoleView);msg(e.message)})}
 }
}
for(const link of document.querySelectorAll('[data-view]'))link.addEventListener('click',e=>{e.preventDefault();showView(link.dataset.view)});
addEventListener('hashchange',()=>showView(location.hash.slice(1),false));
function statusCard(id,text,kind){el(id).textContent=text;el(id).dataset.tone=kind}
function renderConsole(s){
 const device=s.device,online=cloudOnline(s.tunnel),terminal=s.codex?.capabilities?.codex_terminal;
 el('device-title').textContent=device.name;
 el('device-version').textContent='v'+device.version+' · '+({windows:'Windows',darwin:'macOS'}[device.os]||device.os||'本机');
 el('device-state').textContent=s.revoked?'访问已撤销':device.paused?'已暂停':'正在运行';
 el('device-state').dataset.tone=s.revoked?'danger':device.paused?'pending':'ok';
 statusCard('status-local','服务就绪','ok');
 statusCard('status-cloud',online?'已连接':s.tunnel?.configured||s.tunnel_id?'连接未确认':'尚未配置',online?'ok':'pending');
 el('cloud-detail').textContent=cloudLabel(s.tunnel);
 statusCard('status-source',s.plugin?.devices_seen?'已收到调用':'等待首次调用',s.plugin?.devices_seen?'ok':'pending');
 statusCard('status-codex',s.revoked?'访问已撤销':device.paused?'已暂停':terminal?.available?'可以接收任务':s.terminal_enabled?'需要检查':'尚未启用',s.revoked?'danger':!device.paused&&terminal?.available?'ok':'pending');
 el('overview-pause').hidden=!!(device.paused||s.revoked);
 el('overview-resume').hidden=!!(!device.paused||s.revoked);
 const setup=el('setup-next');setup.hidden=!!(s.tunnel_id&&s.terminal_enabled);
 setup.textContent=!s.tunnel_id?'继续设置：连接账号与插件':'继续设置：连接本机 Codex';
 setup.onclick=()=>showView(!s.tunnel_id?'connection':'codex');el('setup-note').hidden=setup.hidden;
 el('connect-basic').textContent=s.terminal_enabled?'重新配置基础终端':'连接并启用 Codex';
 el('device-description').textContent=s.revoked?'远程访问已关闭。请在连接设置中重新配置。':device.paused?'远程任务与会话读取已暂停，可在本机恢复。':'关闭此页面后，DotControl 会继续运行。';
 if(!initializedViews.has('navigation')){initializedViews.add('navigation');showView(location.hash.slice(1)||'overview',false)}
}

// Keep feedback beside the action without moving focus or scrolling on refresh.
if(document.addEventListener)document.addEventListener('click',event=>{
 const button=event.target.closest?.('button'),card=button?.closest('.card'),panel=button?.closest('[data-panel]');
 if(card&&panel){const output=el(panel.dataset.panel+'-message');if(output&&output.parentElement!==card)card.append(output)}
});
