// Synthetic API fixtures only; no notifications or production data.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
(async () => {
  const output = await fs.mkdtemp(path.join(require('node:os').tmpdir(), 'myprobe-policies-ui-'));
  const browser = await chromium.launch({channel:'msedge',headless:true});
  try {
    for (const theme of ['light','dark']) for (const width of [360,768,1440]) {
      const page = await browser.newPage({viewport:{width,height:1000},colorScheme:theme});
      let policy=null, conflict=false, deletes=0;
      const errors=[];page.on('pageerror',e=>errors.push(e.message));
      await page.route('**/api/**',async route=>{
        const request=route.request(), url=new URL(request.url());let body={},status=200;
        if(url.pathname.includes('/alert-policies')) {
          if(url.pathname.includes('/effective/')) body={decisions:policy?[{policy_key:policy.policy_key,selected_id:'policy',candidate_ids:['policy'],reason:'only_match'}]:[],evaluation_enabled:false};
          else if(request.method()==='POST') {policy={...request.postDataJSON(),id:'policy',revision:1};body={policy,evaluation_enabled:false};status=201;}
          else if(request.method()==='PUT') {if(conflict){status=409;body={error:'配置版本冲突，请重新读取'};}else{policy={...request.postDataJSON(),id:'policy',revision:2};body={policy,evaluation_enabled:false};}}
          else if(request.method()==='DELETE'){deletes++;policy=null;status=204;}
          else if(url.pathname.endsWith('/policy')) body={policy,evaluation_enabled:false};
          else body={policies:policy?[policy]:[],next_cursor:'',evaluation_enabled:false};
        } else if(url.pathname.endsWith('/auth/me')) body={csrf_token:'fixture'};
        else if(url.pathname.endsWith('/settings')||url.pathname.endsWith('/site-settings')) body={settings:{theme_mode:theme}};
        else if(url.pathname.endsWith('/nodes')) body={nodes:[{id:'node',name:'测试节点',tags:[],latency_mode:'ping',sort_order:0,hidden:false,country_code:'',currency:'',billing_cycle:'',use_since_boot:false,custom_badges:[],custom_links:[],collection_seconds:5,report_seconds:5}]};
        else if(url.pathname.endsWith('/notification-channels')) body={channels:[{id:'channel',name:'测试通道',enabled:true,kind:'webhook'}]};
        else if(url.pathname.endsWith('/alert-rules')) body={rules:[]};
        else if(url.pathname.endsWith('/notification-templates')) body={templates:[]};
        else if(url.pathname.endsWith('/alert-events')) body={events:[]};
        else if(url.pathname.endsWith('/latency-config')) body={targets:[],groups:[],group_members:[],node_groups:[],node_targets:[]};
        else if(url.pathname.endsWith('/shares')) body={shares:[]};
        else if(url.pathname.endsWith('/audit')) body={entries:[],next_before_id:0};
        await route.fulfill({status,contentType:'application/json',body:status===204?'':JSON.stringify(body)});
      });
      await page.goto(`${process.env.UI_BASE_URL||'http://127.0.0.1:4173'}/admin`);
      await page.getByRole('button',{name:'告警',exact:true}).click();
      await page.getByRole('tab',{name:'范围策略',exact:true}).click();
      await page.getByText('策略评估尚未启用。',{exact:false}).waitFor();
      await page.getByRole('button',{name:'新建范围策略',exact:true}).click();
      await page.getByLabel('策略名称',{exact:true}).fill('CPU 范围策略');
      await page.getByLabel('策略键',{exact:false}).fill('cpu.warning');
      await page.getByRole('combobox',{name:/^匹配范围/}).selectOption('tags');
      await page.getByLabel('标签（每行一个）',{exact:true}).fill('prod\nlinux');
      await page.getByRole('combobox',{name:/^通知通道/}).selectOption('channel');
      await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,'form overflow');
      await page.screenshot({path:path.join(output,`${theme}-${width}-form.png`),fullPage:true});
      await page.getByRole('button',{name:'保存范围策略',exact:true}).click();
      await page.getByRole('button',{name:'编辑策略',exact:true}).waitFor();
      assert.deepEqual(policy.scope,{kind:'tags',tag_mode:'all',tags:['prod','linux']});
      await page.getByRole('combobox',{name:/^预览节点/}).selectOption('node');
      await page.getByRole('button',{name:'查询继承结果',exact:true}).click();
      await page.getByText('选中 CPU 范围策略 · 唯一匹配',{exact:true}).waitFor();
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,'preview overflow');
      await page.screenshot({path:path.join(output,`${theme}-${width}-preview.png`),fullPage:true});
      await page.getByRole('button',{name:'编辑策略',exact:true}).click();
      assert.equal(await page.getByLabel('标签（每行一个）',{exact:true}).inputValue(),'prod\nlinux');
      await page.getByLabel('策略名称',{exact:true}).fill('未覆盖的修改');conflict=true;
      await page.getByRole('button',{name:'保存范围策略',exact:true}).click();
      await page.getByRole('alert').getByText('配置版本冲突，请重新读取').waitFor();
      assert.equal(await page.getByLabel('策略名称',{exact:true}).inputValue(),'未覆盖的修改');
      await page.getByRole('button',{name:'取消编辑',exact:true}).click();
      await page.getByRole('button',{name:'删除策略',exact:true}).click();
      await page.getByRole('dialog').getByRole('button',{name:'取消',exact:true}).click();assert.equal(deletes,0);
      await page.getByRole('button',{name:'删除策略',exact:true}).click();
      await page.getByRole('button',{name:'确认删除策略',exact:true}).click();
      await page.getByText('暂无范围策略。',{exact:true}).waitFor();assert.equal(deletes,1);assert.deepEqual(errors,[]);
      await page.close();
    }
    console.log(`Policy UI passed six theme/viewport combinations: ${output}`);
  } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
