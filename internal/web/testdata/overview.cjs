const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1500,height:900}});page.setDefaultTimeout(10000);
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>{window.sent=[];const original=WebSocket.prototype.send;WebSocket.prototype.send=function(data){sent.push(JSON.parse(data));return original.call(this,data);};});
  const control=async(path,query)=>{const u=new URL(path,process.argv[3]);for(const[k,v]of Object.entries(query))u.searchParams.set(k,v);const r=await fetch(u,{method:'POST'});if(!r.ok)throw Error(await r.text());return r.json();};
  const create=(name,extra={})=>control('/create',{name,...extra});
  const append=(name,tag,lines=100)=>control('/append',{name,tag,lines});
  const bytes=(name,hex)=>control('/bytes',{name,hex});
  const ready=()=>page.waitForFunction(()=>overviewMode&&!replaying&&socket?.readyState===1);
  const text=handle=>page.evaluate(id=>{const t=overview.cards.get(id)?.terminal;return t?Array.from({length:t.buffer.active.length},(_,i)=>t.buffer.active.getLine(i)?.translateToString()).join('\n'):'';},handle);
  const contains=(handle,value)=>page.waitForFunction(({id,value})=>{const t=overview.cards.get(id)?.terminal;return t&&Array.from({length:t.buffer.active.length},(_,i)=>t.buffer.active.getLine(i)?.translateToString()).join('\n').includes(value);},{id:handle,value});
  await page.goto(process.argv[2]);await page.waitForFunction(()=>!replaying&&socket?.readyState===1);
  const second=await create('short-second');await page.waitForSelector(`#tasks button[data-handle="${second.handle}"]`);
  await page.locator('#overview-toggle').click();await ready();
  await contains('task1','short-first initial line 001');await contains(second.handle,'short-second initial line 001');
  assert.equal(await page.locator('#unlock').isDisabled(),true);assert.equal(await page.locator('#exec').isVisible(),false);assert.equal(await page.evaluate(()=>[...overview.cards.values()].every(card=>!card.terminal||card.terminal.options.fontFamily===WEB_FONT_FAMILY)),true);
  const one=await page.locator('.overview-card[data-handle="task1"]').boundingBox(),two=await page.locator(`.overview-card[data-handle="${second.handle}"]`).boundingBox();assert(two.y>one.y&&two.y+two.height<900);
  await bytes('short-first','e4b8');await bytes('short-second','e6');await page.waitForTimeout(50);
  await bytes('short-first','ad0a');await bytes('short-second','96870a');await contains('task1','中');await contains(second.handle,'文');
  assert(!(await text('task1')).includes('文'));assert(!(await text(second.handle)).includes('中'));assert(!(await text('task1')).includes('\ufffd'));
  await page.evaluate(()=>new Promise(r=>overview.cards.get('task1').terminal.write('\x1b[6n',r)));assert.equal(await page.evaluate(()=>sent.filter(m=>m.type==='input').length),0);
  const long=await create('long');await contains(long.handle,'long initial line 099');assert.equal(await page.evaluate(id=>overview.cards.get(id).terminal.rows,long.handle),12);
  const samples=[];for(const name of ['pwd','hostname','uptime']){const r=await create(name);samples.push(r.handle);await page.waitForSelector(`.overview-card[data-handle="${r.handle}"]`);}
  await contains(samples[0],'/srv/keepsess');await contains(samples[1],'demo-server');await contains(samples[2],'load average');
  await page.waitForFunction(()=>overview.atTail());
  if(process.env.KEEPSESS_OVERVIEW_SCREENSHOT)await page.screenshot({path:process.env.KEEPSESS_OVERVIEW_SCREENSHOT,fullPage:true});
  // Growing an earlier card preserves the position of the card being read.
  await page.evaluate(id=>{const root=overview.container,el=overview.cards.get(id).element;root.scrollTop+=el.getBoundingClientRect().top-root.getBoundingClientRect().top;},long.handle);
  await page.waitForFunction(()=>!overview.bodyTail());
  const anchor=await page.evaluate(id=>overview.cards.get(id).element.getBoundingClientRect().top,long.handle);
  await append('short-first','growing-above',50);await contains('task1','growing-above 049');
  const after=await page.evaluate(id=>overview.cards.get(id).element.getBoundingClientRect().top,long.handle);assert(Math.abs(after-anchor)<2,`reading anchor moved: ${anchor} -> ${after}`);
  await page.locator(`.overview-card[data-handle="${second.handle}"] .overview-open`).click();await page.waitForFunction(id=>!overviewMode&&handle===id&&!replaying&&socket?.readyState===1,second.handle);
  assert.equal(await page.locator('#unlock').isDisabled(),false);assert.equal(await page.locator('#run').isDisabled(),true);
  await page.locator('#overview-toggle').click();await ready();
  for(let i=0;i<22;i++)await create('done-'+String(i).padStart(2,'0'));
  await page.waitForFunction(()=>tasks.size===28&&overview.cards.size===20&&overview.lastHandle()==='task28');
  assert.equal(await page.locator('.overview-card').count(),20);assert.equal(await page.evaluate(()=>[...overview.cards.values()].every(c=>!c.terminal||c.terminal.options.scrollback===250)),true);
  await page.locator('#overview-older').click();await ready();await contains('task1','short-first initial line 001');assert.equal(await page.locator('.overview-card').count(),8);
  await page.locator('#overview-newer').click();await ready();await page.waitForFunction(()=>overview.lastHandle()==='task28');
  await page.evaluate(()=>overview.container.scrollTop=0);await page.waitForFunction(()=>!overview.atTail());
  const ids=await page.evaluate(()=>overview.ids.slice());const top=await page.evaluate(()=>overview.container.scrollTop);
  const paused=await create('short-paused');await page.waitForSelector(`#tasks button[data-handle="${paused.handle}"]`);
  assert.deepEqual(await page.evaluate(()=>overview.ids),ids);assert.equal(await page.evaluate(()=>overview.container.scrollTop),top);
  await page.locator('#overview-latest').click();await ready();await contains(paused.handle,'short-paused initial line 001');
  const follow=await create('short-follow');await contains(follow.handle,'short-follow initial line 001');assert.equal(await page.evaluate(()=>overviewMode&&overview.atTail()),true);
  const pty=await create('short-pty',{pty:true});await page.waitForSelector(`.overview-card[data-handle="${pty.handle}"]`);assert.equal(await page.evaluate(id=>overview.cards.get(id).terminal===null,pty.handle),true);assert.match(await page.locator(`.overview-card[data-handle="${pty.handle}"]`).innerText(),/PTY/);
  assert.deepEqual(errors,[]);console.log('overview passed: multiple short commands, independent decoders, long output cap, no writes, reading anchor, bounded paging, paused/followed additions, PTY shortcut');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
