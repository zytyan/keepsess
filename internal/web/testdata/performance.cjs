// Opt-in, headed Chromium/Edge DevTools traces against the real Web fixture.
// Files contain only fixture data. Never run builds concurrently with sampling.
const {launch}=require('./launch.cjs');
const fs=require('node:fs'),path=require('node:path');
(async()=>{
 const out=process.env.KEEPSESS_PERF_DIR;if(!out)throw Error('KEEPSESS_PERF_DIR is required');fs.mkdirSync(out,{recursive:true});
 const browser=await launch({headless:false,ignoreDefaultArgs:['--hide-scrollbars']});
 try{
  const system=await browser.newBrowserCDPSession();
  fs.writeFileSync(path.join(out,'system.json'),JSON.stringify({version:browser.version(),...(await system.send('SystemInfo.getInfo'))},null,2));
  const page=await browser.newPage({viewport:{width:1500,height:1000},deviceScaleFactor:1});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  if(process.env.KEEPSESS_PERF_APPEARANCE)await page.route('**/appearance.js',route=>route.fulfill({path:path.resolve(process.env.KEEPSESS_PERF_APPEARANCE),contentType:'application/javascript'}));
  await page.goto(process.argv[2]);await page.waitForFunction(()=>typeof terminal!=='undefined'&&terminal&&!replaying);
  const scenarios=(process.env.KEEPSESS_PERF_SCENARIOS||'off,ansi,keyword,builtin').split(',');
  if(scenarios.some(kind=>!['off','ansi','keyword','builtin'].includes(kind)))throw Error('unknown performance scenario');
  for(const kind of scenarios){
   await page.evaluate(async kind=>{
    terminal.reset();terminal.resize(100,30);terminal.options.scrollback=5000;
    applyAppearance({enabled:kind==='keyword'||kind==='builtin',rules:kind==='builtin'?appearanceDefaults.rules:[{name:'errors',pattern:'ERROR',enabled:true,foreground:'#ff8585'}]});
    const word=kind==='ansi'?'\x1b[38;2;255;133;133mERROR\x1b[0m':'ERROR';
    await new Promise(r=>terminal.write(Array.from({length:5200},(_,i)=>(word+' '+i+' ').repeat(8)+'\r\n').join(''),r));
   },kind);
   if(kind==='keyword'||kind==='builtin')await page.waitForFunction(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return h.decorations.length>0&&!h.busy&&!h.timer;});
   await page.locator('#terminal .xterm-screen').hover();
   for(const phase of ['first','warm']){
    await page.bringToFront();await page.waitForFunction(()=>document.visibilityState==='visible');
    const cdp=await page.context().newCDPSession(page);
    await cdp.send('Tracing.start',{categories:'devtools.timeline,v8.execute,disabled-by-default-v8.cpu_profiler,blink.user_timing',transferMode:'ReturnAsStream'});
    await page.evaluate(()=>{window.perfFrames=[];window.perfRunning=true;let last=performance.now();function frame(now){if(!perfRunning)return;perfFrames.push(now-last);last=now;requestAnimationFrame(frame);}requestAnimationFrame(frame);performance.mark('idle-start');});
    await page.waitForTimeout(200);await page.evaluate(()=>performance.mark('wheel-start'));
    for(let i=0;i<30;i++){await page.mouse.wheel(0,-135);await page.waitForTimeout(8);}
    await page.evaluate(()=>performance.mark('wheel-stop'));await page.waitForTimeout(350);
    // A native thumb drag, separate from wheel input, in the same trace.
    await page.addStyleTag({content:'#terminal .xterm-viewport::-webkit-scrollbar{width:14px}#terminal .xterm-viewport::-webkit-scrollbar-thumb{background:#999;min-height:20px}#terminal .xterm-viewport::-webkit-scrollbar-button{display:none}'});
    const scroll=await page.locator('#terminal .xterm-viewport').evaluate(el=>{const b=el.getBoundingClientRect();const thumb=Math.max(20,b.height*el.clientHeight/el.scrollHeight);return {x:b.right-5,y:b.top+(b.height-thumb)*el.scrollTop/(el.scrollHeight-el.clientHeight)+thumb/2,target:b.top+b.height*.4};});
    await page.evaluate(()=>performance.mark('drag-start'));await page.mouse.move(scroll.x,scroll.y);await page.mouse.down();await page.mouse.move(scroll.x,scroll.target,{steps:6});await page.mouse.up();await page.waitForTimeout(350);await page.evaluate(()=>performance.mark('drag-stop'));
    const metrics=await page.evaluate(()=>{perfRunning=false;return {frames:perfFrames,visibility:document.visibilityState,dpr:devicePixelRatio,font:terminal.options.fontFamily,rows:terminal.rows,cols:terminal.cols,spans:terminal.element.querySelectorAll('.xterm-rows span').length,view:terminal.buffer.active.viewportY};});
    const done=new Promise(r=>cdp.once('Tracing.tracingComplete',r));await cdp.send('Tracing.end');const {stream}=await done;let trace='';
    while(true){const chunk=await cdp.send('IO.read',{handle:stream});trace+=chunk.data;if(chunk.eof)break;}await cdp.send('IO.close',{handle:stream});
    fs.writeFileSync(path.join(out,`${kind}-${phase}.trace.json`),trace);fs.writeFileSync(path.join(out,`${kind}-${phase}.metrics.json`),JSON.stringify(metrics,null,2));
    console.log(JSON.stringify({kind,phase,maxFrame:Math.max(...metrics.frames),spans:metrics.spans}));await cdp.detach();
   }
  }
  if(errors.length)throw Error(errors.join('\n'));
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
