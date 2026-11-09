const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{const browser=await chromium.launch({headless:true,args:['--no-sandbox']});try{
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(process.argv[2]);await page.waitForFunction(()=>typeof selected!=='undefined'&&selected&&typeof replaying!=='undefined'&&!replaying);
 await page.waitForFunction(()=>document.querySelectorAll('#instances option').length===2&&document.getElementById('instances').textContent.includes('部署检查 A')&&document.getElementById('instances').textContent.includes('网络诊断 B'));
 assert.match(await page.locator('#instance-summary').innerText(),/2 个活动进程/);
 await page.locator('#instances').selectOption(process.argv[3]);await page.waitForFunction(id=>instance===id&&selected==='s1'&&!replaying,process.argv[3]);
 await page.waitForFunction(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes('stdout'));
 assert.equal(await page.locator('#run').isDisabled(),true);
 await page.locator('#instances').selectOption(process.argv[4]);await page.waitForFunction(id=>instance===id&&selected==='s1'&&!replaying,process.argv[4]);
 assert.equal(await page.evaluate(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes('stdout')),false);
 assert.equal(await page.locator('#mode').innerText(),'READ ONLY');assert.deepEqual(errors,[]);
 if(process.env.KEEPSESS_DAEMON_SCREENSHOT)await page.screenshot({path:process.env.KEEPSESS_DAEMON_SCREENSHOT,fullPage:true});
 console.log('shared daemon browser passed: two named active instances/PIDs, same s1 IDs isolated, correct replay, read-only on switch');
 }finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});
