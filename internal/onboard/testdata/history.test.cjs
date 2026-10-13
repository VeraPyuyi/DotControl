const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),test=require('node:test');
const source=fs.readFileSync(process.argv[2],'utf8');
const saved=(mode='allowlist',ids=['old'],revision=2)=>({revision,mode,allowed_thread_ids:ids});
const status=policy=>({policy,capability:{available:!!policy.revision}});
const preview=(policy,mode,ids=[])=>({preview_id:'preview-'+policy.revision,revision:policy.revision,mode,threads:ids.map(thread_id=>({thread_id,title:'Title '+thread_id,cwd:'project'})),added:[],removed:[],unresolved:[],coverage:mode==='all'?'all_local_threads_including_future':'explicit_selection_only'});
const deferred=()=>{let resolve;const promise=new Promise(r=>{resolve=r});return {promise,resolve}};
function harness(policy=saved()){
 const nodes=new Map(),calls=[];
 const node=()=>({value:'',checked:false,hidden:false,disabled:false,children:[],textContent:'',append(...v){this.children.push(...v)},replaceChildren(...v){this.children=v}});
 const el=id=>{if(!nodes.has(id))nodes.set(id,node());return nodes.get(id)};
 const h={el,calls,policy,handler:null};
 const document={activeElement:null,createElement:node};
 const ctx={el,document,feedback:(box,text)=>{box.textContent=text},navigator:{clipboard:{writeText:async()=>{}}},api:async(_,request)=>{
  calls.push(JSON.parse(JSON.stringify(request)));
  if(h.handler){const result=h.handler(request);if(result!==undefined)return result}
  switch(request.operation){
   case 'status':return status(h.policy);
   case 'enable':if(!h.policy.revision)h.policy=saved('all',[],1);return h.policy;
   case 'disable':return {enabled:false};
   case 'catalog':return {threads:['old','other'].map(thread_id=>({thread_id,title:thread_id})),next_cursor:''};
   case 'preview':return preview(h.policy,request.mode,request.thread_ids);
   case 'previews':return [];
   case 'apply':{const p=h.calls.findLast(c=>c.operation==='preview');h.policy=saved(p.mode,p.thread_ids||[],h.policy.revision+1);return h.policy}
   default:throw Error('Unexpected operation '+request.operation);
  }
 }};
 vm.createContext(ctx);vm.runInContext(source,ctx);
 h.run=code=>vm.runInContext(code,ctx);h.refresh=()=>h.run('historyStatus()');
 h.click=async id=>{assert.equal(typeof el(id).onclick,'function',id+' has a click action');await el(id).onclick()};
 h.mode=mode=>{const control=el('history-mode-'+mode);assert.equal(typeof control.onchange,'function','scope has a change action');control.checked=true;control.onchange()};
 h.choose=(id,checked)=>{const control=h.run('historyChecks').get(id);assert.ok(control,'candidate is rendered');control.checked=checked;control.onchange()};
 h.ids=()=>Array.from(h.run('historyChosen')).sort();
 h.hasPreview=()=>!!h.run('historyPreview');
 h.untilCall=async(operation,count=1)=>{for(let i=0;i<10;i++){if(calls.filter(c=>c.operation===operation).length>=count)return;await new Promise(setImmediate)}assert.fail('Request did not start: '+operation)};
 h.ctx=ctx;return h;
}

test('unconfigured setup defaults to all, with no allowlist or permission write',async()=>{
 const h=harness({revision:0,mode:'',allowed_thread_ids:null});await h.refresh();
 assert.equal(h.el('history-mode-all').checked,true);
 assert.equal(h.el('history-mode-allowlist').checked,false);
 assert.equal(h.el('history-allowlist').hidden,true);
 assert.deepEqual(h.calls.map(c=>c.operation),['status']);
 await h.click('history-preview-scope');
 assert.equal(h.calls.some(c=>c.operation==='preview'||c.operation==='enable'||c.operation==='apply'),false,'unconfigured preview cannot implicitly enable sharing');
 assert.match(h.el('history-message').textContent,/启用/);
});

test('saved allowlist, including an empty one, is reflected on first load',async()=>{
 for(const ids of [['old'],[]]){
  const h=harness(saved('allowlist',ids));await h.refresh();
  assert.equal(h.el('history-mode-allowlist').checked,true);
  assert.equal(h.el('history-allowlist').hidden,false);assert.deepEqual(h.ids(),ids);
 }
});

test('mode edits are drafts and all uses the same preview then apply flow',async()=>{
 const h=harness();await h.refresh();h.mode('all');
 assert.equal(h.el('history-allowlist').hidden,true);
 assert.match(h.el('history-state').textContent,/1 个/,'saved scope remains separate');
 assert.deepEqual(h.calls.map(c=>c.operation),['status']);
 await h.click('history-preview-scope');
 assert.deepEqual(h.calls.find(c=>c.operation==='preview'),{operation:'preview',revision:2,mode:'all'});
 assert.equal(h.hasPreview(),true);assert.match(h.el('history-preview-text').textContent,/未来/);
 assert.equal(h.calls.some(c=>c.operation==='apply'),false);
 await h.click('history-apply');assert.equal(h.policy.mode,'all');assert.equal(h.hasPreview(),false);assert.deepEqual(h.ids(),[]);
});

