const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const elements = new Map();
function element() { return {dataset:{},children:[],value:'',append(...v){this.children.push(...v)},replaceChildren(){this.children=[]},add(v){this.children.push(v)},querySelectorAll(){return this.children.map(x=>x.children[0])}}; }
const sandbox = {el:id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)}, document:{createElement:element},Option:function(text,value){this.text=text;this.value=value},bindConnectionButton(){},setTimeout(){},clearTimeout(){}};
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(process.argv[2],'utf8'),sandbox);
let renderID=0;
function render(prompt){sandbox.prompt={...prompt,ExpiresAt:'2099-01-01T00:00:00Z'};sandbox.renderID=String(++renderID);vm.runInContext('showLocalReview({job_id:"job"},{approval_id:renderID,state:"pending",expires_at:prompt.ExpiresAt},prompt)',sandbox);return elements.get('review-fields').querySelectorAll();}
function answer(){return JSON.parse(vm.runInContext('JSON.stringify(readReviewContent(currentReview,el("review-fields").querySelectorAll()))',sandbox));}
// An app may legitimately ask its own "decision"; do not substitute command approval.
let fields=render({Kind:'app',Schema:{properties:{decision:{type:'string',enum:['yes','no']}},required:['decision']}});
assert.equal(fields.length,1);fields[0].value='0';fields[0].onchange();assert.deepEqual(answer(),{decision:'yes'});
fields=render({Kind:'command',Schema:{properties:{decision:{type:'string',enum:['accept','decline']}},required:['decision']}});
assert.equal(fields.length,0);assert.deepEqual(answer(),{decision:'accept'});
// Untouched optional fields must not produce empty enums, false or zero.
fields=render({Kind:'app',Schema:{properties:{count:{type:'integer'},choice:{type:'string',enum:['a','b']},flag:{type:'boolean'},note:{type:'string'}}}});
assert.deepEqual(answer(),{});fields[0].value='0';fields[0].oninput();assert.deepEqual(answer(),{count:0});
fields[0].value='';assert.deepEqual(answer(),{});
fields=render({Kind:'app',Schema:{properties:{count:{type:'integer'}},required:['count']}});
assert.throws(answer,/count/);fields[0].value='3';assert.deepEqual(answer(),{count:3});
fields[0].value='3.5';assert.throws(answer,/count/);
fields=render({Kind:'app',Schema:{properties:{flag:{type:'boolean'}},required:['flag']}});
assert.throws(answer,/flag/);fields[0].value='false';assert.deepEqual(answer(),{flag:false});
console.log('Local review form regression checks passed');

const {test}=require('node:test');
const source=fs.readFileSync(process.argv[2],'utf8');
const epoch=Date.parse('2026-10-12T10:00:00Z');
function deferred(){let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject};}
async function settle(){for(let i=0;i<12;i++)await Promise.resolve();}
function browser(respond=()=>[]){
 const nodes=new Map(),timers=new Map(),calls=[];let now=epoch,nextTimer=0;
 const context={document:{createElement:element},Option:sandbox.Option,bindConnectionButton(){},msg(text){get('message').textContent=text},
  el:get,Date:class extends Date{constructor(...args){super(...(args.length?args:[now]))}static now(){return now}},
  setTimeout(fn,delay){const id=++nextTimer;timers.set(id,{fn,at:now+delay});return id},clearTimeout(id){timers.delete(id)},
  api:async(path,request)=>{calls.push(request);return respond(request)}};
 function get(id){if(!nodes.has(id))nodes.set(id,element());return nodes.get(id)}
 vm.createContext(context);vm.runInContext(source,context);
 return {get,calls,run:code=>vm.runInContext(code,context),
  show(job=jobAt(),prompt=promptAt()){context.job=job;context.prompt=prompt;return vm.runInContext('showLocalReview(job,job.runtime.approvals[0],prompt)',context)},
  async advance(ms){const end=now+ms;let count=0;while(true){const entry=[...timers].filter(([,v])=>v.at<=end).sort((a,b)=>a[1].at-b[1].at)[0];if(!entry)break;assert.ok(++count<1000,'timer loop');now=entry[1].at;timers.delete(entry[0]);entry[1].fn();await settle()}now=end;await settle()}
 };
}
function jobAt(state='pending',expires=epoch+300000){return {job_id:'job',state:'awaiting_approval',runtime:{approvals:[{approval_id:'approval',operation:'command',state,expires_at:new Date(expires).toISOString()}]}}}
function promptAt(expires=epoch+300000){return {DeviceID:'device',JobID:'job',Kind:'app',Message:'Review this request',ExpiresAt:new Date(expires).toISOString(),Schema:{properties:{note:{type:'string'}}}}}
function assertDisabled(b){for(const id of ['review-accept','review-decline','review-cancel'])assert.equal(b.get(id).disabled,true,id+' must stay disabled')}

