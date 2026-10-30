// Local built frontend with API and clipboard fixtures; never accesses a live panel.
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
 res.setHeader('Content-Type',({'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');fs.createReadStream(file).pipe(res);
});
const nodes=[10,11].map(id=>({id,name:'entry '+id,ingress_enabled:true,enabled:true,allow_direct:true,tunnel_exit_enabled:false,allowed_ingress_ids:[],public_address:'entry'+id+'.example.test',port_min:24000,port_max:25000}));
const groups=[{id:42,name:'SNI group',node_ids:[10,11],members:nodes}];
const inbounds=nodes.flatMap(n=>[{id:n.id+100,node_id:n.id,port:24443,name:'shared '+n.id,allowed_sni:['example.test']},...(n.id===10?[{id:999,node_id:10,port:24444,name:'not common',allowed_sni:[]}]:[])]);
const profile={id:2,username:'fixture@example.test',plan_id:1,plan_name:'fixture',plan_enabled:true,node_ids:[10,11],node_group_ids:[42],quota_bytes:-1,traffic_bytes:0,max_rules:20,rule_count:3};
const ordinaryRules=['random','round_robin','least_connections'].map((balance,i)=>({id:i+1,user_id:2,node_id:0,ingress_group_id:42,ingress_group_name:'SNI group',node_name:'SNI group',ingress_nodes:nodes.map(n=>({node_id:n.id,name:n.name,public_address:n.public_address,status:'active',sync_error:''})),egress_node_id:0,protocol:'tcp',name:'policy '+i,listen_port:24001+i,targets:['example.test:443'],balance,group_id:0,proxy_accept:'off',proxy_send:'off',proxy_trusted_cidrs:[],enabled:true,status:'active',traffic_bytes:0}));
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,...(process.env.NEKOPASS_BROWSER?{executablePath:process.env.NEKOPASS_BROWSER}:{})});
 try{
  for(const managed of [false,true]){
   let rules=ordinaryRules.map(r=>({...r}));const calls=[],errors=[];
   const context=await browser.newContext({viewport:{width:1440,height:1000}});
   await context.addInitScript(()=>{
    window.fixtureCopies=[];window.fixtureCopyFailure=false;
    Object.defineProperty(navigator,'clipboard',{value:{writeText:async value=>{if(window.fixtureCopyFailure)throw Error('fixture denied');window.fixtureCopies.push(value)}}});
    const original=document.execCommand.bind(document);document.execCommand=(name,...args)=>window.fixtureCopyFailure&&name==='copy'?false:original(name,...args);
   });
   const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
   await page.route('**/api/v1/**',async route=>{
    const req=route.request(),api=new URL(req.url()).pathname.slice('/api/v1/'.length);
    if(['POST','PUT'].includes(req.method())){calls.push({path:api,body:req.postDataJSON()});return route.fulfill({json:{id:99,ok:true}})}
    let data=[];
    if(api==='me')data={id:managed?1:2,username:'fixture',is_admin:managed};
    else if(api==='profile'||api==='admin/users/2')data=[profile];
    else if(api==='rules'||api==='admin/users/2/rules')data=rules;
    else if(api.endsWith('rule-nodes'))data=nodes;
    else if(api.endsWith('rule-node-groups'))data=groups;
    else if(api.endsWith('tls-inbounds'))data=inbounds;
    else if(api==='site')data={name:'Nekopass',site_name:'Nekopass'};
    await route.fulfill({json:data});
   });
   await page.goto(base+(managed?'/admin/users/2/forward_rules':'/forward_rules'));
   await page.getByRole('button',{name:'批量导出',exact:true}).click();
   let dlg=page.getByRole('dialog',{name:'批量导出规则',exact:true});await dlg.waitFor();
   assert.equal(await dlg.getByRole('radio',{name:'Nekopass 完整备份',exact:true}).isChecked(),true);
   await dlg.locator('label.el-radio').filter({hasText:'nyanpass 兼容格式'}).click();
   await dlg.getByRole('button',{name:'复制到剪贴板',exact:true}).click();
   await page.waitForFunction(()=>window.fixtureCopies.length===1);
   const copied=await page.evaluate(()=>window.fixtureCopies.at(-1)),rows=copied.trim().split('\n').map(JSON.parse);
   assert.deepEqual(rows.map(r=>r.dest_policy),[undefined,'round','least_load']);assert.equal(Object.hasOwn(rows[0],'dest_policy'),false);
   assert.equal(rows.every(r=>!Object.hasOwn(r,'ingress_group_id')),true);
   const downloadPromise=page.waitForEvent('download');await dlg.getByRole('button',{name:'导出',exact:true}).click();
   const download=await downloadPromise,stream=await download.createReadStream(),chunks=[];for await(const chunk of stream)chunks.push(chunk);
   assert.equal(download.suggestedFilename(),'nyanpass-rules.jsonl');assert.equal(Buffer.concat(chunks).toString(),copied);
   await dlg.waitFor({state:'hidden'});
   await page.getByRole('button',{name:'批量导出',exact:true}).click();await dlg.waitFor();
   await page.evaluate(()=>{window.fixtureCopyFailure=true});
   await dlg.getByRole('button',{name:'复制到剪贴板',exact:true}).click();
   await page.getByText('复制失败，请手动选择复制',{exact:true}).waitFor();assert.equal(await dlg.isVisible(),true);
   await page.evaluate(()=>{window.fixtureCopyFailure=false});
   await dlg.getByRole('button',{name:'取消',exact:true}).click();await dlg.waitFor({state:'hidden'});
   await page.getByRole('button',{name:'添加规则',exact:true}).click();
   dlg=page.getByRole('dialog',{name:'添加规则',exact:true});await dlg.waitFor();
   const field=label=>dlg.locator('.el-form-item').filter({has:page.locator('.el-form-item__label').getByText(label,{exact:true})});
   await field('入口节点组').locator('.el-select').click();await page.getByRole('option',{name:'SNI group · 2 个入口',exact:true}).click();
   await field('入站方式').locator('.el-select').click();await page.getByRole('option',{name:'共享 TLS 入站',exact:true}).click();
   await field('共享 TLS 入口').locator('.el-select').click();
   assert.equal(await page.getByRole('option',{name:'节点组共享入口 · 24443',exact:true}).count(),1);
   assert.equal(await page.getByRole('option',{name:/24444/}).count(),0);
   await page.getByRole('option',{name:'节点组共享入口 · 24443',exact:true}).click();
   await field('名称').locator('input').fill('group SNI');await field('SNI 域名').locator('textarea').fill('app.example.test');await field('目标地址').locator('textarea').fill('example.test:443');
   const colors=await dlg.locator('.el-radio-button.is-active .el-radio-button__inner').evaluate(el=>{const s=getComputedStyle(el);return [s.color,s.backgroundColor]});
   assert.notEqual(colors[0],colors[1],'selected ingress type must remain readable');
   if(!managed)await page.screenshot({path:path.join(artifacts,'group-sni-create.png'),fullPage:true,animations:'disabled'});
   await dlg.getByRole('button',{name:'确定',exact:true}).click();await dlg.waitFor({state:'hidden'});
   const input=calls.at(-1);assert.equal(input.path,managed?'admin/users/2/rules':'rules');assert.equal(input.body.tls_inbound_port,24443);assert.equal(input.body.tls_inbound_id,0);assert.equal(input.body.node_id,0);assert.equal(input.body.ingress_group_id,42);assert.equal(input.body.listen_port,0);
   rules=[{...ordinaryRules[0],...input.body,id:99,listen_port:24443}];await page.reload();
   await page.getByRole('button',{name:'编辑规则',exact:true}).click();dlg=page.getByRole('dialog',{name:'编辑规则',exact:true});await dlg.waitFor();assert.equal(await field('SNI 域名').locator('textarea').inputValue(),'app.example.test');
   await dlg.getByRole('button',{name:'确定',exact:true}).click();await dlg.waitFor({state:'hidden'});assert.equal(calls.at(-1).body.tls_inbound_port,24443);
   await page.getByRole('button',{name:'批量导出',exact:true}).click();dlg=page.getByRole('dialog',{name:'批量导出规则',exact:true});await dlg.waitFor();
   await dlg.getByRole('button',{name:'复制到剪贴板',exact:true}).click();await page.waitForFunction(()=>window.fixtureCopies.length===1);
   const backup=JSON.parse(await page.evaluate(()=>window.fixtureCopies.at(-1)));assert.equal(backup.rules[0].tls_inbound_port,24443);assert.equal(backup.rules[0].ingress_group_id,42);
   assert.deepEqual(errors,[]);console.log('PASS export policies, clipboard/download parity, copy failure, common SNI port and native round-trip; managed='+managed);
   await context.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
})().catch(err=>{console.error(err);server.close();process.exitCode=1});