test('explicit empty allowlist previews and saves deny-all, without widening to all',async()=>{
 const h=harness(saved('all',[],1));await h.refresh();h.mode('allowlist');
 await h.click('history-preview-scope');
 assert.deepEqual(h.calls.find(c=>c.operation==='preview'),{operation:'preview',revision:1,mode:'allowlist',thread_ids:[]});
 assert.match(h.el('history-preview-text').textContent,/空.*禁止全部读取/);
 await h.click('history-apply');assert.equal(h.policy.mode,'allowlist');assert.deepEqual(h.ids(),[]);
});

test('polling preserves unsaved mode, candidates, DOM nodes and focus across external policy updates',async()=>{
 const h=harness();await h.refresh();await h.click('history-load');h.choose('old',false);h.choose('other',true);
 h.mode('all');const check=h.run("historyChecks.get('other')");h.ctx.document.activeElement=h.el('history-mode-all');
 h.policy=saved('allowlist',['elsewhere'],3);await h.refresh();
 assert.equal(h.el('history-mode-all').checked,true);assert.deepEqual(h.ids(),['other']);
 assert.equal(h.run("historyChecks.get('other')"),check);assert.equal(check.checked,true);
 assert.equal(h.ctx.document.activeElement,h.el('history-mode-all'));
 h.mode('allowlist');assert.deepEqual(h.ids(),['other']);
});

test('edits before initial status arrives survive initialization',async()=>{
 const h=harness(),pending=deferred();h.handler=r=>r.operation==='status'?pending.promise:undefined;
 const loading=h.refresh();h.mode('allowlist');pending.resolve(status(saved('all',[],1)));await loading;
 assert.equal(h.el('history-mode-allowlist').checked,true);assert.equal(h.el('history-allowlist').hidden,false);
 assert.deepEqual(h.ids(),[]);
});

test('editing mode or candidates invalidates an already displayed preview',async()=>{
 const h=harness();await h.refresh();await h.click('history-load');await h.click('history-preview-scope');
 h.choose('other',true);assert.equal(h.hasPreview(),false);assert.equal(h.el('history-preview').hidden,true);
 await h.click('history-apply');assert.equal(h.calls.some(c=>c.operation==='apply'),false);
 await h.click('history-preview-scope');h.mode('all');assert.equal(h.hasPreview(),false);
});

test('a preview response cannot reappear after a newer draft edit',async()=>{
 const h=harness(),pending=deferred();await h.refresh();
 h.handler=r=>r.operation==='preview'?pending.promise:undefined;
 const loading=h.click('history-preview-scope');await h.untilCall('preview');
 h.mode('all');pending.resolve(preview(h.policy,'allowlist',['old']));await loading;
 assert.equal(h.hasPreview(),false);assert.equal(h.el('history-preview').hidden,true);
});

test('external policy revisions invalidate displayed and in-flight previews',async()=>{
 const h=harness();await h.refresh();await h.click('history-preview-scope');
 h.policy=saved('all',[],3);await h.refresh();assert.equal(h.hasPreview(),false);
 const pending=deferred();h.handler=r=>r.operation==='preview'?pending.promise:undefined;
 const loading=h.click('history-preview-scope');await h.untilCall('preview',2);
 h.policy=saved('allowlist',[],4);await h.refresh();pending.resolve(preview(saved('all',[],3),'allowlist',['old']));await loading;
 assert.equal(h.hasPreview(),false);assert.deepEqual(h.ids(),['old']);
});

test('discovering an external revision on preview gives a clear retry message',async()=>{
 const h=harness();await h.refresh();h.mode('all');h.policy=saved('allowlist',['elsewhere'],3);
 await h.click('history-preview-scope');
 assert.equal(h.hasPreview(),false);assert.equal(h.calls.some(c=>c.operation==='preview'),false);
 assert.match(h.el('history-message').textContent,/已变更.*重新预览/);
 assert.equal(h.el('history-mode-all').checked,true);assert.deepEqual(h.ids(),['old']);
 await h.click('history-preview-scope');assert.equal(h.hasPreview(),true);
});

test('skill preview imports its exact mode and IDs without applying permissions',async()=>{
 const h=harness(saved('all',[],1));await h.refresh();
 h.handler=r=>r.operation==='previews'?[preview(h.policy,'allowlist',['imported'])]:undefined;
 await h.click('history-pending');assert.equal(h.el('history-mode-allowlist').checked,true);
 assert.equal(h.el('history-allowlist').hidden,false);assert.deepEqual(h.ids(),['imported']);
 assert.equal(h.run("historyChecks.get('imported').checked"),true);assert.equal(h.hasPreview(),true);
 assert.equal(h.calls.some(c=>c.operation==='apply'),false);
});

