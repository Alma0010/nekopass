// Build web first; requires Playwright on NODE_PATH. Uses only local fixtures.
const {chromium}=require('playwright');
const assert=require('node:assert/strict');
const http=require('node:http');const fs=require('node:fs');const path=require('node:path');
const root=path.resolve(__dirname,'../web/dist');
const server=http.createServer((req,res)=>{
 let name=new URL(req.url,'http://localhost').pathname;if(name==='/admin/nodes/6/protocols')name='/pages/admin_node_protocols.html';
 const file=path.resolve(root,'.'+name);if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){res.writeHead(404);res.end();return}
 res.setHeader('Content-Type',({'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');fs.createReadStream(file).pipe(res);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,...(process.env.NEKOPASS_BROWSER?{executablePath:process.env.NEKOPASS_BROWSER}:{})});
 try{
  let cfg={ingress_enabled:true,allow_direct:true,tunnel_exit_enabled:false,tunnel_protocol:'plain_tcp',tunnel_transport:'raw_tcp',tunnel_security:'none',tunnel_listen_host:'0.0.0.0',tunnel_listen_port:27443,tunnel_public_host:'exit.example.test',allowed_ingress_ids:[],tls:{server_name:'exit.example.test',padding_enabled:false,padding_scheme:[]}};
  const page=await browser.newPage({viewport:{width:1440,height:1000}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/api/v1/**',async route=>{
   const endpoint=new URL(route.request().url()).pathname;
   if(endpoint.endsWith('/protocols')){if(route.request().method()==='PUT')cfg=route.request().postDataJSON();return route.fulfill({json:{node_id:6,node_name:'fixture',config:cfg,ingress_nodes:[]}})}
   if(endpoint.endsWith('/me'))return route.fulfill({json:{id:1,username:'fixture',is_admin:true}});
   return route.fulfill({json:{site_name:'Nekopass'}});
  });
  await page.goto('http://127.0.0.1:'+server.address().port+'/admin/nodes/6/protocols');
  await page.getByTestId('node-connection-fields').waitFor();
  async function select(container,label,value){await container.getByText(label,{exact:true}).locator('..').locator('.el-select').click();await page.getByRole('option',{name:value,exact:true}).click()}
  async function transport(value){await page.getByRole('button',{name:'编辑传输协议',exact:true}).click();const dialog=page.getByRole('dialog',{name:'编辑传输协议',exact:true});await select(dialog,'传输协议',value);await dialog.getByRole('button',{name:'确定',exact:true}).click();await dialog.waitFor({state:'hidden'})}
  async function openSecurity(){await page.getByRole('button',{name:'安全性设置',exact:true}).click();return page.getByRole('dialog',{name:'安全性设置',exact:true})}
  async function closeSecurity(dialog,cancel=false){await dialog.getByRole('button',{name:cancel?'取消':'确定',exact:true}).click();await dialog.waitFor({state:'hidden'})}
  const padding=dialog=>dialog.locator('textarea[data-testid="tls-padding-scheme"], [data-testid="tls-padding-scheme"] textarea');
  let dialog=await openSecurity();assert.equal(await dialog.getByTestId('tls-padding-enabled').isVisible(),false);
  await select(dialog,'安全性','TLS');await dialog.getByTestId('tls-padding-enabled').click();
  const policy='stop=8\n0=30-30\n1=100-400\n2=400-500,c,500-1000';await padding(dialog).fill(policy);await closeSecurity(dialog);
  await page.getByRole('button',{name:'保存并下发',exact:true}).first().click();await page.getByText('入口、出口与传输协议配置已保存并下发',{exact:true}).waitFor();
  assert.equal(cfg.tls.padding_enabled,true);assert.deepEqual(cfg.tls.padding_scheme,policy.split('\n'));
  for(const value of ['h2','raw(udp)']){await transport(value);dialog=await openSecurity();assert.equal(await dialog.getByTestId('tls-padding-enabled').isVisible(),false);await closeSecurity(dialog,true)}
  await transport('raw(tcp)');dialog=await openSecurity();assert.equal(await padding(dialog).inputValue(),policy);
  await select(dialog,'安全性','不加密');assert.equal(await dialog.getByTestId('tls-padding-enabled').isVisible(),false);await select(dialog,'安全性','TLS');assert.equal(await padding(dialog).inputValue(),policy);await closeSecurity(dialog);
  assert.deepEqual(errors,[]);console.log('PASS raw TCP/TLS-only padding controls, AnyTLS line format, save and preserved hidden settings');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
