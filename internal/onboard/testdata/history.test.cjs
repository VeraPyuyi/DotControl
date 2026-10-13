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
 await vm.runInContext('historyStatus(true)',ctx);
 assert.equal(vm.runInContext("historyChosen.has('elsewhere')",ctx),true,'explicit apply can reset selection');
 console.log('History status preserves pending candidates');
})().catch(e=>{console.error(e);process.exitCode=1});
