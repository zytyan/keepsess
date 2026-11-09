const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 try {
  const page=await browser.newPage({viewport:{width:1400,height:1000}});const errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>{window.sent=[];const orig=WebSocket.prototype.send;WebSocket.prototype.send=function(data){window.sent.push(JSON.parse(data));return orig.call(this,data)}});
  await page.goto(process.argv[2]);await page.waitForSelector('.session');await page.waitForFunction(()=>document.getElementById('connection').textContent.includes('实时连接'));
  assert.equal(await page.locator('#scrollback').inputValue(),'5000');
  assert.equal(await page.evaluate('terminal.options.scrollback'),5000);
  assert.match(await page.locator('#sessions').innerText(),/s1 · production/);assert.match(await page.locator('#sessions').innerText(),/s2 · closed-session/);
  await page.waitForFunction(()=>terminal.buffer.active.getLine(0)?.translateToString().includes('$ both'));
  const text=await page.evaluate(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n'));
  assert.match(text,/stdout/);assert.match(text,/stderr/);
  assert.equal(await page.locator('#run').isDisabled(),true);
  await page.locator('#scrollback').fill('7500');await page.locator('#apply').click();assert.equal(await page.evaluate('terminal.options.scrollback'),7500);
  await page.reload();await page.waitForFunction(()=>document.getElementById('connection').textContent.includes('实时连接'));assert.equal(await page.locator('#scrollback').inputValue(),'7500');assert.equal(await page.evaluate('terminal.options.scrollback'),7500);
  await page.locator('#unlock').click();await page.waitForFunction(()=>document.getElementById('mode').textContent==='INPUT ENABLED');
  // Terminal status queries are render data and must never become remote input.
  await page.evaluate(()=>new Promise(resolve=>terminal.write('\x1b[6n',resolve)));await page.waitForTimeout(100);
  assert.equal(await page.evaluate(()=>window.sent.filter(m=>m.type==='input').length),0);
  await page.locator('#command').fill('exit7');await page.locator('#run').click();await page.waitForFunction(()=>document.getElementById('tasks').textContent.includes('(7)'));
  assert.equal(await page.locator('#run').isDisabled(),true); // task switch restores readonly
  await page.locator('.session').filter({hasText:'s2 · closed-session'}).click();await page.waitForFunction(()=>document.getElementById('title').textContent==='s2 · closed-session');assert.equal(await page.locator('#unlock').isDisabled(),true);
  await page.locator('.session').filter({hasText:'s3 · console'}).click();await page.waitForFunction(()=>document.getElementById('connection').textContent.includes('实时连接'));
  await page.evaluate(async()=>{const orig=Terminal.prototype.write;Terminal.prototype.write=function(data,callback){return orig.call(this,data,()=>setTimeout(()=>callback?.(),75));};try{const previous=selectSession('s1');await new Promise(r=>setTimeout(r,25));await selectSession('s3');await previous;}finally{Terminal.prototype.write=orig;}});
  await page.waitForFunction(()=>document.getElementById('connection').textContent.includes('实时连接'));
  await page.locator('#unlock').click();await page.waitForFunction(()=>document.getElementById('mode').textContent==='INPUT ENABLED');
  await page.locator('.xterm-helper-textarea').focus();await page.keyboard.type('show version');await page.keyboard.press('Enter');
  await page.waitForFunction(()=>!operationPending&&!inputQueue.length);
  assert.equal(await page.evaluate(()=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join('')),'show version\r');
  // This minimal SSH simulator has no PTY line discipline; LF explicitly
  // completes its line reader after verifying raw Enter was sent as CR.
  await page.keyboard.press('Control+j');
  await page.waitForFunction(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes('SwitchOS 1.0'));
  const sentText=await page.evaluate(()=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join(''));assert.equal(sentText,'show version\r\n');
  await page.keyboard.insertText('中文');await page.keyboard.press('Control+j');await page.waitForFunction(()=>document.getElementById('receipts').textContent.includes('中文'));await page.waitForFunction(()=>!operationPending&&!inputQueue.length);assert.equal(await page.evaluate(()=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join('')),'show version\r\n中文\n');
  await page.screenshot({path:process.env.KEEPSESS_SCREENSHOT||'/tmp/keepsess-web.png',fullPage:true});
  await page.locator('#instances').selectOption('archived');
  await page.waitForFunction(()=>document.getElementById('title').textContent==='s1 · archived-console');
  await page.waitForFunction(()=>terminal.buffer.active.getLine(0)?.translateToString().includes('archive retained'));
  await page.waitForFunction(()=>terminal.buffer.active.getLine(1)?.translateToString().includes('中文'));
  assert.equal(await page.locator('#unlock').isDisabled(),true);assert.match(await page.locator('#subtitle').innerText(),/已关闭/);
  assert.deepEqual(errors,[]);console.log('browser acceptance passed: scrollback, persistence, readonly, replay, exec, stream input, nested path, archive replay, no parser replies');
 } finally {await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
