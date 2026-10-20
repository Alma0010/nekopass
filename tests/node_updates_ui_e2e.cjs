// Isolated local fixtures only: no running panel, real node or account is modified.
const {chromium}=require('playwright');const assert=require('node:assert/strict');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../web/dist'),artifacts=process.env.NEKOPASS_E2E_ARTIFACT_DIR;
const server=http.createServer((req,res)=>{
 const names={'/admin/nodes':'admin_nodes','/admin/settings':'admin_settings'};
 const uri=new URL(req.url,'http://localhost').pathname,file=path.resolve(root,'.'+(names[uri]?'/pages/'+names[uri]+'.html':uri));
 if(!file.startsWith(root+path.sep)||!fs.existsSync(file)||!fs.statSync(file).isFile()){res.writeHead(404);res.end();return}
 res.setHeader('Content-Type',({'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');fs.createReadStream(file).pipe(res);
});
const fixture=(id,patch={})=>({id,name:'node-'+id,enabled:true,online:true,group_ids:[],ingress_enabled:true,tunnel_exit_enabled:false,tunnel_security:'none',active_connections:0,applied_revision:1,config_revision:1,sync_error:'',tls:{},protocol_version:25,update_supported:true,agent_version:'v0.19.4',update_status:null,...patch});
const initial=()=>[fixture(1),fixture(2,{online:false}),fixture(3,{update_supported:false}),fixture(4,{update_status:{state:'queued'}}),fixture(5,{agent_version:'v0.19.5'}),fixture(6,{agent_version:'v0.20.0'}),fixture(7),fixture(8),fixture(9,{update_status:{state:'running'}}),fixture(10,{protocol_version:8}),fixture(11,{agent_version:'dev'})];
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,...(process.env.NEKOPASS_BROWSER?{executablePath:process.env.NEKOPASS_BROWSER}:{})});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:900}});await page.emulateMedia({reducedMotion:'reduce'});
  let nodes=initial(),latestError=false,denied=false,latestReads=0;const writes=[],settingsWrites=[],errors=[];
  let settings={site_name:'Nekopass',panel_url:'https://panel.example.test',agent_host:'agent.example.test',agent_port:443,agent_transport:'tls',registration_enabled:false,registration_email_verification:false,force_invite:false,referral_enabled:false,referral_mode:'first',referral_rate:'15',captcha_mode:'off',smtp:{host:'',port:587,security:'starttls',username:'',password:'',from:'',from_name:''},telegram:{enabled:false,token:'',admin_ids:[],notification_chat_ids:[],statistics_days:7},proxy_trusted_cidrs:[],target_deny_cidrs:[],agent_version:'latest'};
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/api/v1/**',async route=>{
   const request=route.request(),uri=new URL(request.url()).pathname;
   if(uri==='/api/v1/me')return route.fulfill({json:{id:1,username:'fixture',is_admin:true}});
   if(uri==='/api/v1/admin/nodes')return route.fulfill({json:nodes});
   if(uri==='/api/v1/admin/node-groups')return route.fulfill({json:[]});
   if(uri==='/api/v1/admin/updates'){latestReads++;return latestError?route.fulfill({status:502,json:{error:'fixture release unavailable'}}):route.fulfill({json:{latest:{version:'v0.19.5'}}})}
   if(request.method()==='POST'&&/^\/api\/v1\/admin\/nodes\/\d+\/update$/.test(uri)){
    const id=Number(uri.split('/').at(-2));writes.push({id,body:request.postDataJSON()});await new Promise(resolve=>setTimeout(resolve,60));
    if(id===7)return route.fulfill({status:denied?403:500,json:{error:denied?'fixture permission changed':'fixture submit failure'}});
    nodes=nodes.map(n=>n.id===id?{...n,update_status:{state:'queued',version:'v0.19.5'}}:n);
    return route.fulfill({json:{state:'queued',generation:1,version:'v0.19.5'}});
   }
   if(uri==='/api/v1/admin/settings'){
    if(request.method()==='PUT'){settings=request.postDataJSON();settingsWrites.push(settings);return route.fulfill({json:{ok:true}})}
    return route.fulfill({json:{settings,api_key_configured:false,agent_listener:null,telegram_status:{state:'disabled'}}});
   }
   if(uri==='/api/v1/admin/updates/panel')return route.fulfill({json:{supported:true,current_version:'v0.19.4',task:null}});
   return route.fulfill({json:{site_name:settings.site_name}});
  });
  const base='http://127.0.0.1:'+server.address().port;
  await page.goto(base+'/admin/nodes');await page.getByRole('row').filter({hasText:'node-11'}).waitFor();
  assert.equal(await page.getByRole('button',{name:'检查更新',exact:true}).count(),0);
  assert.equal(await page.locator('.brand-logo').evaluate(el=>getComputedStyle(el).filter),'none');
  if(artifacts){fs.mkdirSync(artifacts,{recursive:true});await page.screenshot({path:path.join(artifacts,'nodes-color-logo.png')})}
  const all=()=>page.getByRole('button',{name:/^更新所有节点/});
  const confirm=()=>page.getByRole('dialog',{name:'更新所有节点',exact:true});
  await all().click();await confirm().getByRole('button',{name:'取消',exact:true}).click();await confirm().waitFor({state:'hidden'});assert.equal(writes.length,0);
  await all().click();await confirm().getByRole('button',{name:'更新 4 个节点',exact:true}).click();
  await page.waitForFunction(()=>document.querySelectorAll('.el-button.is-loading').length>0);
  const result=()=>page.getByRole('dialog',{name:'节点批量更新结果',exact:true});await result().waitFor();
  assert.deepEqual(writes.map(w=>w.id),[1,7,8,11]);assert.equal(latestReads,2);
  for(const w of writes)assert.deepEqual(w.body,{version:'v0.19.5'});
  assert.match(await result().innerText(),/已下发 3 个，提交失败 1 个/);
  assert.match(await result().innerText(),/fixture submit failure/);assert.match(await result().innerText(),/已有更新任务 2 个/);
  if(artifacts)await page.screenshot({path:path.join(artifacts,'bulk-update-results.png')});
  await result().getByRole('button',{name:'关闭',exact:true}).click();await result().waitFor({state:'hidden'});
  nodes=nodes.map(n=>({...n,online:true,update_supported:true,protocol_version:25,agent_version:'v0.19.5',update_status:null}));
  await all().click();await page.getByText(/没有需要更新的节点/).waitFor();assert.equal(writes.length,4);assert.equal(await confirm().count(),0);
  latestError=true;await all().click();await page.getByText('fixture release unavailable',{exact:true}).waitFor();assert.equal(writes.length,4);
  latestError=false;nodes=initial();denied=true;await page.reload();await page.getByRole('row').filter({hasText:'node-11'}).waitFor();
  await all().click();await confirm().getByRole('button',{name:'更新 4 个节点',exact:true}).click();await result().waitFor();
  assert.deepEqual(writes.slice(4).map(w=>w.id),[1,7]);assert.match(await result().innerText(),/剩余 2 个节点未提交/);
  await result().getByRole('button',{name:'关闭',exact:true}).click();await result().waitFor({state:'hidden'});
  denied=false;nodes=[fixture(1)];await page.reload();await page.getByRole('row').filter({hasText:'node-1'}).getByRole('button',{name:'更新',exact:true}).click();
  const single=page.getByRole('dialog',{name:'更新节点',exact:true});await single.getByRole('button',{name:'更新',exact:true}).click();await single.waitFor({state:'hidden'});
  await page.getByText('更新任务已下发',{exact:true}).waitFor();assert.equal(writes.at(-1).id,1);
  console.log('PASS bulk cancel, eligible-only submissions, one release lookup, partial failure, no-op, lookup failure, permission stop and individual update');
  await page.goto(base+'/admin/settings');await page.getByRole('button',{name:'保存设置',exact:true}).waitFor();
  for(const name of ['站点与连接','用户注册','邮件服务','邀请返利','Telegram','安全与 API','节点安装','软件更新']){
   await page.getByRole('tab',{name,exact:true}).click();assert.equal(await page.getByRole('button',{name:'保存设置',exact:true}).count(),1);
  }
  await page.getByRole('tab',{name:'站点与连接',exact:true}).click();
  await page.getByLabel('站点名称',{exact:true}).fill('Fixture edited');
  await page.getByRole('tab',{name:'用户注册',exact:true}).click();await page.getByRole('switch',{name:'开放注册',exact:true}).locator('..').locator('.el-switch__core').click();assert.equal(await page.getByRole('switch',{name:'开放注册',exact:true}).isChecked(),true);
  await page.getByRole('button',{name:'保存设置',exact:true}).click();await page.getByText('系统设置已保存',{exact:true}).waitFor();
  assert.equal(settingsWrites.length,1);assert.equal(settingsWrites[0].site_name,'Fixture edited');assert.equal(settingsWrites[0].registration_enabled,true);
  assert.equal(await page.locator('.settings-footer').count(),0);
  if(artifacts)await page.screenshot({path:path.join(artifacts,'settings-one-save.png')});
  assert.deepEqual(errors,[]);console.log('PASS original color logo, one save button across all settings tabs, and cross-tab save payload');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
