// Build web first. Requires Playwright on NODE_PATH; NEKOPASS_BROWSER optionally selects a system Chromium.
// Uses a local-only static server and test fixtures; no running panel or real accounts are accessed.
const assert=require('node:assert/strict');
const http=require('node:http');
const fs=require('node:fs');
const path=require('node:path');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'../web/dist');
const artifacts=path.resolve(process.env.NEKOPASS_E2E_ARTIFACT_DIR||'.local');
fs.mkdirSync(artifacts,{recursive:true});
const server=http.createServer((req,res)=>{
 let name=new URL(req.url,'http://localhost').pathname;
 if(name==='/forward_rules')name='/pages/forward_rules.html';
 if(/^\/admin\/users\/\d+\/forward_rules$/.test(name))name='/pages/admin_user_rules.html';
 const file=path.resolve(root,'.'+name);
 if(!file.startsWith(root+path.sep)||!fs.existsSync(file)||!fs.statSync(file).isFile()){res.writeHead(404);res.end();return}
 const types={'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.css':'text/css','.svg':'image/svg+xml'};
 res.setHeader('Content-Type',types[path.extname(file)]||'application/octet-stream');fs.createReadStream(file).pipe(res);
});

const entries=Array.from({length:6},(_,i)=>({id:10+i,name:'HK4-CVM-'+(i===5?'Ultra':'Pro-'+i),ingress_enabled:true,allow_direct:true,tunnel_exit_enabled:false,tunnel_protocol:'plain_tcp',allowed_ingress_ids:[],public_address:'entry'+i+'.example.test',port_min:24000+i,port_max:24010-i}));
const groups=[{id:42,name:'香港Cloudie入口',node_ids:entries.map(n=>n.id),members:entries.map(n=>({...n,enabled:true,tls_inbound_only:false}))}];
const profile={id:2,username:'fixture@example.test',plan_id:1,plan_name:'fixture',plan_enabled:true,node_ids:entries.map(n=>n.id),node_group_ids:[42],quota_bytes:-1,traffic_bytes:300,max_rules:10,max_connections:0,rule_speed_mbps:0,rule_ip_limit:0,rule_connection_limit:0,rule_count:1,expires_at:null};
const rules=[{id:1,user_id:2,node_id:0,ingress_group_id:42,ingress_group_name:groups[0].name,node_name:groups[0].name,ingress_nodes:entries.map(n=>({node_id:n.id,name:n.name,public_address:n.public_address,status:'active',sync_error:''})),egress_node_id:0,egress_node_name:'',protocol:'tcp',name:'群组规则',listen_port:24005,targets:['target.example.test:443'],group_id:0,group_name:'',balance:'random',proxy_accept:'off',proxy_send:'off',proxy_trusted_cidrs:[],enabled:true,public_address:entries[0].public_address,traffic_bytes:300,status:'active',sync_error:'',speed_mbps:0,ip_limit:0,connection_limit:0}];
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,...(process.env.NEKOPASS_BROWSER?{executablePath:process.env.NEKOPASS_BROWSER}:{})});
 try{
  for(const managed of [false,true]){
   const context=await browser.newContext({viewport:{width:1440,height:1000}});const page=await context.newPage();const calls=[],errors=[];
   page.on('pageerror',e=>errors.push(e.message));
   await page.route('**/api/v1/**',async route=>{
    const req=route.request(),api=new URL(req.url()).pathname.slice('/api/v1/'.length);
    if(req.method()==='POST'||req.method()==='PUT'){calls.push({path:api,body:req.postDataJSON()});return route.fulfill({json:{id:99,ids:[99],ok:true}})}
    let data=[];
    if(api==='me')data={id:managed?1:2,username:managed?'admin':'fixture@example.test',is_admin:managed};
    else if(api==='profile'||api==='admin/users/2')data=[profile];
    else if(api==='rules'||api==='admin/users/2/rules')data=rules;
    else if(api.endsWith('rule-nodes'))data=entries;
    else if(api.endsWith('rule-node-groups'))data=groups;
    else if(api==='site')data={name:'Nekopass',site_name:'Nekopass'};
    await route.fulfill({json:data});
   });
   await page.goto(base+(managed?'/admin/users/2/forward_rules':'/forward_rules'));
   await page.getByText('节点组 · 6 个入口',{exact:true}).waitFor();
   assert.match(await page.locator('.address-link').first().innerText(),/entry5.example.test:24005/);
   const snapshot=await page.locator('.rules-panel').ariaSnapshot();assert.match(snapshot,/香港Cloudie入口/);
   await page.getByRole('button',{name:'添加规则',exact:true}).click();
   let dlg=page.getByRole('dialog',{name:'添加规则',exact:true});await dlg.waitFor();
   assert.equal(await dlg.getByRole('radio',{name:'节点组',exact:true}).isChecked(),true);
   const field=(label)=>dlg.locator('.el-form-item').filter({has:page.locator('.el-form-item__label').getByText(label,{exact:true})});
   await field('入口节点组').locator('.el-select').click();
   await page.getByRole('option',{name:'香港Cloudie入口 · 6 个入口',exact:true}).click();
   await field('名称').locator('input').fill('新群组规则');
   await field('目标地址').locator('textarea').fill('target.example.test:443');
   assert.match(await field('监听端口').innerText(),/24005–24005/);
   if(!managed)await page.screenshot({path:path.join(artifacts,'rule-group-create-desktop.png'),fullPage:true,animations:'disabled'});
   await dlg.getByRole('button',{name:'确定',exact:true}).click();await dlg.waitFor({state:'hidden'});
   assert.equal(calls.at(-1).path,managed?'admin/users/2/rules':'rules');
   assert.equal(calls.at(-1).body.ingress_group_id,42);assert.equal(calls.at(-1).body.node_id,0);assert.equal(calls.at(-1).body.egress_node_id,0);
   await page.getByRole('button',{name:'编辑规则',exact:true}).first().click();
   dlg=page.getByRole('dialog',{name:'编辑规则',exact:true});await dlg.waitFor();
   assert.equal(await dlg.getByRole('radio',{name:'节点组',exact:true}).isChecked(),true);
   assert.match(await dlg.innerText(),/香港Cloudie入口/);
   await dlg.getByRole('button',{name:'确定',exact:true}).click();await dlg.waitFor({state:'hidden'});
   assert.equal(calls.at(-1).body.ingress_group_id,42);assert.equal(calls.at(-1).body.listen_port,24005);
   await page.getByRole('button',{name:'规则详情',exact:true}).first().click();
   dlg=page.getByRole('dialog',{name:'规则详情',exact:true});await dlg.waitFor();
   assert.match(await dlg.innerText(),/HK4-CVM-Ultra/);await dlg.getByRole('button',{name:'Close this dialog',exact:true}).click();
   await page.locator('.rules-table .el-table__body-wrapper tbody tr').first().locator('label.el-checkbox').click();
   await page.getByRole('button',{name:'批量编辑',exact:true}).click();
   dlg=page.getByRole('dialog',{name:'批量编辑 1 条规则',exact:true});await dlg.waitFor();
   await dlg.getByTestId('batch-rule-ingress').click();await page.getByRole('option',{name:'香港Cloudie入口 · 节点组',exact:true}).click();
   await dlg.getByRole('button',{name:'确定',exact:true}).click();await dlg.waitFor({state:'hidden'});
   assert.deepEqual(calls.at(-1).body.patch,{ingress_group_id:42});
   await page.getByRole('button',{name:'批量导入',exact:true}).click();dlg=page.getByRole('dialog',{name:'批量导入规则',exact:true});await dlg.waitFor();
   await dlg.locator('textarea').fill(JSON.stringify({dest:['target.example.test:443'],name:'imported',listen_port:24005}));
   await dlg.getByRole('button',{name:'导入',exact:true}).click();await dlg.waitFor({state:'hidden'});
   assert.equal(calls.at(-1).body.rules.length,1);assert.equal(calls.at(-1).body.rules[0].ingress_group_id,42);assert.equal(calls.at(-1).body.rules[0].node_id,0);
   if(!managed){
    await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'添加规则',exact:true}).click();
    await page.getByRole('dialog',{name:'添加规则',exact:true}).waitFor();await page.getByRole('dialog',{name:'添加规则',exact:true}).locator('.el-radio-button').filter({hasText:'节点组'}).click();await page.screenshot({path:path.join(artifacts,'rule-group-create-mobile.png'),fullPage:true,animations:'disabled'});
   }
   assert.deepEqual(errors,[]);console.log('PASS group browser create, edit, six addresses, details, batch and import; managed='+managed);
   await context.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
})().catch(err=>{console.error(err);server.close();process.exitCode=1});
