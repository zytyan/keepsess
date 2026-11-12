const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:960}}),page=await context.newPage(),errors=[];
  context.on('page',p=>p.on('pageerror',e=>errors.push(e.message)));page.on('pageerror',e=>errors.push(e.message));
  await context.addInitScript(()=>{window.sent=[];const send=WebSocket.prototype.send;WebSocket.prototype.send=function(data){sent.push(JSON.parse(data));return send.call(this,data);};});
  page.setDefaultTimeout(10000);
  const ready=p=>p.waitForFunction(()=>typeof replaying!=='undefined'&&typeof terminal!=='undefined'&&terminal&&!replaying&&socket?.readyState===1);
  const text=p=>p.evaluate(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString(true)).join('\n'));
  const contains=(p,v)=>p.waitForFunction(v=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString(true)).join('\n').includes(v),v);
  const control=async(p,q)=>{const u=new URL(p,process.argv[3]);for(const[k,v]of Object.entries(q))u.searchParams.set(k,v);const r=await fetch(u,{method:'POST'});assert(r.ok,await r.text());};
  await page.goto(process.argv[2]);await ready(page);await contains(page,'SSH transport healthy');await contains(page,'WARNING');
  assert.equal(await page.locator('#exec,#command,#task-sidebar').count(),0);assert.equal(await page.evaluate(()=>terminal.options.disableStdin),true);
  await page.waitForSelector('#source-gutter .source-row[data-stream=stdout]');await page.waitForSelector('#source-gutter .source-row[data-stream=stderr]');
  assert.equal(await page.locator('#history-drawer').isVisible(),false);
  await page.evaluate(()=>{
   const ansi=new TranscriptANSI();if(ansi.feed('\x1b[31')!=='')throw Error('split CSI');
   const projected=ansi.feed('mred\x1b[2J\x1b[?1049h visible\x1b]0;hidden title\x07');
   if(projected!=='\x1b[31mred visible')throw Error('unsafe ANSI projection');
   const text='中'.repeat(6000)+'\x1b[38;2;1;2;3m😀';const n=transcriptBoundary(text,text.length);
   if(new TextEncoder().encode(text.slice(0,n)).length>16384||text.slice(0,n).endsWith('\x1b'))throw Error('split projection boundary');
   if(ansi.feed('\x1b]'+ 'x'.repeat(5000))!==''||!ansi.truncated||ansi.escape.length>4096)throw Error('unbounded ANSI state');
  });
  // Ordinary copying never includes the provenance gutter.
  await page.evaluate(()=>terminal.selectAll());assert(!(await page.evaluate(()=>terminal.getSelection())).includes('task1 / stdout'));
  assert((await page.evaluate(()=>transcript.selectionWithSources())).includes('task1 / stdout'));await page.evaluate(()=>terminal.clearSelection());
  await control('/create',{name:'hostname'});await contains(page,'demo-server');await control('/create',{name:'uptime'});await contains(page,'load average');
  await page.locator('#history-toggle').click();assert(await page.locator('#tasks button').count()>=3);await page.locator('#history-close').click();
  const screenshotDir=process.env.KEEPSESS_SCREENSHOT_DIR;
  if(screenshotDir){await fs.mkdir(screenshotDir,{recursive:true});await page.screenshot({path:path.join(screenshotDir,'workspace.png')});}
  console.log('workspace: sources and history ready');
  await page.locator('#search-toggle').click();await page.locator('#search-advanced summary').click();await page.locator('#filter-streams input[value=stdout]').uncheck();await page.locator('#filter-apply').click();await ready(page);await contains(page,'WARNING');await page.waitForFunction(()=>!Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes('SSH transport healthy'));assert(!(await text(page)).includes('SSH transport healthy'));
  await page.locator('#filter-reset').click();await ready(page);await contains(page,'SSH transport healthy');
  // Per-source complete-line text matching survives packet splits.
  await page.locator('#search-text').fill('split-needle');await page.locator('#search-only').check();await page.locator('#filter-apply').click();await ready(page);
  await control('/create',{name:'short-split'});
  await control('/bytes',{name:'short-split',hex:Buffer.from('split-nee').toString('hex')});await control('/bytes',{name:'short-split',hex:Buffer.from('dle found\n').toString('hex')});await contains(page,'split-needle found');
  await page.locator('#filter-reset').click();await ready(page);await page.locator('#search-clear').click();await page.locator('#search-close').click();
  console.log('workspace: filters passed');
  // Import validates atomically; export includes only whitelisted browser preferences.
  await page.locator('#appearance-open').click();await page.locator('#font-size').fill('17');await page.locator('#font-size').dispatchEvent('input');assert.equal(await page.evaluate(()=>terminal.options.fontSize),17);
  await page.locator('#scrollback').fill('7500');await page.locator('#apply').click();
  if(screenshotDir)await page.screenshot({path:path.join(screenshotDir,'settings.png')});
  await page.locator('[data-settings=config]').click();const download=page.waitForEvent('download');await page.locator('#preferences-export').click();const file=await download,exported=JSON.parse(await fs.readFile(await file.path(),'utf8'));
  assert.equal(exported.fontSize,17);assert(!JSON.stringify(exported).includes('password'));assert(!JSON.stringify(exported).includes('systemctl'));
  const before=await page.evaluate(()=>JSON.stringify(preferences));await page.locator('#preferences-import').setInputFiles({name:'bad.json',mimeType:'application/json',buffer:Buffer.from('{"version":99}')});await page.waitForFunction(()=>document.getElementById('highlight-status').textContent.includes('导入失败'));assert.equal(await page.evaluate(()=>JSON.stringify(preferences)),before);
  exported.fontSize=15;await page.locator('#preferences-import').setInputFiles({name:'valid.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(exported))});await page.waitForFunction(()=>preferences.fontSize===15);await page.locator('#appearance-close').click();await page.reload();await ready(page);assert.equal(await page.evaluate(()=>terminal.options.fontSize),15);
  await page.locator('#appearance-open').click();await page.locator('#theme-mode').selectOption('light');assert.equal(await page.locator('html').getAttribute('data-theme'),'light');await page.locator('#theme-mode').selectOption('dark');await page.locator('#appearance-close').click();
  console.log('workspace: preferences passed');
  const popup=context.waitForEvent('page');await page.locator('#open-terminal').click();let human=await popup;await ready(human);assert.equal(await human.evaluate(()=>sent.filter(x=>x.type==='web_shell').length),0);
  await human.locator('#unlock').click();await human.waitForFunction(()=>unlocked);await human.locator('#shell-start').click();await human.waitForFunction(()=>!!handle&&!replaying&&socket?.readyState===1);await contains(human,'demo@lab');
  const humanURL=human.url(),task=await human.evaluate(()=>handle);await human.locator('#unlock').click();await human.waitForFunction(()=>unlocked);await human.setViewportSize({width:1280,height:880});await human.waitForFunction(()=>sent.some(x=>x.type==='web_resize'));await human.locator('.xterm-helper-textarea').focus();await human.keyboard.type('echo terminal-alive');await human.keyboard.press('Enter');await contains(human,'terminal-alive');
  await page.waitForFunction(id=>tasks.get(id)?.origin==='human',task);await contains(page,'terminal-alive');
  if(screenshotDir)await human.screenshot({path:path.join(screenshotDir,'human-terminal.png')});
  await human.close();human=await context.newPage();await human.goto(humanURL);await ready(human);await contains(human,'terminal-alive');assert.equal(await human.evaluate(()=>unlocked),false);assert.equal(await human.evaluate(()=>sent.filter(x=>x.type==='web_shell').length),0);
  await human.locator('#unlock').click();await human.waitForFunction(()=>unlocked);await human.locator('#shell-end').click();await human.waitForFunction(()=>tasks.get(handle)?.status.state==='finished');
  const invalid=await context.newPage(),badURL=new URL(humanURL);badURL.searchParams.set('handle','task1');await invalid.goto(badURL.href);await invalid.waitForFunction(()=>typeof selected!=='undefined'&&selected===''&&document.getElementById('warning').textContent.includes('不是保留的人类终端'));assert.equal(await invalid.evaluate(()=>sent.filter(x=>x.type==='web_shell'||x.type==='input').length),0);await invalid.close();
  assert.deepEqual(errors,[]);console.log('workspace passed: sources, filters, split lines, settings roundtrip, tabs, shell restore and explicit termination');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
