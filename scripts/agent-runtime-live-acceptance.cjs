// Only disposable localhost data. The real CLI Agent runs with synthetic credentials.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
const { spawn } = require('node:child_process')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const base = process.env.HEALTH_TEST_URL
if (!base || !['127.0.0.1','localhost'].includes(new URL(base).hostname) || !process.env.RUNTIME_TEST_AGENT) throw new Error('Disposable local Server and fixture Agent required')
;(async () => {
  const browser = await chromium.launch({channel:process.env.PLAYWRIGHT_CHANNEL||'msedge',headless:true})
  const context = await browser.newContext()
  let nodeID, csrf, agent, agentDone
  const stopAgent = async () => {
    if(agent) { agent.kill(); await agentDone; agent=undefined }
  }
  const startAgent = token => {
    agent=spawn(process.env.RUNTIME_TEST_AGENT,['--server',base,'--collection-interval','1s','--report-interval','1s'],{windowsHide:true,stdio:'ignore',env:{...process.env,MYPROBE_TOKEN:token}})
    agentDone=new Promise((resolve,reject)=>{agent.once('exit',resolve);agent.once('error',reject)})
  }
  try {
    const page=await context.newPage()
    const errors=[]
    page.on('pageerror',error=>errors.push(error.message))
    await page.goto(base+'/admin')
    await page.getByLabel('密码',{exact:true}).fill(process.env.HEALTH_TEST_PASSWORD)
    const login=page.waitForResponse(response=>response.url().endsWith('/api/v1/auth/login'))
    await page.getByRole('button',{name:'使用密码登录'}).click()
    csrf=(await (await login).json()).csrf_token
    const create=await context.request.post(base+'/api/v1/admin/nodes',{headers:{'X-CSRF-Token':csrf},data:{name:'Runtime acceptance fixture',collection_seconds:2,report_seconds:3}})
    assert.equal(create.status(),201)
    const created=await create.json()
    nodeID=created.node.id
    startAgent(created.agent_token)
    const read=async()=>{
      const response=await context.request.get(base+`/api/v1/admin/nodes/${nodeID}/diagnostics`)
      assert.equal(response.status(),200)
      assert.equal(response.headers()['cache-control'],'private, no-store')
      const bytes=await response.text()
      assert.equal(bytes.includes(created.agent_token),false)
      assert.equal(/connection_epoch|issued_nonce/.test(bytes),false)
      return JSON.parse(bytes)
    }
    const wait=async predicate=>{
      for(let deadline=Date.now()+15000;Date.now()<deadline;) {
        const evidence=await read()
        if(predicate(evidence)) return evidence
        await new Promise(resolve=>setTimeout(resolve,100))
      }
      throw new Error('Runtime evidence did not reach the expected state')
    }
    const first=await wait(value=>value.configuration_acknowledgement.status==='agent_confirmed'&&value.last_report_ingress?.transport==='websocket')
    assert.equal(first.configuration_acknowledgement.desired_revision,1)
    assert.equal(first.configuration_acknowledgement.collection_seconds,2)
    assert.equal(first.configuration_acknowledgement.report_seconds,3)
    assert.equal(first.last_report_ingress.wire_version,2)
    const update=await context.request.patch(base+`/api/v1/admin/nodes/${nodeID}`,{headers:{'X-CSRF-Token':csrf},data:{name:'Runtime acceptance fixture',collection_seconds:1,report_seconds:2,latency_mode:'ping'}})
    assert.equal(update.status(),200)
    await wait(value=>value.configuration_acknowledgement.status==='agent_confirmed'&&value.configuration_acknowledgement.applied_revision===2)
    await page.goto(base+`/admin?tab=health&diagnostic_node=${nodeID}`)
    await page.getByText('runtime-evidence-acceptance',{exact:true}).waitFor()
    await page.getByText('Agent 已确认应用设置',{exact:true}).waitFor()
    await page.getByText('WebSocket / v2',{exact:true}).waitFor()
    const output=process.env.QA_ARTIFACT_DIR ? path.join(process.env.QA_ARTIFACT_DIR,'runtime') : path.join(os.tmpdir(),'myprobe-agent-runtime-live-ui')
    await fs.mkdir(output,{recursive:true})
    for(const theme of ['light','dark']) for(const width of [360,768,1440]) {
      await page.setViewportSize({width,height:1000})
      await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme)
      await page.getByRole('button',{name:'刷新节点诊断',exact:true}).focus()
      await page.keyboard.press('Enter')
      await page.getByText('Agent 已确认应用设置',{exact:true}).waitFor()
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
      await page.getByRole('heading',{name:'节点诊断',exact:true}).scrollIntoViewIfNeeded()
      await page.locator('section.admin-panel').filter({has:page.getByRole('heading',{name:'节点诊断',exact:true})}).screenshot({path:path.join(output,`${theme}-${width}.png`)})
    }
    await stopAgent()
    await wait(value=>!value.websocket_connected&&value.configuration_acknowledgement.status==='confirmed_no_active_connection')
    await page.getByRole('button',{name:'刷新节点诊断',exact:true}).click()
    await page.getByText('历史设置已确认，当前未连接',{exact:true}).waitFor()
    startAgent(created.agent_token)
    await wait(value=>value.websocket_connected&&value.configuration_acknowledgement.status==='agent_confirmed'&&value.configuration_acknowledgement.applied_revision===2)
    await page.reload()
    await page.getByText('Agent 已确认应用设置',{exact:true}).waitFor()
    await stopAgent()
    await context.request.post(base+'/api/v1/auth/logout',{headers:{'X-CSRF-Token':csrf}})
    await page.getByRole('button',{name:'刷新节点诊断',exact:true}).click()
    await page.getByRole('heading',{name:'登录管理中心',exact:true}).waitFor()
    assert.equal(await page.getByText('runtime-evidence-acceptance',{exact:true}).count(),0)
    assert.deepEqual(errors,[])
    console.log(`PASS: real CLI Agent welcome/live revision, persisted WS/v2, offline history, reconnect, six themes/viewports and revoked session. Screenshots: ${output}`)
  } finally {
    await stopAgent()
    // Use a fresh login after revocation to remove only this fixture node.
    if(nodeID) {
      const login=await context.request.post(base+'/api/v1/auth/login',{data:{username:'admin',password:process.env.HEALTH_TEST_PASSWORD}})
      if(login.ok()) { const token=(await login.json()).csrf_token; await context.request.delete(base+`/api/v1/admin/nodes/${nodeID}`,{headers:{'X-CSRF-Token':token}}) }
    }
    await browser.close()
  }
})().catch(error=>{console.error(error);process.exitCode=1})
