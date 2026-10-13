const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const nodes=new Map();function el(id){if(!nodes.has(id))nodes.set(id,{value:'',checked:false,children:[],textContent:'',append(...v){this.children.push(...v)},replaceChildren(...v){this.children=v}});return nodes.get(id)}
let policy={revision:2,mode:'allowlist',allowed_thread_ids:['old']};
const ctx={el,document:{createElement:()=>({append(){}})},api:async()=>({policy,capability:{available:true}}),navigator:{clipboard:{writeText:async()=>{}}}};
vm.createContext(ctx);vm.runInContext(fs.readFileSync(process.argv[2],'utf8'),ctx);
(async()=>{
 await vm.runInContext('historyStatus()',ctx);
 assert.equal(vm.runInContext("historyChosen.has('old')",ctx),true);
 vm.runInContext("historyChosen.add('unsaved');historyChosen.delete('old')",ctx);
 policy={revision:3,mode:'allowlist',allowed_thread_ids:['elsewhere']};
 await vm.runInContext('historyStatus()',ctx);
 assert.equal(vm.runInContext("historyChosen.has('unsaved')",ctx),true,'status must preserve pending candidate edits');
 assert.equal(vm.runInContext("historyChosen.has('elsewhere')",ctx),false,'another tab cannot silently replace local selection');
 ctx.runtime={device:{paused:true}};vm.runInContext('historyRenderStatus(runtime)',ctx);
 assert.match(el('history-state').textContent,/已暂停/,'current pause overrides cached ready state');
 ctx.runtime={revoked:true};vm.runInContext('historyRenderStatus(runtime)',ctx);
 assert.match(el('history-state').textContent,/访问已撤销/);
 assert.equal(vm.runInContext("historyChosen.has('unsaved')",ctx),true);
 await vm.runInContext('historyStatus(true)',ctx);
 assert.equal(vm.runInContext("historyChosen.has('elsewhere')",ctx),true,'explicit apply can reset selection');
 const requests=[];ctx.api=()=>new Promise(resolve=>requests.push(resolve));
 const older=vm.runInContext('historyStatus()',ctx),newer=vm.runInContext('historyStatus(true)',ctx);
 requests[1]({policy:{revision:4,mode:'allowlist',allowed_thread_ids:['latest']},capability:{available:true}});await newer;
 requests[0]({policy:{revision:3,mode:'all'},capability:{available:true}});await older;
 assert.equal(vm.runInContext('historyPolicy.revision',ctx),4,'late status response cannot roll back current policy');
 assert.equal(vm.runInContext("historyChosen.has('latest')",ctx),true);
 console.log('History status preserves pending candidates');
})().catch(e=>{console.error(e);process.exitCode=1});
