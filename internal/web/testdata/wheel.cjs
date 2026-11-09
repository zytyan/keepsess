const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{const browser=await chromium.launch({headless:true,args:['--no-sandbox']});try{
 const page=await browser.newPage({viewport:{width:1500,height:1100}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(process.argv[2]);await page.waitForFunction(()=>typeof terminal!=='undefined'&&terminal&&!replaying);
 await page.evaluate(async()=>{
  terminal.reset();terminal.resize(120,35);terminal.options.scrollback=5000;
  applyAppearance({enabled:true,rules:[{name:'errors',pattern:'ERROR',regex:false,enabled:true,foreground:'#ff8585'}]});
  await new Promise(r=>terminal.write(Array.from({length:1500},(_,i)=>('ERROR '+i+' ').repeat(10)+'\r\n').join(''),r));
 });
 await page.waitForFunction(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return h.decorations.length>200&&!h.busy&&!h.timer;});
 const during=await page.evaluate(async()=>{
  const h=[...terminalHighlights].find(h=>h.term===terminal),clear=h.clear,scan=h.scan;
  const stats={clears:0,scans:0,inputs:0};
  h.clear=function(){stats.clears++;return clear.call(this)};h.scan=function(){stats.scans++;return scan.call(this)};
  const before=terminal.buffer.active.viewportY;
  try{
   // Exercise the real xterm wheel listener at high frequency without an
   // automation action's implicit frame waits between individual wheel inputs.
   await new Promise(resolve=>{const timer=setInterval(()=>{
    terminal.element.querySelector('.xterm-screen').dispatchEvent(new WheelEvent('wheel',{bubbles:true,cancelable:true,deltaY:-45}));
    if(++stats.inputs===40){clearInterval(timer);resolve();}
   },12);});
   return {...stats,before,after:terminal.buffer.active.viewportY};
  }finally{h.clear=clear;h.scan=scan;}
 });
 assert(during.after<during.before);assert.equal(during.scans,0,'highlights must not rebuild during a wheel burst');
 assert(during.clears<=1,JSON.stringify(during));
 await page.waitForFunction(()=>{
  const h=[...terminalHighlights].find(h=>h.term===terminal),view=terminal.buffer.active.viewportY;
  return !h.busy&&!h.timer&&h.decorations.length>200&&h.markers.some(m=>m.line===view)&&h.markers.every(m=>m.line>=view&&m.line<view+terminal.rows);
 });
 const settled=await page.evaluate(()=>{
  const h=[...terminalHighlights].find(h=>h.term===terminal),row=terminal.element.querySelector('.xterm-rows').firstElementChild;
  return {markers:h.markers.length,decorations:h.decorations.length,rows:terminal.rows,text:row.textContent,colors:[...row.querySelectorAll('span')].flatMap(el=>[...el.textContent].map(()=>getComputedStyle(el).color)).slice(0,5)};
 });
 assert(settled.markers<=settled.rows);assert(settled.decorations>settled.markers*5);assert(settled.text.startsWith('ERROR'));assert(settled.colors.every(c=>c==='rgb(255, 133, 133)'));
 // Real Chromium wheel input still moves the viewport and restores highlighting.
 await page.locator('#terminal .xterm-screen').hover();const before=await page.evaluate(()=>terminal.buffer.active.viewportY);
 await page.mouse.wheel(0,-300);await page.waitForFunction(before=>terminal.buffer.active.viewportY<before,before);
 await page.waitForFunction(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return !h.busy&&!h.timer&&h.markers.some(m=>m.line===terminal.buffer.active.viewportY)});
 await page.locator('#sessions .session').first().click();await page.waitForFunction(()=>!replaying);assert.deepEqual(errors,[]);
 console.log('wheel passed:',JSON.stringify({during,markers:settled.markers,decorations:settled.decorations}));
}finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});
