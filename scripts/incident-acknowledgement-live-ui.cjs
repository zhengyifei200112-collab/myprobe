// Real embedded frontend, session/CSRF API, SQLite, evaluator/worker and receiver.
const { launchFixtureBrowser } = require('./browser-fixture.cjs');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
(async () => {
  const browser = await launchFixtureBrowser();
  try {
    const context = await browser.newContext({ baseURL: process.env.ACK_BROWSER_URL, timezoneId: 'Asia/Shanghai' });
    await context.addInitScript(() => Object.defineProperty(crypto, 'randomUUID', { value: undefined, configurable: true }));
    const login = await context.request.post('/api/v1/auth/login', { data: { username: 'browser-admin', password: process.env.ACK_BROWSER_PASSWORD } });
    assert.equal(login.status(), 200);
    const csrf = (await login.json()).csrf_token;
    const read = async url => { const r = await context.request.get(url); assert.equal(r.status(), 200); return r.json(); };
    const incidents = (await read('/api/v1/admin/incidents')).incidents;
    const primary = incidents.find(i => i.node_name === 'Ack node'), other = incidents.find(i => i.node_name === 'Other ack node');
    assert.ok(primary && other);
    const ackURL = id => `/api/v1/admin/incidents/${id}/acknowledgement`;
    const key = 'myprobe-incident-ack-pending-v1';
    const page = await context.newPage(), errors = [];
    page.on('pageerror', e => errors.push(e.message));
    const open = async name => {
      await page.goto('/admin');
      await page.getByRole('button', { name: '告警', exact:true }).click();
      await page.getByRole('tab', { name: '故障事件', exact:true }).click();
      await page.getByRole('button', { name:`查看 ${name} 的处理记录`, exact:true }).click();
      await page.getByRole('heading', { name:'知悉与处理记录', exact:true }).waitFor();
      await page.getByText('正在加载知悉记录…',{exact:true}).waitFor({state:'hidden'});
    };
    const section = page.getByRole('region', { name:'事件知悉', exact:true });
    const input = section.getByRole('textbox', {name:/^知悉备注/});
    const submit = section.getByRole('button', {name:'知悉此故障（继续通知）',exact:true});
    const output = await fs.mkdtemp(path.join(require('node:os').tmpdir(), 'myprobe-ack-live-'));
    const screenshots = async stage => {
      for (const theme of ['light','dark']) for (const width of [360,768,1440]) {
        await page.setViewportSize({width,height:1000});
        await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
        await page.evaluate(()=>scrollTo(0,0));
        assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1), false, `${stage} overflow ${theme}/${width}`);
        await page.screenshot({path:path.join(output,`${stage}-${theme}-${width}.png`),fullPage:true});
      }
    };
    await open('Ack node');
    await input.fill('界'.repeat(334));
    assert.equal(await submit.isDisabled(),true,'UTF-8 byte limit');
    const note = '排查中\n<img src=x onerror="window.ackNoteExecuted=1"> private-browser-note';
    await input.fill(note);
    await screenshots('form');
    let original;
    await page.route(`**${ackURL(primary.id)}`, async route => {
      if (route.request().method() !== 'POST') return route.continue();
      original = route.request().postDataJSON();
      const r = await route.fetch(); assert.equal(r.status(),201);
      await route.abort('failed');
    }, {times:1});
    await submit.click();
    await section.getByRole('button',{name:'重试确认知悉结果',exact:true}).waitFor();
    assert.equal(await input.isDisabled(),true);
    const first = (await read(ackURL(primary.id))).acknowledgement;
    assert.equal(first.note,note); assert.equal(first.actor_name,'browser-admin');
    // Reload and select another incident: retry still targets the original one.
    await open('Other ack node');
    assert.equal(await input.isDisabled(),true);
    await section.getByText('这是另一事件的操作；确认结果后才能新建当前事件的知悉记录。',{exact:true}).waitFor();
    const retry = page.waitForRequest(r=>r.method()==='POST' && r.url().endsWith(ackURL(primary.id)));
    await section.getByRole('button',{name:'重试确认知悉结果',exact:true}).click();
    assert.deepEqual((await retry).postDataJSON(),original);
    await section.getByText('已确认 Ack node 的知悉结果；本次操作未修改当前事件。',{exact:true}).waitFor();
    assert.equal(await page.evaluate(key=>sessionStorage.getItem(key),key),null);
    assert.equal((await read(ackURL(other.id))).acknowledgement,null,'retry must not acknowledge selected other incident');
    assert.equal(await input.isDisabled(),false);
    await page.getByRole('button',{name:'查看 Ack node 的处理记录',exact:true}).click();
    await section.locator('.ack-note').waitFor();
    assert.equal(await section.locator('.ack-note').textContent(),note);
    assert.equal(await section.locator('.ack-note img').count(),0);
    assert.equal(await page.evaluate(()=>window.ackNoteExecuted),undefined);
    await screenshots('record');
    // Poll actual delivery state without replacing the API.
    for (let n=0;n<100;n++) {
      const jobs=(await read(`/api/v1/admin/incidents/${primary.id}/deliveries`)).deliveries;
      if (jobs.length===1 && jobs[0].status==='delivered') break;
      if(n===99) throw new Error('acknowledgement suppressed first real notification');
      await page.waitForTimeout(20);
    }
    const clear = async () => {
      await section.getByText('核对后处理本地知悉记录',{exact:true}).click();
      await section.getByLabel('我已核对事件知悉与审计记录',{exact:true}).check();
      await section.getByRole('button',{name:'清除本地待确认知悉记录',exact:true}).click();
    };
    for (const raw of ['{',JSON.stringify({version:1,incidentID:other.id,nodeName:null,requestID:'broken',note:''}),JSON.stringify({version:1,incidentID:other.id,nodeName:'Other ack node',requestID:'broken',note:'x'.repeat(1001)})]) {
      await page.evaluate(([key,raw])=>sessionStorage.setItem(key,raw),[key,raw]);
      await open('Other ack node');
      await section.getByText('无法读取待确认知悉记录。',{exact:false}).waitFor();
      assert.equal(await input.isDisabled(),true);
      assert.equal(await page.evaluate(key=>sessionStorage.getItem(key),key),raw);
      assert.equal((await read(ackURL(other.id))).acknowledgement,null);
      await clear(); assert.equal(await input.isDisabled(),false);
    }
    await page.evaluate(()=>{
      window.ackSetItem=Storage.prototype.setItem;
      Storage.prototype.setItem=function(key,value){if(key==='myprobe-incident-ack-pending-v1') throw new Error('fixture ack storage unavailable'); return window.ackSetItem.call(this,key,value);};
    });
    await input.fill('blocked write'); await submit.click();
    await section.getByText('fixture ack storage unavailable',{exact:true}).waitFor();
    assert.equal((await read(ackURL(other.id))).acknowledgement,null);
    await page.evaluate(()=>{Storage.prototype.setItem=window.ackSetItem;delete window.ackSetItem;});
    // Another operator wins between the displayed form and this user's post.
    const external=await context.request.post(ackURL(other.id),{headers:{'X-CSRF-Token':csrf},data:{request_id:'external-winner',note:'已有人接手'}});
    assert.equal(external.status(),201);
    await input.fill('我的新备注'); await submit.click();
    await section.getByText('事件已被知悉；您的新备注未另行保存，请查看原记录。',{exact:true}).waitFor();
    assert.equal(await section.locator('.ack-note').textContent(),'已有人接手');
    assert.equal(await page.evaluate(key=>sessionStorage.getItem(key),key),null);
    // A conflicting original identity retains the uncertain operation.
    const conflict={version:1,incidentID:primary.id,nodeName:'Ack node',requestID:original.request_id,note:'changed identity'};
    const conflictRaw=JSON.stringify(conflict);
    await page.evaluate(([key,raw])=>sessionStorage.setItem(key,raw),[key,conflictRaw]); await open('Ack node');
    await section.getByRole('button',{name:'重试确认知悉结果',exact:true}).click();
    await section.getByText('原请求身份存在冲突，请核对知悉记录和审计；不要建立新的请求。',{exact:true}).waitFor();
    assert.equal(await page.evaluate(key=>sessionStorage.getItem(key),key),conflictRaw);
    assert.equal((await read(ackURL(primary.id))).acknowledgement.note,note); await clear();
    // Read failure has a usable retry and cannot display a successful new action.
    await page.route(`**${ackURL(primary.id)}`,route=>route.fulfill({status:503,contentType:'application/json',body:'{"error":"fixture acknowledgement read unavailable"}'}),{times:1});
    await section.getByRole('button',{name:'刷新知悉记录',exact:true}).click();
    await section.getByText('fixture acknowledgement read unavailable',{exact:true}).waitFor();
    await section.getByRole('button',{name:'刷新知悉记录',exact:true}).click(); await section.locator('.ack-note').waitFor();
    // Actual admin configuration change is observed by the real evaluator/worker.
    const changed=await context.request.patch(`/api/v1/admin/nodes/${primary.node_id}`,{headers:{'X-CSRF-Token':csrf},data:{name:'Ack node',expires_at:new Date(Date.now()+72*3600_000).toISOString(),collection_seconds:5,report_seconds:5,latency_mode:'ping',tags:[]}});
    assert.equal(changed.status(),200);
    for(let n=0;n<100;n++) {
      const view=await read(ackURL(primary.id)), jobs=(await read(`/api/v1/admin/incidents/${primary.id}/deliveries`)).deliveries;
      if(view.incident_state==='resolved' && jobs.length===2 && jobs.every(j=>j.status==='delivered')) break;
      if(n===99) throw new Error('acknowledgement stopped real recovery notification');
      await page.waitForTimeout(20);
    }
    await section.getByRole('button',{name:'刷新知悉记录',exact:true}).click();
    await section.getByText('事件已恢复或结束，首次知悉记录继续保留。',{exact:true}).waitFor();
    const final=(await read(ackURL(primary.id))).acknowledgement;
    assert.deepEqual(final,first,'recovery preserves original acknowledgement');
    assert.deepEqual(errors,[],'browser exceptions');
    console.log('ACK_BROWSER_OK',JSON.stringify({screenshots:output,themes:2,widths:[360,768,1440],lostResponse:true,retryOtherSelected:true,corruptRecords:3,blockedStorage:true,concurrentWinner:true,identityConflict:true,realNotifications:2}));
  } finally { await browser.close(); }
})().catch(e=>{console.error(e);process.exit(1);});