test('stale skill previews and imports arriving after a local edit cannot replace drafts',async()=>{
 const h=harness();await h.refresh();
 h.handler=r=>r.operation==='previews'?[preview(saved('all',[],1),'all')]:undefined;
 await h.click('history-pending');assert.equal(h.hasPreview(),false);assert.deepEqual(h.ids(),['old']);
 const pending=deferred();h.handler=r=>r.operation==='previews'?pending.promise:undefined;
 const loading=h.click('history-pending');await h.untilCall('previews',2);h.mode('all');
 pending.resolve([preview(h.policy,'allowlist',['imported'])]);await loading;
 assert.equal(h.el('history-mode-all').checked,true);assert.deepEqual(h.ids(),['old']);assert.equal(h.hasPreview(),false);
});

test('re-enabling saved restrictions never initializes them to all',async()=>{
 const h=harness();await h.refresh();await h.click('history-enable');
 assert.equal(h.el('history-mode-allowlist').checked,true);assert.deepEqual(h.ids(),['old']);
 assert.equal(h.calls.some(c=>c.operation==='apply'||c.operation==='preview'),false);
});

test('edits made while first enable is pending survive its status response',async()=>{
 const h=harness({revision:0,mode:'',allowed_thread_ids:null});await h.refresh();
 const pending=deferred();h.handler=r=>r.operation==='enable'?pending.promise:undefined;
 const enabling=h.click('history-enable');h.mode('allowlist');
 h.policy=saved('all',[],1);pending.resolve(h.policy);await enabling;
 assert.equal(h.el('history-mode-allowlist').checked,true);assert.deepEqual(h.ids(),[]);
 assert.match(h.el('history-state').textContent,/全部本机会话/);
 assert.equal(h.calls.some(c=>c.operation==='apply'),false);
});

test('late status responses cannot roll back the saved policy',async()=>{
 const h=harness();await h.refresh();const requests=[];
 h.handler=r=>r.operation==='status'?new Promise(resolve=>requests.push(resolve)):undefined;
 const older=h.refresh(),newer=h.refresh();requests[1](status(saved('all',[],4)));await newer;
 requests[0](status(saved('allowlist',[],3)));await older;
 assert.equal(h.run('historyPolicy.revision'),4);
});

test('post-apply status superseded by a poll still synchronizes the draft without replacing nodes',async()=>{
 const h=harness();await h.refresh();await h.click('history-load');h.choose('other',true);h.mode('all');await h.click('history-preview-scope');
 const check=h.run("historyChecks.get('other')"),requests=[];
 h.handler=r=>r.operation==='status'?new Promise(resolve=>requests.push(resolve)):undefined;
 const statusCount=h.calls.filter(c=>c.operation==='status').length;
 const applying=h.click('history-apply');await h.untilCall('status',statusCount+1);
 const polling=h.run('historyRefreshStatus()');
 requests[0](status(h.policy));await applying;requests[1](status(h.policy));await polling;
 assert.deepEqual(h.ids(),[]);assert.equal(h.run("historyChecks.get('other')"),check);assert.equal(check.checked,false);
 assert.equal(h.el('history-mode-all').checked,true);
});

test('edits made during the post-apply status read are not reset',async()=>{
 const h=harness();await h.refresh();await h.click('history-load');await h.click('history-preview-scope');
 const pending=deferred(),statusCount=h.calls.filter(c=>c.operation==='status').length;
 h.handler=r=>r.operation==='status'?pending.promise:undefined;
 const applying=h.click('history-apply');await h.untilCall('status',statusCount+1);
 h.choose('other',true);h.mode('all');pending.resolve(status(h.policy));await applying;
 assert.deepEqual(h.ids(),['old','other']);assert.equal(h.el('history-mode-all').checked,true);
});

test('edits made while apply is in flight survive its response and post-apply status',async()=>{
 const h=harness();await h.refresh();await h.click('history-load');await h.click('history-preview-scope');
 const pending=deferred();h.handler=r=>r.operation==='apply'?pending.promise:undefined;
 const applying=h.click('history-apply');h.choose('other',true);h.mode('all');
 h.policy=saved('allowlist',['old'],3);pending.resolve(h.policy);await applying;
 assert.deepEqual(h.ids(),['old','other']);assert.equal(h.el('history-mode-all').checked,true);
 assert.match(h.el('history-state').textContent,/1 个/);
});

test('pause and revocation status remain visible without replacing the draft',async()=>{
 const h=harness();await h.refresh();
 h.ctx.runtime={device:{paused:true}};h.run('historyRenderStatus(runtime)');assert.match(h.el('history-state').textContent,/已暂停/);
 h.ctx.runtime={revoked:true};h.run('historyRenderStatus(runtime)');assert.match(h.el('history-state').textContent,/访问已撤销/);
 assert.deepEqual(h.ids(),['old']);
});
