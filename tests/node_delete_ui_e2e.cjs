// Uses fixture APIs only; no real node or account is modified.
const {chromium}=require('playwright');const assert=require('node:assert/strict');
const http=require('node:http');const fs=require('node:fs');const path=require('node:path');
const root=path.resolve(__dirname,'../web/dist');
const server=http.createServer((req,res)=>{
 let name=new URL(req.url,'http://localhost').pathname;if(name==='/admin/nodes')name='/pages/admin_nodes.html';
 const file=path.resolve(root,'.'+name);if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){res.writeHead(404);res.end();return}
 res.setHeader('Content-Type',({'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');fs.createReadStream(file).pipe(res);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,...(process.env.NEKOPASS_BROWSER?{executablePath:process.env.NEKOPASS_BROWSER}:{})});
 try{
  const fixture=(id,name,patch={})=>({id,name,enabled:false,online:false,group_ids:[],ingress_enabled:true,tunnel_exit_enabled:false,tunnel_security:'none',active_connections:0,applied_revision:1,config_revision:10,sync_error:'',tls:{},update_supported:false,...patch});
  let nodes=[
   fixture(9,'历史节点'),fixture(10,'未使用节点'),fixture(11,'仍被规则使用'),
   fixture(12,'批量可卸载',{bound:true,uninstall_supported:true}),
   fixture(13,'批量需手动',{bound:true,uninstall_supported:false}),
  ];const deletes=[],errors=[];
  const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/api/v1/**',async route=>{
   const req=route.request(),url=new URL(req.url());
   if(req.method()==='DELETE'){
    deletes.push(url.pathname+url.search);const id=Number(url.pathname.split('/').at(-1));
    if(id===11)return route.fulfill({status:409,json:{error:'此节点仍被转发规则用作入口或出口，请先迁移或删除相关规则'}});
    if(id===9&&url.searchParams.get('force')!=='true')return route.fulfill({status:409,json:{error:'存在历史用量，请再次确认',code:'node_delete_confirmation_required'}});
    if(id===12&&url.searchParams.get('record_only')!=='true'){
     nodes=nodes.map(n=>n.id===12?{...n,uninstall_status:{state:'queued'}}:n);
     return route.fulfill({json:{uninstall_state:'queued'}});
    }
    if(id===13&&url.searchParams.get('record_only')!=='true')return route.fulfill({status:409,json:{error:'需要手动卸载',code:'node_uninstall_manual_required'}});
    nodes=nodes.filter(n=>n.id!==id);return route.fulfill({json:{ok:true}});
   }
   if(url.pathname.endsWith('/me'))return route.fulfill({json:{id:1,username:'fixture',is_admin:true}});
   if(url.pathname==='/api/v1/admin/nodes')return route.fulfill({json:nodes});
   if(url.pathname==='/api/v1/admin/node-groups')return route.fulfill({json:[]});
   return route.fulfill({json:{site_name:'Nekopass'}});
  });
  await page.goto('http://127.0.0.1:'+server.address().port+'/admin/nodes');
  const row=name=>page.getByRole('row').filter({hasText:name});
  async function start(name){await row(name).getByRole('button',{name:'卸载',exact:true}).click();const dialog=page.getByRole('dialog',{name:'确认卸载',exact:true});await dialog.getByRole('button',{name:'卸载',exact:true}).click()}
  await start('历史节点');let confirm=page.getByRole('dialog',{name:'确认历史用量',exact:true});
  await confirm.waitFor();assert.match(await confirm.innerText(),/历史账本仍保留/);assert.match(await confirm.innerText(),/额度不会因删除返还/);
  await confirm.getByRole('button',{name:'取消',exact:true}).click();await confirm.waitFor({state:'hidden'});
  assert.equal(deletes.length,1);assert.equal(nodes.some(n=>n.id===9),true);
  await start('历史节点');confirm=page.getByRole('dialog',{name:'确认历史用量',exact:true});
  await confirm.getByRole('button',{name:'确认继续',exact:true}).click();await row('历史节点').waitFor({state:'hidden'});
  assert.equal(deletes.length,3);assert.equal(deletes[2],'/api/v1/admin/nodes/9?force=true');
  await start('未使用节点');await row('未使用节点').waitFor({state:'hidden'});assert.equal(deletes.length,4);assert.equal(deletes[3],'/api/v1/admin/nodes/10');
  await start('仍被规则使用');await page.getByText('此节点仍被转发规则用作入口或出口，请先迁移或删除相关规则',{exact:true}).waitFor();
  assert.equal(await page.getByRole('dialog',{name:'确认历史用量',exact:true}).count(),0);assert.equal(deletes.length,5);assert.equal(deletes.filter(v=>v.includes('force=true')).length,1);
  const first=page.getByRole('checkbox',{name:'选择 批量可卸载',exact:true});
  const second=page.getByRole('checkbox',{name:'选择 批量需手动',exact:true});
  await page.getByRole('checkbox',{name:'选择 仍被规则使用',exact:true}).click();
  await second.click({modifiers:['Shift']});
  assert.equal(await first.isChecked(),true);
  await page.getByRole('textbox',{name:'搜索节点',exact:true}).fill('批量');
  await row('仍被规则使用').waitFor({state:'hidden'});
  assert.equal(await first.isChecked(),false);
  assert.equal(await second.isChecked(),false);
  const all=page.getByRole('checkbox',{name:'全选当前筛选结果',exact:true});
  await all.click();
  assert.equal(await first.isChecked(),true);
  assert.equal(await second.isChecked(),true);
  await all.click();
  const origin=await first.boundingBox();
  const end=await second.boundingBox();
  await page.mouse.move(origin.x+origin.width/2,origin.y+origin.height/2);
  await page.mouse.down();
  await page.mouse.move(end.x+end.width/2,end.y+end.height/2,{steps:8});
  await page.mouse.up();
  assert.equal(await first.isChecked(),true);
  assert.equal(await second.isChecked(),true);
  await page.getByRole('button',{name:/^批量操作/}).click();
  await page.getByRole('menuitem',{name:'批量卸载',exact:true}).click();
  await page.getByRole('dialog',{name:'批量卸载节点',exact:true}).getByRole('button',{name:'卸载 2 个节点',exact:true}).click();
  const result=page.getByRole('dialog',{name:'节点批量卸载结果',exact:true});
  await result.waitFor();
  assert.match(await result.innerText(),/等待卸载/);
  assert.match(await result.innerText(),/需手动卸载/);
  assert.deepEqual(deletes.slice(5),['/api/v1/admin/nodes/12','/api/v1/admin/nodes/13']);
  await result.getByRole('button',{name:'关闭',exact:true}).click();
  await result.waitFor({state:'hidden'});
  assert.match(await row('批量可卸载').innerText(),/等待卸载/);
  await row('批量需手动').getByRole('button',{name:'仅删除面板记录',exact:true}).click();
  const record=page.getByRole('dialog',{name:'仅删除面板记录',exact:true});
  await record.getByRole('button',{name:'仅删除面板记录',exact:true}).click();
  await row('批量需手动').waitFor({state:'hidden'});
  assert.equal(deletes.at(-1),'/api/v1/admin/nodes/13?record_only=true');
  assert.deepEqual(errors,[]);
  console.log('PASS confirmation for history, ordinary deletion, rule dependency protection, filtered bulk uninstall, queued row and explicit record-only fallback');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
