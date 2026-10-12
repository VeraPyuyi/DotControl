'use strict';
let currentReview=null,reviewRefresh=null,reviewRefreshTimer=null,reviewExpiryTimer=null,reviewRows=[],reviewSelection=0;
const reviewRecords=new Map(),reviewTerminalStates=new Set(['completed','failed','cancelled','blocked','rejected','local_takeover']);
const reviewStates={waiting_for_idle:'等待电脑连续空闲',awaiting_approval:'等待人工处理',awaiting_target:'需要此电脑处理',local_takeover:'已交给本机 Codex',outcome_unknown:'停止或结果尚未确认',completed:'轮次已结束，需核对结果',cancelled:'已取消',running:'执行中'};
function reviewRecord(job,approval){
 const key=JSON.stringify([job.job_id,approval.approval_id]);let binding=reviewRecords.get(key);
 if(!binding){binding={job_id:job.job_id,approval_id:approval.approval_id,expires:Infinity,state:'ready',notice:'等待你的处理；尚未提交人工回答。'};reviewRecords.set(key,binding)}
 const expires=Date.parse(approval.expires_at);binding.expires=Math.min(binding.expires,Number.isFinite(expires)?expires:0);
 checkReview(binding,job,approval);return binding;
}
function endReview(binding,state,notice){
 if(binding.state!=='ready')return;
 binding.state=state;binding.notice=notice;
}
function checkReview(binding,job,approval){
 if(binding.expires<=Date.now()||approval?.state==='expired')endReview(binding,'expired','本次授权已过期，不能再提交；不会自动续期或重新生成授权。');
 else if(!job||reviewTerminalStates.has(job.state)||!approval||!['pending','awaiting_target'].includes(approval.state))endReview(binding,'stale','本次授权已失效：任务已结束、请求已处理或已不存在。请核对任务状态。');
}
function reviewExpiryText(binding){return Number.isFinite(binding.expires)&&binding.expires>0?'有效至 '+new Date(binding.expires).toLocaleString()+'（本次授权不会自动续期）':'无法确认授权有效期，不能提交。'}
function updateReviewDisplay(){
 clearTimeout(reviewExpiryTimer);let next=Infinity;
 for(const binding of reviewRecords.values()){
  if(binding.expires<=Date.now())endReview(binding,'expired','本次授权已过期，不能再提交；不会自动续期或重新生成授权。');
  if(binding.state==='ready')next=Math.min(next,binding.expires);
 }
 for(const {binding,p,button,operation} of reviewRows){
  p.textContent=operation+' · '+(binding.state==='ready'?'等待人工处理':binding.notice)+' · '+reviewExpiryText(binding);
  button.disabled=binding.state!=='ready'||!!binding.opening;
 }
 if(currentReview){
  el('review-expiry').textContent=reviewExpiryText(currentReview);
  el('review-feedback').textContent=currentReview.notice;
  for(const id of ['review-accept','review-decline','review-cancel'])el(id).disabled=currentReview.state!=='ready';
  for(const input of el('review-fields').querySelectorAll('[data-field]'))input.disabled=currentReview.state!=='ready';
 }
 if(Number.isFinite(next))reviewExpiryTimer=setTimeout(updateReviewDisplay,Math.max(0,next-Date.now()));
}
function scheduleReviewRefresh(){
 clearTimeout(reviewRefreshTimer);
 if(!reviewRefresh)reviewRefreshTimer=setTimeout(()=>{loadLocalReviews().catch(()=>{el('reviews-status').textContent='待办刷新未完成；现有授权不会续期，请稍后核对状态。'})},10000);
}
function loadLocalReviews(){
 if(reviewRefresh)return reviewRefresh;
 clearTimeout(reviewRefreshTimer);
 reviewRefresh=(async()=>{
  const jobs=await api('reviews',{operation:'list'}),box=el('local-jobs');box.replaceChildren();reviewRows=[];
  for(const binding of reviewRecords.values()){
   const job=jobs.find(j=>j.job_id===binding.job_id),approval=job?.runtime?.approvals?.find(a=>a.approval_id===binding.approval_id);
   checkReview(binding,job,approval);
  }
  for(const job of jobs){
   const row=document.createElement('article'),heading=document.createElement('p');heading.textContent=job.job_id+' · '+(reviewStates[job.state]||job.state);row.append(heading);
   for(const approval of job.runtime?.approvals||[]){
    const binding=reviewRecord(job,approval),p=document.createElement('p'),button=document.createElement('button');button.className='secondary';row.append(p);
    const target=job.runtime.execution==='computer_use'||approval.state==='awaiting_target';button.textContent=target?'停止远程执行并在 Codex 接手':'查看本次授权';
    button.onclick=async()=>{
     updateReviewDisplay();if(binding.state!=='ready'||binding.opening)return;
     const selection=++reviewSelection;
     if(currentReview!==binding||target){currentReview=null;el('local-review').hidden=true}
     binding.opening=true;updateReviewDisplay();
     try{
      if(target){
       binding.state='submitting';binding.notice='正在请求中断，请等待回执。';updateReviewDisplay();
       await api('reviews',{operation:'take_over',job_id:job.job_id,approval_id:approval.approval_id});
       binding.state='submitted';binding.notice='已收到中断请求回执；官方确认停止并清理连接后才会打开任务。';if(selection===reviewSelection)el('reviews-status').textContent=binding.notice;
      }else{
       const prompt=await api('reviews',{operation:'prompt',job_id:job.job_id,approval_id:approval.approval_id});showLocalReview(job,approval,prompt,selection);
      }
     }catch(e){
      if(['approval_stale','approval_not_found'].includes(e.message)){binding.state='stale';binding.notice='本次授权已失效或已不存在，请核对任务状态。'}
      else if(target){binding.state='uncertain';binding.notice='中断请求结果未确认，不会自动重发；请刷新并核对任务状态。'}
      if(selection===reviewSelection)el('reviews-status').textContent=binding.state==='ready'?e.message:binding.notice;
     }finally{binding.opening=false;updateReviewDisplay()}
    };
    reviewRows.push({binding,p,button,operation:approval.operation});row.append(button);
   }box.append(row);
  }
  if(!jobs.length)box.textContent='没有 DotControl 创建的任务。';
  el('reviews-status').textContent='待办状态已刷新；等待人工处理表示尚未提交回答。';updateReviewDisplay();
 })().finally(()=>{reviewRefresh=null;scheduleReviewRefresh()});
 return reviewRefresh;
}
function showLocalReview(job,approval,prompt,selection=++reviewSelection){
 const binding=reviewRecord(job,approval),expires=Date.parse(prompt.ExpiresAt);binding.expires=Math.min(binding.expires,Number.isFinite(expires)?expires:0);
 // A late response still constrains its own expiry, but cannot replace the latest selection.
 if(selection!==reviewSelection){updateReviewDisplay();return}
 el('local-review').hidden=false;scheduleReviewRefresh();
 if(currentReview===binding){updateReviewDisplay();return}
 currentReview=binding;binding.prompt=prompt;
 el('review-detail').textContent='目标设备：'+prompt.DeviceID+'\n任务：'+prompt.JobID+'\n'+prompt.Message;
 const fields=el('review-fields');fields.replaceChildren();
 for(const [name,spec] of Object.entries(prompt.Schema?.properties||{})){
  if(name==='decision'&&prompt.Kind!=='app')continue;
  const label=document.createElement('label');label.textContent=spec.title||name;let input;
  if(Array.isArray(spec.enum)){input=document.createElement('select');input.add(new Option('请选择',''));spec.enum.forEach((v,i)=>input.add(new Option(String(v),String(i))))}else if(spec.type==='boolean'){input=document.createElement('select');input.add(new Option('请选择',''));input.add(new Option('是','true'));input.add(new Option('否','false'))}else{input=document.createElement('input');input.type=spec.type==='number'||spec.type==='integer'?'number':'text';if(spec.type==='integer')input.step='1';input.maxLength=Math.min(spec.maxLength||4096,4096)}
  input.required=(prompt.Schema?.required||[]).includes(name);input.dataset.field=name;input.dataset.kind=spec.type;input.dataset.touched='false';input.oninput=input.onchange=()=>{input.dataset.touched='true'};label.append(input);fields.append(label);
 }
 updateReviewDisplay();
 el('local-review').scrollIntoView?.({block:'start',behavior:'smooth'});
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
 updateReviewDisplay();if(!currentReview||currentReview.state!=='ready')return;const binding=currentReview;let content={};
 try{if(action==='accept')content=readReviewContent(binding,el('review-fields').querySelectorAll('[data-field]'))}catch(e){binding.notice=e.message;updateReviewDisplay();return}
 binding.state='submitting';binding.notice='正在提交本次人工回答，请等待回执。';updateReviewDisplay();
 try{
  await api('reviews',{operation:'answer',job_id:binding.job_id,approval_id:binding.approval_id,answer:{Action:action,Content:content}});
  binding.state='submitted';binding.notice='本次人工回答已提交，并收到本机回执。任务是否完成仍需核对任务状态。';
 }catch(e){
  binding.state=['approval_stale','approval_not_found'].includes(e.message)?'stale':'uncertain';
  binding.notice=binding.state==='stale'?'本次授权已失效、已处理或已不存在，不能再提交。':'本次提交结果未确认，不会自动重发，也不能再次提交。请刷新并核对任务状态。';
 }
 updateReviewDisplay();
 // A failed read after a confirmed write must not turn that receipt into an uncertain write.
 try{await loadLocalReviews()}catch{el('reviews-status').textContent='待办刷新未完成，请稍后核对任务状态。'}
}
bindConnectionButton('load-reviews',loadLocalReviews);
el('review-accept').onclick=()=>answerLocalReview('accept');el('review-decline').onclick=()=>answerLocalReview('decline');el('review-cancel').onclick=()=>answerLocalReview('cancel');
