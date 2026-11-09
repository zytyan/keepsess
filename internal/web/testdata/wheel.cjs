const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
const paint=require('./highlight-paint.cjs');
(async()=>{const browser=await launch({headless:true,args:['--no-sandbox']});try{
 const page=await browser.newPage({viewport:{width:1500,height:1100},deviceScaleFactor:1}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(process.argv[2]);await page.waitForFunction(()=>typeof terminal!=='undefined'&&terminal&&!replaying);
 await page.evaluate(async()=>{
  terminal.reset();terminal.resize(120,35);terminal.options.scrollback=5000;
  applyAppearance({enabled:true,rules:[{name:'errors',pattern:'(?:ERROR)',regex:true,enabled:true,foreground:'#ff8585'}]});
  await new Promise(r=>terminal.write(Array.from({length:1500},(_,i)=>('ERROR '+i+' ').repeat(10)+'\r\n').join(''),r));
 });
 await page.waitForFunction(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return h.decorations.length>200&&!h.busy&&!h.timer;});
 await paint.install(page);
 assert(await page.evaluate(()=>!![...terminalHighlights].find(h=>h.term===terminal).layer));
 const during=await page.evaluate(async()=>{
  const h=[...terminalHighlights].find(h=>h.term===terminal),clear=h.clear,scan=h.scan;
  const stats={clears:0,scans:0,inputs:0,nativeDecorations:0,coloredPaints:0,rendered:0,uncoloredFrames:0,misses:[]};
  const request=highlightEngine.request;
  // Keep replies in flight across viewport changes. Their unchanged-content
  // results must populate the prefetch cache rather than starve while scrolling.
  highlightEngine.request=function(...args){return request.apply(this,args).then(data=>new Promise(resolve=>setTimeout(()=>resolve(data),40)));};
  const rendered=terminal.onRender(()=>{stats.rendered++;if(!keywordPaintReady(terminal)){stats.uncoloredFrames++;stats.misses.push({view:terminal.buffer.active.viewportY,cached:h.cache.get(terminal.buffer.active.viewportY)?.length,text:terminal.element.querySelector('.xterm-rows').firstElementChild.textContent.slice(0,12)});}});
  const register=terminal.registerDecoration,layer=h.layer,paint=layer.paint;
  terminal.registerDecoration=function(...args){stats.nativeDecorations++;return register.apply(this,args)};
  layer.paint=function(...args){const result=paint.apply(this,args);if(terminal.element.querySelector('.keepsess-keyword'))stats.coloredPaints++;return result;};
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
  }finally{h.clear=clear;h.scan=scan;terminal.registerDecoration=register;layer.paint=paint;highlightEngine.request=request;rendered.dispose();}
 });
 assert(during.after<during.before);assert(during.scans>0,'ranges should update while scrolling');assert(during.coloredPaints>0,'visible ranges must stay colored while scrolling');
 assert.equal(during.nativeDecorations,0,'synchronous painting must not register xterm DOM decorations');
 assert(during.rendered>5,JSON.stringify(during));assert.equal(during.uncoloredFrames,0,'new rows must be colored in the render callback, before delayed Worker replies: '+JSON.stringify(during));
 assert.equal(await page.locator('#terminal .xterm-decoration').count(),0);
 assert(await page.evaluate(()=>[...terminalHighlights].find(h=>h.term===terminal).layer.rows.size<=terminal.rows));
 try{await page.waitForFunction(()=>{
  const h=[...terminalHighlights].find(h=>h.term===terminal),view=terminal.buffer.active.viewportY;
  return !h.busy&&!h.timer&&h.decorations.length>200&&h.markers.some(m=>m.line===view)&&h.markers.every(m=>m.line>=view&&m.line<view+terminal.rows);
 },null,{timeout:5000});}catch(error){console.log(await page.evaluate(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return {errors:document.querySelector('#highlight-status').textContent,view:terminal.buffer.active.viewportY,cache:[...h.cache.keys()],markers:h.markers.map(m=>m.line),decos:h.decorations.length,busy:h.busy,timer:h.timer,dirty:h.dirty}}));throw error;}
 const settled=await page.evaluate(()=>{
  const h=[...terminalHighlights].find(h=>h.term===terminal);
  return {markers:h.markers.length,decorations:h.decorations.length,rows:terminal.rows,painted:keywordPaintReady(terminal)};
 });
 assert(settled.markers<=settled.rows);assert(settled.decorations>settled.markers*5);assert(settled.painted);await paint.check(page);
 const redraw=await page.evaluate(async()=>{
  let renders=0;const listener=terminal.onRender(()=>renders++);
  // xterm 5.5's cursor/selection/link redraw path used to replace our colored
  // nodes without emitting the public render event. Exercise that exact path.
  terminal._core._renderService.refreshRows(0,terminal.rows-1,true);
  await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  listener.dispose();return {renders,colored:keywordPaintReady(terminal)};
 });
 assert(redraw.renders>0,'xterm redraw-only rendering must notify color/echo layers');
 assert(redraw.colored,'redraw-only rendering must keep the keyword color');await paint.check(page);
 const direct=await page.evaluate(()=>{
  const renderer=terminal._core._renderService._renderer.value;
  renderer.handleSelectionChanged(undefined,undefined,false);
  const selection=keywordPaintReady(terminal);
  renderer._setCellUnderline(0,5,0,0,terminal.cols,true);
  return {selection,link:keywordPaintReady(terminal)};
 });assert(direct.selection&&direct.link,'direct selection/link rendering must synchronously preserve color');await paint.check(page);
 const held=await page.evaluate(async()=>{
  const old=terminal.element.querySelector('.xterm-rows').firstElementChild.textContent,request=highlightEngine.request;
  highlightEngine.request=function(...args){return request.apply(this,args).then(data=>new Promise(resolve=>setTimeout(()=>resolve(data),80)));};
  terminal.scrollToTop();
  await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  const result={pending:[...terminalHighlights].find(h=>h.term===terminal).renderPending,unchanged:terminal.element.querySelector('.xterm-rows').firstElementChild.textContent===old,colored:keywordPaintReady(terminal)};
  highlightEngine.request=request;return result;
 });assert(held.pending&&held.unchanged&&held.colored,'uncached jumps must retain the previous colored frame until matching is ready');
 await page.waitForFunction(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return !h.renderPending&&!h.busy&&terminal.element.querySelector('.xterm-rows').firstElementChild.textContent.startsWith('ERROR 0 ');});await paint.check(page);
 await page.evaluate(()=>terminal.scrollToBottom());await page.waitForFunction(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return !h.renderPending&&!h.busy;});
 await page.evaluate(()=>applyAppearance({enabled:true,rules:[{name:'literal',pattern:'ERROR',enabled:true,foreground:'#ff8585'}]}));
 await page.waitForFunction(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return h.syncRules&&!h.busy&&!h.timer;});
 const synchronous=await page.evaluate(async()=>{
  const request=highlightEngine.request;let requests=0;highlightEngine.request=function(...args){requests++;return request.apply(this,args);};
  try{terminal.scrollToTop();await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));return {requests,pending:[...terminalHighlights].find(h=>h.term===terminal).renderPending,text:terminal.element.querySelector('.xterm-rows').firstElementChild.textContent,colored:keywordPaintReady(terminal)};}finally{highlightEngine.request=request;}
 });assert.equal(synchronous.requests,0,'literal jumps must match in the current render, without a Worker round trip');assert(!synchronous.pending&&synchronous.text.startsWith('ERROR 0 ')&&synchronous.colored);await paint.check(page);
 await page.evaluate(()=>terminal.scrollToBottom());await page.waitForFunction(()=>!([...terminalHighlights].find(h=>h.term===terminal).renderPending));
 // Real Chromium wheel input still moves the viewport and restores highlighting.
 await page.locator('#terminal .xterm-screen').hover();const before=await page.evaluate(()=>terminal.buffer.active.viewportY);
 await page.mouse.wheel(0,-300);await page.waitForFunction(before=>terminal.buffer.active.viewportY<before,before);
 await page.waitForFunction(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return !h.busy&&!h.timer&&h.markers.some(m=>m.line===terminal.buffer.active.viewportY)});
 await page.locator('#sessions .session').first().click();await page.waitForFunction(()=>!replaying);assert.deepEqual(errors,[]);
 console.log('wheel passed:',JSON.stringify({during,markers:settled.markers,decorations:settled.decorations}));
}finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});
