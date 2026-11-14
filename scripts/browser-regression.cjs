// Isolated browser regression. No deployment accounts, databases or external services.
const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),crypto=require('node:crypto'),net=require('node:net'),{spawn}=require('node:child_process');
const {chromium}=require(process.env.QA_PLAYWRIGHT_MODULE || '../.browser-qa/node_modules/playwright');
const root=path.resolve(__dirname,'..'),config=JSON.parse(fs.readFileSync(path.join(__dirname,'browser-config.json'),'utf8'));
const pause=ms=>new Promise(r=>setTimeout(r,ms));
(async()=>{let browser,child;const data=fs.mkdtempSync(path.join(os.tmpdir(),'app-browser-qa-'));try{
 const port=await new Promise(resolve=>{const server=net.createServer();server.listen(0,'127.0.0.1',()=>{const p=server.address().port;server.close(()=>resolve(p))})});
 const password=crypto.randomBytes(24).toString('hex'),base='http://127.0.0.1:'+port;
 child=spawn(path.resolve(root,process.env.QA_BINARY || config.binary),[],{cwd:root,stdio:'ignore',env:{...process.env,DATA_DIR:data,WEB_DIR:path.join(root,'frontend/dist'),USER:'fixtureadmin',PASSWORD:password,ADMIN_USERNAME:'fixtureadmin',ADMIN_PASSWORD:password,LISTEN_ADDR:'127.0.0.1:'+port,PORT:String(port)}});
 let ready=false;for(let i=0;i<150;i++){try{if((await fetch(base+config.health)).ok){ready=true;break}}catch{}await pause(100)}if(!ready)throw Error('Isolated service did not start');
 browser=await chromium.launch({headless:true,channel:process.env.QA_BROWSER_CHANNEL || undefined});
 const context=await browser.newContext(),page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.name));
 await page.goto(base+config.route,{waitUntil:'networkidle'});
 for(const width of [375,834,1440]){await page.setViewportSize({width,height:1000});if(!await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1))throw Error('Horizontal overflow '+width)}
 if(config.kind!=='relay'){
  const form=page.locator(config.form);
  await form.locator('input').nth(0).fill('fixtureadmin');await form.locator('input[type=password]').fill(password);
  await form.getByRole('button').last().click();
  await page.waitForFunction(()=>!document.querySelector('input[type=password]')?.getClientRects().length);
  if(config.kind==='movie'){
   await page.route('**/api/auth/logout',route=>route.fulfill({status:503,contentType:'application/json',body:'{"detail":"fixture outage"}'}));
   await page.getByRole('button',{name:'退出登录',exact:true}).click();
   await page.getByRole('alert').waitFor();
   if(await page.getByRole('heading',{name:'管理员登录',exact:true}).isVisible())throw Error('Failed logout reported success');
  }
 }
 if(errors.length)throw Error('Page runtime errors');
 const denied=await browser.newContext();await denied.addInitScript(()=>{for(const name of ['localStorage','sessionStorage'])Object.defineProperty(window,name,{get(){throw new DOMException('denied','SecurityError')}})});
 const restricted=await denied.newPage();const deniedErrors=[];restricted.on('pageerror',e=>deniedErrors.push(e.name));await restricted.goto(base+config.route,{waitUntil:'networkidle'});
 if(deniedErrors.length||!(await restricted.locator('body').innerText()).trim())throw Error('Blocked storage broke startup');
 console.log(JSON.stringify({project:config.kind,responsiveWidths:[375,834,1440],blockedStorage:true,login:config.kind!=='relay',logoutFailure:config.kind==='movie'}));
}finally{if(browser)await browser.close();if(child){child.kill();await Promise.race([new Promise(r=>child.once('exit',r)),pause(2000)])}
 if(path.dirname(fs.realpathSync(data))!==fs.realpathSync(os.tmpdir()))throw Error('Unexpected temporary fixture path');fs.rmSync(data,{recursive:true,force:true});
}})().catch(e=>{console.error(e.message);process.exitCode=1});
