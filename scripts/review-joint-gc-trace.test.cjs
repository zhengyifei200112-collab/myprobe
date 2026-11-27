'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),os=require('node:os'),path=require('node:path'),cp=require('node:child_process');
const {review}=require('./review-joint-gc-trace.cjs');
const event=(cycle,seconds,live,forced='')=>`gc ${cycle} @${seconds}s 1%: 0.1+0.2+0.1 ms clock, 0.2+0/0.1/0.2+0.2 ms cpu, 10->8->${live} MB, 20 MB goal, 0 MB stacks, 0 MB globals, 2 P${forced}\n`;
function fixture(t,text,change={}){
 const temporaryRoot=fs.realpathSync(os.tmpdir()),dir=fs.mkdtempSync(path.join(temporaryRoot,'myprobe-private-gc-'));fs.chmodSync(dir,0o700);
 t.after(()=>{assert.equal(path.dirname(dir),temporaryRoot);assert.equal(fs.realpathSync(dir),dir);assert(path.basename(dir).startsWith('myprobe-private-gc-'));fs.rmSync(dir,{recursive:true,force:true});});
 const file=path.join(dir,'server-synthetic.log');fs.writeFileSync(file,text,{mode:0o600});
 const terminal={format:'myprobe.joint-process-closeout.v1',closed:true,runtime_gc_trace:true,log_file:path.basename(file),log_limit_bytes:32*1024*1024,captured_log_bytes:Buffer.byteLength(text),discarded_log_bytes:0,log_write_errors:0,server_pid:12345,...change};
 fs.writeFileSync(file+'.closeout.json',JSON.stringify(terminal),{mode:0o600});return file;
}
test('GC review verifies private bytes without copying unrelated output',t=>{
 const file=fixture(t,'private-generated-password=never-export\n'+event(1,'0.010',3)+event(2,'1.200',4,' (forced)'));
 const result=review(file);assert.equal(result.cycles,2);assert.equal(result.forced_cycles,1);assert.deepEqual(result.live_heap_MiB,{first:3,last:4,min:3,max:4});
 assert.equal(result.input_verified,true);assert.equal(result.diagnostic_only,true);assert.equal(result.capacity_accepted,false);assert.equal(result.steady_memory_accepted,false);
 const output=JSON.stringify(result);assert(!output.includes('never-export'));assert(!output.includes(path.dirname(file)));assert.match(result.log_sha256,/^[a-f0-9]{64}$/);
});
for(const [name,text,metadata]of [
 ['truncated',event(1,'0.01',1),{discarded_log_bytes:1}],
 ['write-error',event(1,'0.01',1),{log_write_errors:1}],
 ['not-closed',event(1,'0.01',1),{closed:false}],
 ['disabled',event(1,'0.01',1),{runtime_gc_trace:false}],
 ['wrong-size',event(1,'0.01',1),{captured_log_bytes:1}],
 ['missing-process',event(1,'0.01',1),{server_pid:0}],
 ['missing-first-cycle',event(2,'0.01',1),{}],
 ['cycle-gap',event(1,'0.01',1)+event(3,'1.0',1),{}],
 ['time-backwards',event(1,'1.0',1)+event(2,'0.1',1),{}],
 ['unknown-format','gc private-sentinel-unsupported\n',{}],
 ['partial-line',event(1,'0.01',1).trimEnd(),{}],
 ['no-gc','ordinary private output\n',{}],
])test(`GC review rejects ${name}`,t=>assert.throws(()=>review(fixture(t,text,metadata))));
test('CLI rejection does not echo raw bytes or private paths',t=>{
 const file=fixture(t,'gc private-sentinel-unsupported\n');
 const result=cp.spawnSync(process.execPath,[path.join(__dirname,'review-joint-gc-trace.cjs'),file],{encoding:'utf8'});
 assert.equal(result.status,1);assert(!result.stdout.includes('sentinel'));assert(!result.stderr.includes('sentinel'));assert(!result.stderr.includes(path.dirname(file)));
});
