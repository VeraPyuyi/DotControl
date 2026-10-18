'use strict';
Object.assign(messages,{
 invalid_project_dir:'请选择这台电脑上的完整项目目录。',
 invalid_permission_profile:'请先读取并选择已有的 Codex 权限配置。',
 feature_retired:'此功能已下线。请重新打开控制台，使用基础终端任务配置。',
 terminal_mode_pause_required:'请先在概览暂停访问，再更改执行模式。',
 terminal_runtime_busy:'还有未停止或状态未确认的任务，请先查询并取消控制任务。',
 terminal_not_enabled:'请先保存任务配置，再验证并启用。',
 terminal_needs_local_setup:'旧配置需要在目标电脑重新设置。请保存基础终端配置并验证权限。',
 permission_profile_unavailable:'所选权限配置不可用，请重新读取并选择。',
 terminal_configuration_changed:'配置已变更，请重新检查。',
 terminal_profile_requires_manual_selection:'已有自定义配置。请展开高级配置，选中原配置并检查启用，避免覆盖。'
});
let connectionConfig={execution_profiles:[]};
const connectionMessages={
 terminal_ready:'所选权限下的本机执行检查通过。',
 terminal_enabled:'可以接收任务。请在源 Dot 核对新版插件工具。',
 terminal_enabled_not_ready:'启用选择已保存，但当前能力不可用，请检查概览中的状态。',
 terminal_mode_selected:'已选择仅终端模式，仍保持暂停。请配置并验证本机 Codex。',
 terminal_permission_denied:'所选权限无法访问任务所需目录，请在本机调整配置。',
 terminal_sandbox_layout_unsupported:'当前 Codex 不支持所选目录的权限规则，请调整本机配置。',
 terminal_probe_failed:'本机执行检查未通过，没有扩大权限。',
 terminal_probe_unconfirmed:'未收到完整检查回执，请检查本机配置后重试。'
};
async function connectionsCall(operation,data={}){return api('connections',{operation,...data})}
function selection(id,profiles){const select=el(id),previous=select.value;select.replaceChildren(new Option('新建配置',''));for(const p of profiles)select.add(new Option(p.name,p.id));select.value=profiles.some(p=>p.id===previous)?previous:''}
async function loadConnections(){
 connectionConfig=await connectionsCall('list');
 selection('execution-id',connectionConfig.execution_profiles||[]);
 el('codex-executable').value=connectionConfig.codex_executable||'';
}
function fillExecution(){
 const p=(connectionConfig.execution_profiles||[]).find(p=>p.id===el('execution-id').value)||{};
 el('execution-name').value=p.name||'';el('project-dir').value=p.project_dir||'';el('presentation').value=p.presentation||'visible';
 const select=el('permission-profile');select.replaceChildren(new Option('请读取此项目的权限配置',''));
 if(p.permission_profile)select.add(new Option(p.permission_profile+' · 已保存，执行前会核对',p.permission_profile));
 select.value=p.permission_profile||'';
 const unsupported=!!p.id&&(p.execution!=='terminal'||p.approval_policy!=='never');
 el('execution-warning').hidden=!unsupported;
 el('execution-warning').textContent=unsupported?'此旧配置不能远程继续执行。选择项目与权限后，点击“保存终端配置”才会切换为基础终端任务；不会自动转换。':'';
}
function bindConnectionButton(id,fn){el(id).onclick=async()=>{const button=el(id);button.disabled=true;button.setAttribute('aria-busy','true');try{await fn()}catch(e){msg(e.message)}finally{button.disabled=false;button.setAttribute('aria-busy','false')}}}
el('execution-id').onchange=fillExecution;
bindConnectionButton('load-connections',async()=>{await loadConnections();fillExecution();msg('已重新读取配置。')});
bindConnectionButton('discover-codex',async()=>{msg('正在查找本机 Codex…');const r=await connectionsCall('discover');el('codex-executable').value=r.executable||'';msg('已找到 Codex '+(r.status?.version||'')+'，保存后生效。')});
bindConnectionButton('save-codex',async()=>{await connectionsCall('save_codex',{executable:el('codex-executable').value.trim()});msg('程序位置已保存。')});
el('execution-form').onsubmit=async e=>{
 e.preventDefault();const button=e.submitter;if(button)button.disabled=true;
 try{
  const p=await connectionsCall('save_execution',{execution:{id:el('execution-id').value,name:el('execution-name').value.trim(),project_dir:el('project-dir').value.trim(),allowed_apps:[],presentation:el('presentation').value,execution:'terminal',permission_profile:el('permission-profile').value.trim(),approval_policy:'never'}});
  await loadConnections();el('execution-id').value=p.id;fillExecution();msg('终端配置已保存。请检查并启用此配置。');
 }catch(e){msg(e.message)}finally{if(button)button.disabled=false}
};
bindConnectionButton('load-permissions',async()=>{
 if(!el('project-dir').value.trim())throw Error('请先填写本机项目目录。');
 const profiles=await connectionsCall('permissions',{execution:{project_dir:el('project-dir').value.trim()}});
 const select=el('permission-profile'),previous=select.value;select.replaceChildren(new Option('请选择已有权限配置',''));
 for(const p of profiles){const option=new Option(p.description?p.id+' · '+p.description:p.id,p.id);option.disabled=!p.allowed;select.add(option)}
 select.value=previous;msg('已读取官方权限配置，没有修改权限。');
});
for(const [button,operation,field] of [['pick-project','pick_project','project-dir'],['pick-codex','pick_codex','codex-executable']])bindConnectionButton(button,async()=>{el(field).value=await connectionsCall(operation)});
bindConnectionButton('select-terminal-mode',async()=>{const r=await connectionsCall('terminal_mode');msg(connectionMessages[r.code]||messages[r.code]||r.code);await refresh()});
async function terminalSetup(operation){
 if(!el('execution-id').value)throw Error('请先保存并选中任务配置。');
 msg('正在检查本机 Codex，请稍候…');
 const r=await connectionsCall(operation,{profile_id:el('execution-id').value});
 msg(connectionMessages[r.code]||messages[r.code]||r.code);await refresh();
}
bindConnectionButton('check-terminal',()=>terminalSetup('check_terminal'));
bindConnectionButton('enable-terminal',()=>terminalSetup('enable_terminal'));
bindConnectionButton('connect-basic',async()=>{msg('正在连接本机 Codex…');const r=await connectionsCall('connect_basic');msg(connectionMessages[r.code]||messages[r.code]||r.code);await refresh()});