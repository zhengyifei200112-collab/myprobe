// Embedded UI + real session/CSRF API, SQLite, evaluator/worker and local receiver.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
(async () => {
  const browser = await chromium.launch({channel:'msedge',headless:true});
  try {
    const context = await browser.newContext({baseURL:process.env.SILENCE_BROWSER_URL,timezoneId:'Asia/Shanghai'});
    await context.addInitScript(() => Object.defineProperty(crypto,'randomUUID',{value:undefined,configurable:true}));
    const login=await context.request.post('/api/v1/auth/login',{data:{username:'browser-admin',password:process.env.SILENCE_BROWSER_PASSWORD}});
    assert.equal(login.status(),200);const csrf=(await login.json()).csrf_token;
    const read=async url=>{const r=await context.request.get(url);assert.equal(r.status(),200);return r.json();};
    const post=async(url,data)=>{const r=await context.request.post(url,{headers:{'X-CSRF-Token':csrf},data});return r;};
    const items=(await read('/api/v1/admin/incidents')).incidents;
    const primary=items.find(i=>i.node_name==='Silence node'),other=items.find(i=>i.node_name==='Other silence node');assert.ok(primary&&other);
    const url=id=>`/api/v1/admin/incidents/${id}/silences`,ack=id=>`/api/v1/admin/incidents/${id}/acknowledgement`;
    const jobs=id=>read(`/api/v1/admin/incidents/${id}/deliveries`);
    const key='myprobe-incident-silence-pending-v1',page=await context.newPage(),errors=[];
    page.on('pageerror',e=>errors.push(e.message));
    const section=page.getByRole('region',{name:'事件静默',exact:true});
    const open=async name=>{
      await page.goto('/admin');await page.getByRole('button',{name:'告警',exact:true}).click();
      await page.getByRole('tab',{name:'故障事件',exact:true}).click();
      await page.getByRole('button',{name:`查看 ${name} 的处理记录`,exact:true}).click();
      await section.getByRole('heading',{name:'限时静默',exact:true}).waitFor();
      await section.getByText('正在加载静默记录…',{exact:true}).waitFor({state:'hidden'});
      await section.getByRole('button',{name:'刷新静默记录',exact:true}).waitFor();
    };
    const input=section.getByRole('textbox',{name:/^静默原因/}),submit=section.getByRole('button',{name:'仅设置静默',exact:true});
    const output=await fs.mkdtemp(path.join(require('node:os').tmpdir(),'myprobe-silence-live-'));
    const screenshots=async stage=>{
      for(const theme of ['light','dark'])for(const width of [360,768,1440]){
        await page.setViewportSize({width,height:1000});await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
        await page.evaluate(()=>scrollTo(0,0));assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,`${stage} overflow ${theme}/${width}`);
        await page.screenshot({path:path.join(output,`${stage}-${theme}-${width}.png`),fullPage:true});
      }
    };
    await open('Silence node');
    await section.getByRole('spinbutton',{name:/^静默时长/}).fill('1');
    await input.fill('界'.repeat(334));assert.equal(await submit.isDisabled(),true,'reason byte limit');
    const note='排查中\n<img src=x onerror="window.silenceExecuted=1"> private-silence-note';await input.fill(note);
    await screenshots('form');
    let original;
    await page.route(`**${url(primary.id)}`,async route=>{
      original=route.request().postDataJSON();const r=await route.fetch();assert.equal(r.status(),201);await route.abort('failed');
    },{times:1});
    await section.getByRole('button',{name:'知悉并静默',exact:true}).click();
    await section.getByRole('button',{name:'重试确认静默结果',exact:true}).waitFor();
    const first=await read(url(primary.id));assert.equal(first.active_silence.reason,note);
    assert.equal(first.active_silence.creator_name,'browser-admin');assert.equal((await read(ack(primary.id))).acknowledgement.note,note);
    const ends=first.active_silence.ends_at;
    await open('Other silence node');assert.equal(await input.isDisabled(),true);
    await section.getByText('这是另一事件的操作；重试只处理原事件。',{exact:true}).waitFor();
    const retry=page.waitForRequest(r=>r.method()==='POST'&&r.url().endsWith(url(primary.id)));
    await section.getByRole('button',{name:'重试确认静默结果',exact:true}).click();assert.deepEqual((await retry).postDataJSON(),original);
    await section.getByText('已确认 Silence node 的静默结果；当前事件未被修改。',{exact:true}).waitFor();
    assert.equal(await page.evaluate(k=>sessionStorage.getItem(k),key),null);
    assert.equal((await read(url(primary.id))).active_silence.ends_at,ends);
    assert.equal((await read(url(other.id))).silences.length,0);assert.equal((await read(ack(other.id))).acknowledgement,null);
    await open('Silence node');
    await section.locator('summary').filter({hasText:'此事件的静默历史'}).click();
    assert.equal(await section.locator('.silence-reason').textContent(),note);assert.equal(await section.locator('.silence-reason img').count(),0);
    assert.equal(await page.evaluate(()=>window.silenceExecuted),undefined);
    const primaryJobs=await jobs(primary.id);assert.equal(primaryJobs.deliveries.length,1);const old=primaryJobs.deliveries[0];
    assert.equal(old.status,'canceled');assert.equal(old.error_class,'maintenance_and_silence');assert.equal(old.attempt_count,0);
    await page.getByText('此通知关联的事件静默',{exact:true}).click();
    await page.getByRole('region',{name:'通知静默关联',exact:true}).locator('.silence-reason').waitFor();
    const linked=await read(`/api/v1/admin/notification-deliveries/${old.id}/silences`);
    assert.equal(linked.silences.length,1);assert.equal(linked.silences[0].id,first.active_silence.id);assert.ok(linked.silences[0].recorded_at);
    const maint=await read(`/api/v1/admin/notification-deliveries/${old.id}/maintenance`);assert.equal(maint.windows.length,1);
    await screenshots('active');
    const cancelURL=`${url(primary.id)}/${first.active_silence.id}/cancel`;let originalCancel;
    await page.route(`**${cancelURL}`,async route=>{originalCancel=route.request().postDataJSON();const r=await route.fetch();assert.equal(r.status(),200);await route.abort('failed');},{times:1});
    await section.getByRole('button',{name:'取消此事件静默',exact:true}).click();
    await section.getByRole('button',{name:'重试确认静默结果',exact:true}).waitFor();
    const canceled=await read(url(primary.id));assert.equal(canceled.active_silence,null);assert.ok(canceled.silences[0].canceled_at);
    await open('Other silence node');
    const cancelRetry=page.waitForRequest(r=>r.method()==='POST'&&r.url().endsWith(cancelURL));
    await section.getByRole('button',{name:'重试确认静默结果',exact:true}).click();assert.deepEqual((await cancelRetry).postDataJSON(),originalCancel);
    await section.getByText('已确认 Silence node 的静默结果；当前事件未被修改。',{exact:true}).waitFor();
    assert.equal((await read(url(primary.id))).silences[0].canceled_at,canceled.silences[0].canceled_at);
    assert.equal((await jobs(primary.id)).deliveries.length,1,'maintenance must still suppress after silence cancellation');
    const clear=async()=>{await section.getByText('核对后处理本地静默记录',{exact:true}).click();await section.getByLabel('我已核对静默与审计记录',{exact:true}).check();await section.getByRole('button',{name:'清除本地待确认静默记录',exact:true}).click();};
    for(const raw of ['{',JSON.stringify({version:1,incidentID:other.id,nodeName:'Other silence node',requestID:'broken',operation:'cancel',silenceID:'valid',revision:0}),JSON.stringify({version:1,incidentID:other.id,nodeName:'Other silence node',requestID:'broken',operation:'create',params:{duration_seconds:60,reason:'x'.repeat(1001),acknowledge:false}})]){
      await page.evaluate(([k,v])=>sessionStorage.setItem(k,v),[key,raw]);await open('Other silence node');
      await section.getByText('无法读取待确认静默记录。',{exact:false}).waitFor();assert.equal(await input.isDisabled(),true);
      assert.equal(await page.evaluate(k=>sessionStorage.getItem(k),key),raw);await clear();assert.equal(await input.isDisabled(),false);
    }
    await page.evaluate(()=>{window.silenceSetItem=Storage.prototype.setItem;Storage.prototype.setItem=function(k,v){if(k==='myprobe-incident-silence-pending-v1')throw new Error('fixture silence storage unavailable');return window.silenceSetItem.call(this,k,v);};});
    await input.fill('blocked write');await submit.click();await section.getByText('fixture silence storage unavailable',{exact:true}).waitFor();
    assert.equal((await read(url(other.id))).silences.length,0);
    await page.evaluate(()=>{Storage.prototype.setItem=window.silenceSetItem;delete window.silenceSetItem;});
    const external=await post(url(other.id),{request_id:'external-silence',duration_seconds:60,reason:'已有静默',acknowledge:false});assert.equal(external.status(),201);
    await input.fill('新原因');await submit.click();
    await section.getByText('已有生效静默，请查看原记录，取消或到期后再设置。',{exact:true}).waitFor();
    assert.equal((await read(url(other.id))).silences.length,1);assert.equal((await read(ack(other.id))).acknowledgement,null,'standalone silence does not acknowledge');
    const conflictRaw=JSON.stringify({version:1,incidentID:primary.id,nodeName:'Silence node',requestID:original.request_id,operation:'create',params:{...original,reason:'changed identity'}});
    await page.evaluate(([k,v])=>sessionStorage.setItem(k,v),[key,conflictRaw]);await open('Silence node');
    await section.getByRole('button',{name:'重试确认静默结果',exact:true}).click();
    await section.getByText('原请求身份存在冲突，请核对静默及审计记录，保留原请求。',{exact:true}).waitFor();
    assert.equal(await page.evaluate(k=>sessionStorage.getItem(k),key),conflictRaw);await clear();
    await page.route(`**${url(primary.id)}?**`,route=>route.fulfill({status:503,contentType:'application/json',body:'{"error":"fixture silence read unavailable"}'}),{times:1});
    await section.getByRole('button',{name:'刷新静默记录',exact:true}).click();await section.getByText('fixture silence read unavailable',{exact:true}).waitFor();
    await section.getByRole('button',{name:'刷新静默记录',exact:true}).click();await submit.waitFor();
    const cancelMaint=await post(`/api/v1/admin/maintenance-windows/${process.env.SILENCE_BROWSER_MAINTENANCE}/cancel`,{revision:1});assert.equal(cancelMaint.status(),200);
    for(let n=0;n<100;n++){
      const current=(await jobs(primary.id)).deliveries;
      if(current.length===2&&current.some(j=>j.status==='delivered'))break;
      if(n===99)throw new Error('final suppression did not resume current summary');await page.waitForTimeout(25);
    }
    const summary=(await jobs(primary.id)).deliveries.find(j=>j.status==='delivered');
    assert.equal((await read(`/api/v1/admin/notification-deliveries/${summary.id}/silences`)).silences.length,0);
    const changed=await context.request.patch(`/api/v1/admin/nodes/${primary.node_id}`,{headers:{'X-CSRF-Token':csrf},data:{name:'Silence node',expires_at:new Date(Date.now()+72*3600_000).toISOString(),collection_seconds:5,report_seconds:5,latency_mode:'ping',tags:[]}});assert.equal(changed.status(),200);
    for(let n=0;n<100;n++){
      const current=(await jobs(primary.id)).deliveries;if((await read(url(primary.id))).incident_state==='resolved'&&current.length===3&&current.filter(j=>j.status==='delivered').length===2)break;
      if(n===99)throw new Error('missing real recovery notification');await page.waitForTimeout(25);
    }
    await section.getByRole('button',{name:'刷新静默记录',exact:true}).click();await section.getByText('事件已恢复或结束，不能新建静默；历史继续保留。',{exact:true}).waitFor();
    assert.equal((await read(url(primary.id))).silences[0].ends_at,ends);
    assert.equal((await read(ack(primary.id))).acknowledgement.note,note);assert.deepEqual(errors,[]);
    console.log('SILENCE_BROWSER_OK',JSON.stringify({screenshots:output,themes:2,widths:[360,768,1440],lostCreate:true,lostCancel:true,retryOtherSelected:true,corruptRecords:3,blockedStorage:true,activeConflict:true,identityConflict:true,maintenanceOrSilence:true,realNotifications:2}));
  }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exit(1);});
