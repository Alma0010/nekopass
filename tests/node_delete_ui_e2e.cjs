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
  const fixture=(id,name)=>({id,name,enabled:false,online:false,group_ids:[],ingress_enabled:true,tunnel_exit_enabled:false,tunnel_security:'none',active_connections:0,applied_revision:1,config_revision:10,sync_error:'',tls:{},update_supported:false});
  let nodes=[fixture(9,'历史节点'),fixture(10,'未使用节点'),fixture(11,'仍被规则使用')];const deletes=[],errors=[];
  const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/api/v1/**',async route=>{
   const req=route.request(),url=new URL(req.url());
   if(req.method()==='DELETE'){
    deletes.push(url.pathname+url.search);const id=Number(url.pathname.split('/').at(-1));
    if(id===11)return route.fulfill({status:409,json:{error:'此节点仍被转发规则用作入口或出口，请先迁移或删除相关规则'}});
    if(id===9&&url.searchParams.get('force')!=='true')return route.fulfill({status:409,json:{error:'存在历史用量，请再次确认',code:'node_delete_confirmation_required'}});
    nodes=nodes.filter(n=>n.id!==id);return route.fulfill({json:{ok:true}});
   }
   if(url.pathname.endsWith('/me'))return route.fulfill({json:{id:1,username:'fixture',is_admin:true}});
   if(url.pathname==='/api/v1/admin/nodes')return route.fulfill({json:nodes});
   if(url.pathname==='/api/v1/admin/node-groups')return route.fulfill({json:[]});
   return route.fulfill({json:{site_name:'Nekopass'}});
  });
  await page.goto('http://127.0.0.1:'+server.address().port+'/admin/nodes');
  async function start(name){await page.getByRole('row').filter({hasText:name}).getByRole('button',{name:'删除',exact:true}).click();const dialog=page.getByRole('dialog',{name:'确认删除',exact:true});await dialog.getByRole('button',{name:'删除',exact:true}).click()}
  await start('历史节点');let confirm=page.getByRole('dialog',{name:'强制删除节点',exact:true});
  await confirm.waitFor();assert.match(await confirm.innerText(),/历史账本仍保留/);assert.match(await confirm.innerText(),/额度不会因删除返还/);
  await confirm.getByRole('button',{name:'取消',exact:true}).click();await confirm.waitFor({state:'hidden'});
  assert.equal(deletes.length,1);assert.equal(nodes.some(n=>n.id===9),true);
  await start('历史节点');confirm=page.getByRole('dialog',{name:'强制删除节点',exact:true});
  await confirm.getByRole('button',{name:'确认强制删除',exact:true}).click();await page.getByRole('row').filter({hasText:'历史节点'}).waitFor({state:'hidden'});
  assert.equal(deletes.length,3);assert.equal(deletes[2],'/api/v1/admin/nodes/9?force=true');
  await start('未使用节点');await page.getByRole('row').filter({hasText:'未使用节点'}).waitFor({state:'hidden'});assert.equal(deletes.length,4);assert.equal(deletes[3],'/api/v1/admin/nodes/10');
  await start('仍被规则使用');await page.getByText('此节点仍被转发规则用作入口或出口，请先迁移或删除相关规则',{exact:true}).waitFor();
  assert.equal(await page.getByRole('dialog',{name:'强制删除节点',exact:true}).count(),0);assert.equal(deletes.length,5);assert.equal(deletes.filter(v=>v.includes('force=true')).length,1);
  assert.deepEqual(errors,[]);console.log('PASS force-delete second confirmation, cancel without force, ordinary deletion, and rule dependency protection');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