test('local deadline disables list and expanded form without extending authorization',async()=>{
 const job=jobAt('pending',epoch+2000),b=browser(()=>[job]);await b.run('loadLocalReviews()');b.show(job,promptAt(epoch+2000));
 assert.match(b.get('review-expiry').textContent,/有效至/);
 assert.equal(b.get('review-accept').disabled,false);
 await b.advance(1999);assert.equal(b.get('review-accept').disabled,false);
 await b.advance(1);assertDisabled(b);assert.match(b.get('review-feedback').textContent,/已过期/);
 assert.equal(b.get('local-jobs').children[0].children[2].disabled,true);
 await b.run('answerLocalReview("accept")');assert.equal(b.calls.filter(c=>c.operation==='answer').length,0);
});
test('expired, consumed, absent or terminal list responses invalidate the open form',async()=>{
 for(const kind of ['expired','consumed','absent','approval_absent','completed','failed','cancelled','blocked','rejected','local_takeover']){
  const job=jobAt(),b=browser(()=>kind==='absent'?[]:[job]);b.show();
  if(['completed','failed','cancelled','blocked','rejected','local_takeover'].includes(kind))job.state=kind;
  else if(kind==='approval_absent')job.runtime.approvals=[];
  else job.runtime.approvals[0].state=kind;
  await b.run('loadLocalReviews()');assertDisabled(b);assert.match(b.get('review-feedback').textContent,/过期|失效|结束|已处理/);
 }
});
test('read-only refresh keeps typed fields and never extends the original expiry',async()=>{
 const b=browser(()=>[jobAt('pending',epoch+600000)]);b.show(jobAt('pending',epoch+2000),promptAt(epoch+2000));
 const input=b.get('review-fields').querySelectorAll()[0];input.value='keep my answer';input.oninput();
 await b.run('loadLocalReviews()');assert.equal(b.get('review-fields').querySelectorAll()[0],input);assert.equal(input.value,'keep my answer');
 b.show(jobAt('pending',epoch+600000),promptAt(epoch+600000));assert.equal(b.get('review-fields').querySelectorAll()[0],input);
 await b.advance(2000);assertDisabled(b);
});
test('list polling is low frequency and serial even during manual refresh',async()=>{
 const pending=deferred();let lists=0;const b=browser(()=>{lists++;return lists===1?pending.promise:[jobAt()]});
 const first=b.run('loadLocalReviews()'),second=b.run('loadLocalReviews()');await b.advance(30000);assert.equal(lists,1);
 pending.resolve([jobAt()]);await Promise.all([first,second]);await b.advance(9000);assert.equal(lists,1);
 await b.advance(1000);assert.equal(lists,2);assert.ok(b.calls.every(c=>c.operation==='list'));
});
test('opening details alone starts read-only refresh',async()=>{
 const b=browser(()=>[jobAt('expired')]);b.show();await b.advance(10000);assert.equal(b.calls.length,1);assertDisabled(b);
});
test('successful answer stays visibly acknowledged next to the disabled form',async()=>{
 const pending=deferred(),b=browser(request=>request.operation==='answer'?pending.promise:[]);b.show();
 const first=b.run('answerLocalReview("accept")'),second=b.run('answerLocalReview("accept")');assert.equal(b.calls.filter(c=>c.operation==='answer').length,1);
 pending.resolve({state:'running'});await Promise.all([first,second]);assertDisabled(b);assert.equal(b.get('local-review').hidden,false);assert.match(b.get('review-feedback').textContent,/已提交.*回执/);
 await b.advance(20000);assert.equal(b.calls.filter(c=>c.operation==='answer').length,1);
});
test('uncertain answer never automatically or manually replays, including reopening',async()=>{
 const b=browser(request=>{if(request.operation==='answer')throw Error('network failed');return [jobAt()]});b.show();
 await b.run('answerLocalReview("accept")');assertDisabled(b);assert.match(b.get('review-feedback').textContent,/未确认|不确定/);
 await b.advance(10000);b.show();await b.run('answerLocalReview("accept")');assertDisabled(b);assert.equal(b.calls.filter(c=>c.operation==='answer').length,1);
});
test('stale or missing answer response makes the current form invalid',async()=>{
 for(const code of ['approval_stale','approval_not_found']){
  const b=browser(request=>{if(request.operation==='answer')throw Error(code);return []});b.show();await b.run('answerLocalReview("decline")');assertDisabled(b);assert.match(b.get('review-feedback').textContent,/失效|已处理|不存在/);
 }
});
test('expired prompt response cannot create a newly active form',()=>{
 const b=browser();b.show(jobAt(),promptAt(epoch-1));assertDisabled(b);assert.match(b.get('review-feedback').textContent,/已过期/);
});
test('failed refresh preserves input and local expiry still closes the form',async()=>{
 const b=browser(()=>{throw Error('network failed')});b.show(jobAt('pending',epoch+12000),promptAt(epoch+12000));
 const input=b.get('review-fields').querySelectorAll()[0];input.value='retain';input.oninput();
 await b.advance(10000);assert.equal(b.get('review-fields').querySelectorAll()[0],input);assert.equal(input.value,'retain');assert.equal(b.get('review-accept').disabled,false);
 assert.match(b.get('reviews-status').textContent,/刷新未完成/);await b.advance(2000);assertDisabled(b);
 await b.advance(8000);assert.equal(b.calls.length,2);assert.ok(b.calls.every(c=>c.operation==='list'));
});
test('failed readback cannot erase a confirmed write receipt',async()=>{
 const b=browser(request=>{if(request.operation==='answer')return {state:'running'};throw Error('network failed')});b.show();
 await b.run('answerLocalReview("accept")');assertDisabled(b);assert.match(b.get('review-feedback').textContent,/已提交.*回执/);
 assert.match(b.get('reviews-status').textContent,/刷新未完成/);assert.equal(b.calls.filter(c=>c.operation==='answer').length,1);
});
test('a prompt error for an already expanded approval invalidates that form',async()=>{
 const b=browser(request=>{if(request.operation==='prompt')throw Error('approval_not_found');return [jobAt()]});b.show();await b.run('loadLocalReviews()');
 await b.get('local-jobs').children[0].children[2].onclick();assertDisabled(b);assert.match(b.get('review-feedback').textContent,/失效|不存在/);
});
test('a late answer receipt never clears or enables another approval form',async()=>{
 const pending=deferred(),other=jobAt();other.runtime.approvals[0].approval_id='other';
 const b=browser(request=>request.operation==='answer'?pending.promise:[other]);b.show();const first=b.run('answerLocalReview("accept")');b.show(other);
 const input=b.get('review-fields').querySelectorAll()[0];input.value='second answer';input.oninput();
 pending.resolve({state:'running'});await first;assert.equal(b.get('review-fields').querySelectorAll()[0],input);assert.equal(input.value,'second answer');assert.equal(b.get('review-accept').disabled,false);
 assert.match(b.get('review-feedback').textContent,/尚未提交/);
});
test('uncertain takeover request is not repeated by refresh or another click',async()=>{
 const job=jobAt('awaiting_target');job.runtime.execution='computer_use';
 const b=browser(request=>{if(request.operation==='take_over')throw Error('network failed');return [job]});await b.run('loadLocalReviews()');
 await b.get('local-jobs').children[0].children[2].onclick();await b.advance(10000);const button=b.get('local-jobs').children[0].children[2];assert.equal(button.disabled,true);await button.onclick();
 assert.equal(b.calls.filter(c=>c.operation==='take_over').length,1);
});
test('late details cannot replace the latest selected approval or its typed answer',async()=>{
 const firstPrompt=deferred(),first=jobAt(),second=jobAt();second.job_id='second';second.runtime.approvals[0].approval_id='second-approval';
 const b=browser(request=>request.operation==='prompt'?(request.job_id==='job'?firstPrompt.promise:{...promptAt(),JobID:'second',Message:'Second approval'}):[first,second]);
 await b.run('loadLocalReviews()');const firstOpen=b.get('local-jobs').children[0].children[2].onclick();
 await b.get('local-jobs').children[1].children[2].onclick();const input=b.get('review-fields').querySelectorAll()[0];input.value='answer for second only';input.oninput();
 firstPrompt.resolve(promptAt(epoch+2000));await firstOpen;
 assert.equal(b.run('currentReview.job_id'),'second');assert.equal(b.get('review-fields').querySelectorAll()[0],input);assert.equal(input.value,'answer for second only');assert.match(b.get('review-detail').textContent,/Second approval/);
 await b.advance(2000);assert.equal(b.get('local-jobs').children[0].children[2].disabled,true);assert.equal(b.get('review-accept').disabled,false);
 await b.run('answerLocalReview("accept")');const answers=b.calls.filter(c=>c.operation==='answer');assert.equal(answers.length,1);assert.equal(answers[0].job_id,'second');assert.equal(answers[0].approval_id,'second-approval');assert.equal(answers[0].answer.Content.note,'answer for second only');
});
test('selecting native takeover invalidates an earlier pending details response',async()=>{
 const pending=deferred(),first=jobAt(),target=jobAt('awaiting_target');target.job_id='target';target.runtime.execution='computer_use';
 const b=browser(request=>request.operation==='prompt'?pending.promise:request.operation==='list'?[first,target]:{});
 b.get('local-review').hidden=true;await b.run('loadLocalReviews()');const firstOpen=b.get('local-jobs').children[0].children[2].onclick();
 await b.get('local-jobs').children[1].children[2].onclick();pending.resolve(promptAt());await firstOpen;
 assert.equal(b.run('currentReview'),null);assert.equal(b.get('local-review').hidden,true);assert.equal(b.calls.filter(c=>c.operation==='take_over').length,1);
});
test('selecting different details hides the previous actionable form while loading',async()=>{
 const pending=deferred(),first=jobAt(),second=jobAt();second.job_id='second';
 const b=browser(request=>request.operation==='prompt'?pending.promise:[first,second]);b.show(first);await b.run('loadLocalReviews()');
 const opening=b.get('local-jobs').children[1].children[2].onclick();assert.equal(b.get('local-review').hidden,true);
 await b.run('answerLocalReview("accept")');assert.equal(b.calls.filter(c=>c.operation==='answer').length,0);
 pending.resolve({...promptAt(),JobID:'second'});await opening;assert.equal(b.get('local-review').hidden,false);assert.equal(b.run('currentReview.job_id'),'second');
});
