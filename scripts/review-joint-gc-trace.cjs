'use strict';
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const LIMIT=32*1024*1024;
const need=(condition,message)=>{if(!condition)throw new Error(message);};
const sha=raw=>crypto.createHash('sha256').update(raw).digest('hex');

function privateFile(file,limit){
 let info,raw,after;
 try{info=fs.lstatSync(file);need(info.isFile()&&!info.isSymbolicLink()&&info.size<=limit,'invalid private input');
  need(process.platform==='win32'||(info.mode&0o077)===0,'input permissions are not private');
  raw=fs.readFileSync(file);after=fs.lstatSync(file);
 }catch{throw new Error('private input unavailable or invalid');}
 need(info.dev===after.dev&&info.ino===after.ino&&info.size===raw.length&&after.size===raw.length&&info.mtimeMs===after.mtimeMs,'input changed during review');
 return raw;
}

function review(file){
 const raw=privateFile(file,LIMIT),terminalRaw=privateFile(file+'.closeout.json',8192);
 let terminal;try{terminal=JSON.parse(terminalRaw);}catch{throw new Error('invalid closeout metadata');}
 need(terminal.format==='myprobe.joint-process-closeout.v1'&&terminal.closed===true&&terminal.runtime_gc_trace===true,'missing completed GC trace mode');
 need(terminal.log_file===path.basename(file)&&terminal.log_limit_bytes===LIMIT&&terminal.captured_log_bytes===raw.length,'log bytes or identity do not match the terminal record');
 need(terminal.discarded_log_bytes===0&&terminal.log_write_errors===0,'GC evidence is truncated or has write errors');
 need(Number.isSafeInteger(terminal.server_pid)&&terminal.server_pid>0,'actual process identity is unavailable');
 need(raw.length>0&&raw[raw.length-1]===10,'log ends with an incomplete line');
 const rows=[];
 const pattern=/^gc ([0-9]+) @([0-9]+(?:\.[0-9]+)?)s .* ms clock, .* ms cpu, ([0-9]+)->([0-9]+)->([0-9]+) MB, ([0-9]+) MB goal, ([0-9]+) MB stacks, ([0-9]+) MB globals, ([0-9]+) P( \(forced\))?$/;
 for(const line of raw.toString('utf8').split(/\r?\n/)){
  if(!/^gc(?:\s|$)/.test(line))continue;
  const match=pattern.exec(line);need(match,'unsupported GC trace format');
  const values=match.slice(1,10).map(Number);
  need(values.every(Number.isFinite)&&values.filter((_,i)=>i!==1).every(Number.isSafeInteger),'invalid GC counters');
  const [cycle,seconds,beforeMiB,afterMiB,liveMiB,goalMiB,stacksMiB,globalsMiB,processors]=values;
  need(cycle===(rows.at(-1)?.cycle??0)+1&&seconds>=0&&seconds>=(rows.at(-1)?.seconds??0)&&processors>0,'GC sequence or time is incomplete');
  rows.push({cycle,seconds,beforeMiB,afterMiB,liveMiB,goalMiB,stacksMiB,globalsMiB,processors,forced:!!match[10]});
 }
 need(rows.length>0,'no GC events were captured');
 const lives=rows.map(row=>row.liveMiB);
 return {format:'myprobe.joint-gc-review.v1',input_verified:true,diagnostic_only:true,capacity_accepted:false,steady_memory_accepted:false,
  log_sha256:sha(raw),closeout_sha256:sha(terminalRaw),log_bytes:raw.length,server_pid:terminal.server_pid,
  cycles:rows.length,first_runtime_seconds:rows[0].seconds,last_runtime_seconds:rows.at(-1).seconds,
  observed_span_seconds:rows.at(-1).seconds-rows[0].seconds,forced_cycles:rows.filter(row=>row.forced).length,
  live_heap_MiB:{first:lives[0],last:lives.at(-1),min:lives.reduce((a,b)=>Math.min(a,b)),max:lives.reduce((a,b)=>Math.max(a,b))},
  limitations:['Go trace heap values have MiB granularity and are not RSS','Intervals without GC have no fresh live-heap observation','Match this process to external PID/start-clock/executable evidence; do not concatenate processes','This review does not establish full-window coverage or memory stability']};
}

module.exports={review};
if(require.main===module){
 try{need(process.argv.length===3,'usage: node review-joint-gc-trace.cjs <private process log>');console.log(JSON.stringify(review(process.argv[2])));}
 catch(error){console.error(error instanceof Error?error.message:'GC trace review rejected');process.exitCode=1;}
}
