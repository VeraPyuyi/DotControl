const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(process.argv[2],'utf8').split('function msg(')[0];
const response=(status,body)=>({ok:status>=200&&status<300,status,json:async()=>body});
function client(fetch){const sandbox={document:{getElementById(){}},fetch};vm.createContext(sandbox);vm.runInContext(source,sandbox);vm.runInContext("csrf='old'",sandbox);return (path,data={})=>{sandbox.path=path;sandbox.data=data;return vm.runInContext('api(path,data)',sandbox)}}
async function run(){
 const calls=[];const api=client(async(url,opt)=>{calls.push({url,body:opt.body,csrf:opt.headers['X-DotControl-CSRF']});return url.endsWith('/session')?response(200,{csrf:'new'}):opt.headers['X-DotControl-CSRF']==='old'?response(403,{error:'session_rejected'}):response(200,{saved:true})});
 assert.equal((await api('history',{operation:'apply',preview_id:'chosen',revision:2})).saved,true);
 assert.equal(calls.length,3);assert.match(calls[1].url,/\/session$/);assert.equal(calls[0].body,calls[2].body);assert.equal(calls[2].csrf,'new');
 let count=0;await assert.rejects(client(async()=>{count++;throw Error('network interrupted')})('history',{operation:'apply'}),/network interrupted/);assert.equal(count,1,'uncertain mutations must never replay');
 count=0;await assert.rejects(client(async()=>{count++;return response(400,{error:'history_revision_conflict'})})('history',{operation:'apply'}));assert.equal(count,1,'business errors must not replay');
 count=0;await assert.rejects(client(async()=>{count++;return count===1?response(403,{error:'session_rejected'}):response(401,{error:'launch_expired_open_dotcontrol'})})('history',{operation:'apply'}));assert.equal(count,2,'expired authentication must stop before mutation retry');
 let restores=0;const concurrent=client(async(url,opt)=>{if(url.endsWith('/session')){restores++;await new Promise(resolve=>setImmediate(resolve));return response(200,{csrf:'new'})}return opt.headers['X-DotControl-CSRF']==='old'?response(403,{error:'session_rejected'}):response(200,{ok:true})});
 await Promise.all([concurrent('status'),concurrent('history',{operation:'status'})]);assert.equal(restores,1);
 console.log('Session recovery, bounded retry and uncertain-mutation protection passed');
}
run().catch(error=>{console.error(error);process.exitCode=1});
