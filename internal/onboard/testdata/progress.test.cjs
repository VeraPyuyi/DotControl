const fs=require('fs'),vm=require('vm'),assert=require('assert');
const text=fs.readFileSync('web/app.js','utf8');
const fn=text.match(/function executionLabel\(p\)\{[\s\S]*?\n\}/);
assert(fn,'progress UI function missing');
const box={};vm.createContext(box);vm.runInContext(fn[0],box);
assert.match(box.executionLabel({cancel_registered:true,native_pending:true}),/取消已登记/);
assert.match(box.executionLabel({native_pending:true}),/原生调用未结束/);
assert.match(box.executionLabel({can_observe:true}),/可重新观察/);
assert.doesNotMatch(box.executionLabel({native_pending:true}),/已完成/);
