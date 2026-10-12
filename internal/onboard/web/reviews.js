'use strict';
let currentReview=null;
const reviewStates={waiting_for_idle:'等待电脑连续空闲',awaiting_approval:'等待人工处理',awaiting_target:'需要此电脑处理',local_takeover:'已交给本机 Codex',outcome_unknown:'停止或结果尚未确认',completed:'轮次已结束，需核对结果',cancelled:'已取消',running:'执行中'};
async function loadLocalReviews(){
 const jobs=await api('reviews',{operation:'list'}),box=el('local-jobs');box.replaceChildren();
 for(const job of jobs){
  const row=document.createElement('article'),heading=document.createElement('p');heading.textContent=job.job_id+' · '+(reviewStates[job.state]||job.state);row.append(heading);
  for(const approval of job.runtime?.approvals||[]){
   const p=document.createElement('p');p.textContent=approval.operation+' · '+approval.state;row.append(p);
   const button=document.createElement('button');button.className='secondary';
   const target=job.runtime.execution==='computer_use'||approval.state==='awaiting_target';button.textContent=target?'停止远程执行并在 Codex 接手':'查看本次授权';button.disabled=approval.state==='expired';
   button.onclick=async()=>{button.disabled=true;try{if(target){await api('reviews',{operation:'take_over',job_id:job.job_id,approval_id:approval.approval_id});msg('已请求中断。官方确认停止并清理连接后才会打开任务。');await loadLocalReviews()}else{const prompt=await api('reviews',{operation:'prompt',job_id:job.job_id,approval_id:approval.approval_id});showLocalReview(job,approval,prompt)}}catch(e){msg(e.message)}finally{button.disabled=false}};row.append(button);
  }box.append(row);
 }if(!jobs.length)box.textContent='没有 DotControl 创建的任务。';
}
function showLocalReview(job,approval,prompt){
 currentReview={job_id:job.job_id,approval_id:approval.approval_id,prompt};el('local-review').hidden=false;
 el('review-detail').textContent='目标设备：'+prompt.DeviceID+'\n任务：'+prompt.JobID+'\n'+prompt.Message+'\n有效期：'+prompt.ExpiresAt;
 const fields=el('review-fields');fields.replaceChildren();
 for(const [name,spec] of Object.entries(prompt.Schema?.properties||{})){
  if(name==='decision'&&prompt.Kind!=='app')continue;
  const label=document.createElement('label');label.textContent=spec.title||name;let input;
  if(Array.isArray(spec.enum)){input=document.createElement('select');input.add(new Option('请选择',''));spec.enum.forEach((v,i)=>input.add(new Option(String(v),String(i))))}else if(spec.type==='boolean'){input=document.createElement('select');input.add(new Option('请选择',''));input.add(new Option('是','true'));input.add(new Option('否','false'))}else{input=document.createElement('input');input.type=spec.type==='number'||spec.type==='integer'?'number':'text';if(spec.type==='integer')input.step='1';input.maxLength=Math.min(spec.maxLength||4096,4096)}
  input.required=(prompt.Schema?.required||[]).includes(name);input.dataset.field=name;input.dataset.kind=spec.type;input.dataset.touched='false';input.oninput=input.onchange=()=>{input.dataset.touched='true'};label.append(input);fields.append(label);
 }
}
function readReviewContent(binding,fields){
 const content={},schema=binding.prompt.Schema||{};
 if(binding.prompt.Kind!=='app'&&schema.properties?.decision)content.decision='accept';
 for(const input of fields){
  const name=input.dataset.field,spec=schema.properties[name],required=(schema.required||[]).includes(name),raw=input.value;
  if(!required&&input.dataset.touched!=='true')continue;
  const blankChoice=Array.isArray(spec.enum)||['boolean','number','integer'].includes(spec.type);
  if(blankChoice&&raw.trim()===''){if(required)throw new Error('请填写：'+name);continue}
  let value=raw;
  if(Array.isArray(spec.enum)){const index=Number(raw);if(!Number.isInteger(index)||index<0||index>=spec.enum.length)throw new Error('请选择：'+name);value=spec.enum[index]}
  else if(spec.type==='boolean'){if(!['true','false'].includes(raw))throw new Error('请选择：'+name);value=raw==='true'}
  else if(['number','integer'].includes(spec.type)){value=Number(raw);if(!Number.isFinite(value)||(spec.type==='integer'&&!Number.isInteger(value)))throw new Error('请输入有效数字：'+name)}
  content[name]=value;
 }return content;
}
async function answerLocalReview(action){
 if(!currentReview)return;const binding=currentReview;let content={};
 try{if(action==='accept')content=readReviewContent(binding,el('review-fields').querySelectorAll('[data-field]'))}catch(e){msg(e.message);return}
 for(const id of ['review-accept','review-decline','review-cancel'])el(id).disabled=true;
 try{await api('reviews',{operation:'answer',job_id:binding.job_id,approval_id:binding.approval_id,answer:{Action:action,Content:content}});currentReview=null;el('local-review').hidden=true;msg('已提交本次人工回答；任务状态以官方回执为准。');await loadLocalReviews()}catch(e){msg(e.message)}finally{for(const id of ['review-accept','review-decline','review-cancel'])el(id).disabled=false}
}
bindConnectionButton('load-reviews',loadLocalReviews);
el('review-accept').onclick=()=>answerLocalReview('accept');el('review-decline').onclick=()=>answerLocalReview('decline');el('review-cancel').onclick=()=>answerLocalReview('cancel');
