const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const nodes=new Map();function node(){return {children:[],dataset:{},addEventListener(){},replaceChildren(){this.children=[]},append(...x){this.children.push(...x)},value:''}}
const get=id=>{if(!nodes.has(id))nodes.set(id,node());return nodes.get(id)};
const buttons=['permissions','permissions_screen','permissions_activity','resume','confirm_image'].map(action=>Object.assign(node(),{dataset:{action}}));
const sandbox={document:{getElementById:get,createElement:node,querySelectorAll:q=>q.includes('^=permissions')?buttons.slice(0,3):buttons},location:{hash:'',pathname:'/onboard/'},history:{replaceState(){}},fetch:async()=>({ok:false,status:401,json:async()=>({error:'expired'})}),setInterval(){},setTimeout(){},navigator:{clipboard:{writeText:async()=>{}}}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(process.argv[2],'utf8'),sandbox);
sandbox.payload={terminal_only:true,terminal_enabled:true,device:{os:'windows',name:'test',paused:false,activity:{available:false,locked:true}},permissions:{state:'unknown'},tunnel:{ready:true},plugin:{devices_seen:true},codex:{capabilities:{codex_terminal:{state:'ready',available:true}}}};
vm.runInContext('renderState(payload)',sandbox);
const text=id=>get(id).children.map(n=>n.textContent).join(' ');
assert.doesNotMatch(text('checks'),/等待输入监控|等待驱动/);
assert.match(text('checks'),/本模式不需要/);
assert.doesNotMatch(text('validation'),/等待源 Dot 观察|尚未确认源 Dot 实际显示/);
assert.match(buttons[3].textContent,/终端/);assert.equal(buttons[4].hidden,true);
sandbox.payload.terminal_only=false;sandbox.payload.device.os='darwin';vm.runInContext('renderState(payload)',sandbox);
assert.match(text('checks'),/等待输入监控/);assert.equal(buttons[0].hidden,false);assert.equal(buttons[4].hidden,false);
console.log('Terminal and legacy onboarding checks passed');
