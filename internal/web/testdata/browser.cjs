const {launch}=require('./launch.cjs');
const assert = require('node:assert/strict');
const verifyTypography = require('./typography.cjs');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try {
  const context=await browser.newContext({viewport:{width:1400,height:1000},ignoreHTTPSErrors:process.env.KEEPSESS_BROWSER_INSECURE_TLS_TEST==='1'});let page=await context.newPage();const errors=[];
  page.on('pageerror',e=>{errors.push(e.message);console.error('Page error:',e.message);});
  await context.addInitScript(()=>{window.sent=[];const orig=WebSocket.prototype.send;WebSocket.prototype.send=function(data){window.sent.push(JSON.parse(data));return orig.call(this,data)}});
  await page.goto(process.argv[2]);await page.waitForSelector('.session');await page.waitForFunction(()=>document.getElementById('connection').textContent.includes('实时连接')).catch(async e=>{console.error(await page.evaluate(()=>({connection:document.getElementById('connection').textContent,warning:document.getElementById('warning').textContent,status:document.getElementById('status').textContent,selected,instance,current,replaying,renderer:terminal?.keepsessRenderer})));throw e;});
  await verifyTypography(page);
  await require('./context-menu.cjs')(page);
  await require('./theme-width.cjs')(page);
  await page.evaluate(()=>{
   const now=new Date(2026,10,9,14,8),check=(date,want)=>{const got=instanceTime(date,now);if(got!==want)throw Error(`${got} != ${want}`);};
   check(new Date(+now-59000),'刚刚');check(new Date(+now-60000),'1分钟前');check(new Date(+now-3599999),'59分钟前');
   check(new Date(+now-3600000),'13:08');check(new Date(2026,10,8,14,8),'11月08日 14:08');
   check(new Date(2025,10,8,14,8),'2025年11月08日 14:08');check(new Date(+now+60000),'2026年11月09日 14:09');
   if(instanceTime(new Date(2026,10,8,23,50),new Date(2026,10,9,0,10))!=='20分钟前')throw Error('midnight');
  });
  await page.locator('#instance-selected').click();
  const instanceInfo=await page.evaluate(()=>[...instanceButtons].map(([id,b])=>({id,text:b.textContent,title:b.title,label:b.getAttribute('aria-label')})));
  for(const i of instanceInfo){assert(!i.text.includes(i.id));assert(!i.title.includes(i.id));assert(!i.label.includes(i.id));}
  assert.equal(await page.locator('#instance-options .instance-live').count(),1);
  assert.match(await page.locator('#instance-selected').innerText(),/未命名实例\n/);
  assert.equal(await page.locator('#instance-options .instance-live').evaluate(el=>getComputedStyle(el,'::before').animationName),'instance-pulse');
  await page.emulateMedia({reducedMotion:'reduce'});
  assert.equal(await page.locator('#instance-options .instance-live').evaluate(el=>getComputedStyle(el,'::before').animationName),'none');
  await page.emulateMedia({reducedMotion:'no-preference'});
  await page.locator('#instance-selected').press('ArrowDown');
  const focused=await page.evaluate(()=>document.activeElement===document.querySelector('#instance-options button'));
  assert(focused);await page.evaluate(()=>refreshInstances());assert(await page.evaluate(()=>document.activeElement===document.querySelector('#instance-options button')));
  await page.keyboard.press('Escape');assert.equal(await page.locator('#instances').getAttribute('open'),null);

  // Human names are server-backed, independent of AI labels, and never send terminal input.
  await page.locator('#instance-selected').click();await page.locator('.instance-option-row:has(.instance-choice[aria-current=true]) .instance-rename').click();
  await page.locator('#instance-human-name').fill('人类实例名称');
  await page.locator('#instance-name-save').click();
  await page.waitForFunction(()=>!document.getElementById('instance-name-dialog').open);
  assert.equal(await page.locator('#instance-selected strong').textContent(),'人类实例名称');
  await page.reload();await page.waitForFunction(()=>document.getElementById('instance-selected').textContent.includes('人类实例名称'));
  await page.evaluate(()=>{
   const item=instanceItems.find(i=>i.id===instance);
   paintInstance(document.getElementById('instance-selected'),{...item,name:'AI 工作描述'},new Date());
  });
  assert.equal(await page.locator('#instance-selected strong').textContent(),'人类实例名称');
  assert.equal(await page.locator('#instance-selected .instance-ai-name').textContent(),'AI：AI 工作描述');
  await page.locator('#instance-selected').click();await page.locator('.instance-option-row:has(.instance-choice[aria-current=true]) .instance-rename').click();await page.locator('#instance-human-name').fill('');
  await page.locator('#instance-name-save').click();await page.waitForFunction(()=>!document.getElementById('instance-name-dialog').open);
  assert.equal(await page.locator('#instance-selected strong').textContent(),'未命名实例');
  assert.equal(await page.evaluate(()=>window.sent.filter(m=>m.type==='input'||m.type==='exec').length),0);

  assert.deepEqual(await page.locator('#session-list .session strong').allTextContents(),['s1 · production','s3 · console','s5 · telnet-console','s2 · closed-session']);
  assert.equal(await page.locator('#failed-sessions .session').count(),1);assert.equal(await page.locator('#failed-sessions .session').isVisible(),false);
  await page.locator('#session-error-summary').click();assert.equal(await page.locator('#failed-sessions .session').isVisible(),true);
  await page.locator('#failed-sessions .session').click();await page.waitForFunction(()=>selected==='s4'&&!replaying);
  assert.equal(await page.locator('#unlock').isDisabled(),true);assert.match(await page.locator('#subtitle').innerText(),/连接失败.*handshake/);
  await page.locator('#session-list .session').first().click();await page.waitForFunction(()=>selected==='s1'&&!replaying);
  await page.locator('#session-error-summary').click();
  assert.equal(await page.locator('#scrollback').inputValue(),'5000');
  assert.equal(await page.evaluate('terminal.options.scrollback'),5000);
  assert.match(await page.locator('#sessions').innerText(),/s1 · production/);assert.match(await page.locator('#sessions').innerText(),/s2 · closed-session/);
  await page.waitForFunction(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes('$ both'));
  const text=await page.evaluate(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n'));
  assert.match(text,/stdout/);assert.match(text,/stderr/);
  assert.equal(await page.locator('#run,#command,#task-sidebar').count(),0);
  assert.equal(await page.evaluate(()=>terminal.options.disableStdin),true);
  await page.locator('#appearance-open').click();await page.locator('#scrollback').fill('7500');await page.locator('#apply').click();await page.locator('#appearance-close').click();assert.equal(await page.evaluate('terminal.options.scrollback'),7500);
  await page.reload();await page.waitForFunction(()=>document.getElementById('connection').textContent.includes('实时连接'));assert.equal(await page.locator('#scrollback').inputValue(),'7500');
  await page.evaluate(()=>new Promise(resolve=>terminal.write('\x1b[6n',resolve)));assert.equal(await page.evaluate(()=>sent.filter(m=>m.type==='input').length),0);
  await page.locator('.session').filter({hasText:'s2 · closed-session'}).click();await page.waitForFunction(()=>document.getElementById('title').textContent==='s2 · closed-session');assert.equal(await page.locator('#unlock').isDisabled(),true);
  await page.locator('.session').filter({hasText:'s3 · console'}).click();await page.waitForFunction(()=>document.getElementById('connection').textContent.includes('实时连接'));
  await page.evaluate(async()=>{const orig=Terminal.prototype.write;Terminal.prototype.write=function(data,callback){return orig.call(this,data,()=>setTimeout(()=>callback?.(),75));};try{const previous=selectSession('s1');await new Promise(r=>setTimeout(r,25));await selectSession('s3');await previous;}finally{Terminal.prototype.write=orig;}});
  await page.waitForFunction(()=>document.getElementById('connection').textContent.includes('实时连接'));
  const popup=page.context().waitForEvent('page');await page.locator('#open-terminal').click();const observer=page;page=await popup;await page.waitForFunction(()=>typeof replaying!=='undefined'&&!replaying&&typeof socket!=='undefined'&&socket?.readyState===1);
  await page.locator('#unlock').click();await page.waitForFunction(()=>unlocked);
  await page.locator('.xterm-helper-textarea').focus();await page.keyboard.type('show version');await page.keyboard.press('Enter');
  await page.waitForFunction(()=>!operationPending&&!inputQueue.length);
  assert.equal(await page.evaluate(()=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join('')),'show version\r');
  // This minimal SSH simulator has no PTY line discipline; LF explicitly
  // completes its line reader after verifying raw Enter was sent as CR.
  await page.keyboard.press('Control+j');
  await page.waitForFunction(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes('SwitchOS 1.0'));
  assert.equal(await page.locator('.local-echo-preview').textContent(),'');
  const sentText=await page.evaluate(()=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join(''));assert.equal(sentText,'show version\r\n');
  await page.keyboard.insertText('中文');await page.keyboard.press('Control+j');await page.waitForFunction(()=>document.getElementById('receipts').textContent.includes('中文'));await page.waitForFunction(()=>!operationPending&&!inputQueue.length);assert.equal(await page.evaluate(()=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join('')),'show version\r\n中文\n');
  await require('./ime.cjs')(page);
  await page.evaluate(async()=>{
   const check=(ok,label)=>{if(!ok)throw Error(label);};
   localEcho.clear();terminal.scrollToBottom();
   const text=()=>localEcho.layer.textContent;
   localEcho.add('ab中');localEcho.add('\x7f');check(text()==='ab','grapheme backspace');
   localEcho.add('\x1b[');localEcho.add('D');check(text()==='ab','split arrow suppressed');
   localEcho.add('\x1bOD');check(text()==='ab','application arrow suppressed');
   localEcho.add('\x1b]title\x07');check(text()==='ab','OSC suppressed');
   const event={kind:'output',session:selected,handle,encoding:'utf-8',epoch:taskDecoders.get(handle)?.epoch||0,seq:cursor+1,data:btoa('unrelated')};
   await apply({...event,session:'another-session'},generation,true);check(text()==='ab','other session preserves preview');
   await apply({...event,data:''},generation,true);check(text()==='ab','empty event preserves preview');
   await apply(event,generation,true);check(text()==='','unrelated output clears whole preview');
   localEcho.add('pending');await apply({...event,data:btoa('\x1b[0m')},generation,true);check(text()==='','control-only output clears');
   localEcho.add('pending');await apply({...event,data:btoa(String.fromCharCode(0xe4))},generation,true);check(text()==='','partial UTF-8 clears');
   localEcho.add('pending');await receiveReceipt({id:'input-preview-test',error:'test not_sent'},generation,socket);check(text()==='','failed send clears');
   localEcho.add('pending');document.getElementById('echo').checked=false;document.getElementById('echo').onchange();check(text()==='','disable clears');
   document.getElementById('echo').checked=true;localEcho.add('x'.repeat(7000));check(localEcho.text.length<=4096,'bounded preview');
   unlocked=false;updateControls();check(text()==='','lock clears');
  });
  await page.screenshot({path:process.env.KEEPSESS_SCREENSHOT||require('node:path').join(require('node:os').tmpdir(),'keepsess-web.png'),fullPage:true});
  await page.close();page=observer;await page.bringToFront();
  await page.locator('.session').filter({hasText:'s5 · telnet-console'}).click();await page.waitForFunction(()=>selected==='s5'&&!replaying);await page.keyboard.press('Control+f');await page.locator('#search-text').fill('serial');await page.waitForFunction(()=>terminalQuery.results.length===1);await page.locator('.search-result').click();assert((await page.evaluate(()=>terminal.getSelection())).includes('serial'));assert(await page.locator('#search-only').isDisabled());await page.locator('#search-clear').click();await page.locator('#search-close').click();
  await page.locator('#instance-selected').click();
  await page.getByRole('button',{name:/归档，只读/}).click();
  await page.waitForFunction(()=>document.getElementById('title').textContent==='s1 · archived-console');
  await page.waitForFunction(()=>terminal.buffer.active.getLine(0)?.translateToString().includes('archive retained'));
  await page.waitForFunction(()=>terminal.buffer.active.getLine(1)?.translateToString().includes('中文'));
  assert.equal(await page.locator('#unlock').isDisabled(),true);assert.match(await page.locator('#subtitle').innerText(),/已关闭/);
  await page.keyboard.press('Control+f');await page.locator('#search-text').fill('中文');await page.waitForFunction(()=>terminalQuery.results.length===1);await page.locator('.search-result').click();assert((await page.evaluate(()=>terminal.getSelection())).includes('中文'));await page.locator('#search-clear').click();await page.locator('#search-close').click();
  assert.deepEqual(errors,[]);console.log('browser acceptance passed: compact observer, persistence, stream terminal tab, nested path, archive replay, no parser replies');
 } finally {await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
