const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const elements = new Map();
function element() { return {dataset:{},children:[],value:'',append(...v){this.children.push(...v)},replaceChildren(){this.children=[]},add(v){this.children.push(v)},querySelectorAll(){return this.children.map(x=>x.children[0])}}; }
const sandbox = {el:id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)}, document:{createElement:element},Option:function(text,value){this.text=text;this.value=value},bindConnectionButton(){}};
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(process.argv[2],'utf8'),sandbox);
function render(prompt){sandbox.prompt=prompt;vm.runInContext('showLocalReview({job_id:"job"},{approval_id:"approval"},prompt)',sandbox);return elements.get('review-fields').querySelectorAll();}
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
