const assert = require('node:assert/strict'), fs = require('node:fs'), vm = require('node:vm');
const nodes = new Map(), nav = [], panels = [];
function node(id) { return {id, hidden:false, dataset:{}, textContent:'', attrs:{}, value:'', setAttribute(k,v){this.attrs[k]=v}, addEventListener(k,v){this[k]=v}}; }
function el(id){if(!nodes.has(id))nodes.set(id,node(id));return nodes.get(id)}
for(const name of ['overview','connection','codex','sharing']){nav.push(Object.assign(node(name),{dataset:{view:name}}));panels.push(Object.assign(node(name),{dataset:{panel:name}}))}
let sharingRefreshes=0;
const ctx={el,document:{querySelectorAll:q=>q==='[data-view]'?nav:panels},location:{hash:'',pathname:'/onboard/'},history:{replaceState(){}},addEventListener(){},cloudOnline:t=>!!t?.ready,cloudLabel:()=> '连接未确认',msg(){},loadConnections:async()=>{},historyStatus:async()=>{},historyRefreshStatus:()=>{sharingRefreshes++},state:null};
vm.createContext(ctx);vm.runInContext(fs.readFileSync(process.argv[2],'utf8'),ctx);
const render=s=>{ctx.payload=s;vm.runInContext('renderConsole(payload)',ctx)};
const base={device:{name:'设备一',version:'0.2-test',paused:false},terminal_only:true,terminal_enabled:true,tunnel_id:'tunnel_test',tunnel:{ready:true},plugin:{devices_seen:false},codex:{capabilities:{codex_terminal:{available:true}}}};
render(base);
assert.equal(panels.find(p=>p.dataset.panel==='overview').hidden,false);
assert.equal(el('status-source').textContent,'等待首次调用','cloud connection must not imply source validation');
vm.runInContext("showView('connection')",ctx);el('device-name').value='尚未保存';
render({...base,device:{...base.device,paused:true}});
assert.equal(panels.find(p=>p.dataset.panel==='connection').hidden,false,'polling must not move selected view');
assert.equal(el('device-name').value,'尚未保存','console status must not replace an edited form');
assert.equal(el('overview-pause').hidden,true);assert.equal(el('overview-resume').hidden,false);
render({...base,revoked:true});assert.equal(el('overview-resume').hidden,true,'revoke cannot look resumable');
render({...base,tunnel_id:'',terminal_enabled:false});assert.equal(el('setup-next').hidden,false);assert.match(el('setup-next').textContent,/连接/);
render(base);assert.equal(el('setup-next').hidden,true);
vm.runInContext("showView('sharing');showView('overview');showView('sharing')",ctx);
assert.equal(sharingRefreshes,2,'returning to sharing must check current policy, not reuse readiness forever');
render({...base,device:{...base.device,paused:true}});
assert.equal(sharingRefreshes,3,'visible sharing must refresh with runtime status');
vm.runInContext("showView('untrusted')",ctx);assert.equal(panels.filter(p=>!p.hidden).length,1);
console.log('Console navigation, truthful status and refresh isolation passed');
