const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({headless:true,ignoreDefaultArgs:['--hide-scrollbars'],args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1500,height:1100}}),errors=[];
  page.setDefaultTimeout(7000);
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(process.argv[2]);await page.waitForFunction(()=>typeof terminal!=='undefined'&&terminal&&terminalHighlights.size>0&&!replaying);
  assert.equal(await page.evaluate(()=>terminal.options.minimumContrastRatio),4.5);
  await page.evaluate(()=>new Promise(resolve=>{terminal.reset();terminal.write('中文 ERROR 192.168.1.2\r\n0 errors\r\n\x1b[35mERROR\x1b[0m\r\n',resolve);}));
  await page.waitForFunction(()=>[...terminalHighlights].some(h=>h.term===terminal&&h.decorations.length>=2));
  let snapshot=await page.evaluate(()=>{const h=[...terminalHighlights].find(h=>h.term===terminal);return {text:terminal.buffer.active.getLine(0).translateToString(true),spans:h.decorations.map(d=>({x:d.options.x,w:d.options.width,y:d.marker.line})),markers:h.markers.length};});
  assert.equal(snapshot.text,'中文 ERROR 192.168.1.2');assert(snapshot.spans.some(d=>d.x===5&&d.w===5));assert(snapshot.spans.every(d=>d.y===0),JSON.stringify(snapshot));
  // Split chunks and repaint replace old decorations, without altering cells.
  await page.evaluate(async()=>{terminal.reset();await new Promise(r=>terminal.write('ER',r));await new Promise(r=>terminal.write('ROR',r));});
  await page.waitForFunction(()=>[...terminalHighlights].find(h=>h.term===terminal).decorations.length===1);
  await page.evaluate(()=>new Promise(r=>terminal.write('\r\x1b[2KOK',r)));
  await page.waitForTimeout(400);assert.equal(await page.evaluate(()=>[...terminalHighlights].find(h=>h.term===terminal).decorations.length),0);
  // A keyword split by terminal wrapping remains one match; wide cell offsets stay correct.
  await page.evaluate(async()=>{terminal.reset();terminal.resize(10,8);await new Promise(r=>terminal.write('1234567 ERROR',r));});
  await page.waitForFunction(()=>[...terminalHighlights].find(h=>h.term===terminal).decorations.length===2);
  await page.evaluate(()=>{terminal.selectAll();});assert.equal(await page.evaluate(()=>terminal.getSelection().trim()),'1234567 ERROR');
  await page.evaluate(async()=>{terminal.reset();applyAppearance({enabled:true,rules:[{name:'anchor',pattern:'ERROR$',regex:true,enabled:true,foreground:'#ffffff'}]});await new Promise(r=>terminal.write('ERROR',r));});
  await page.waitForFunction(()=>[...terminalHighlights].find(h=>h.term===terminal).decorations.length===1);
  await page.evaluate(()=>new Promise(r=>terminal.write('\x1b[?1049hERROR',r)));
  await page.waitForTimeout(300);assert.equal(await page.evaluate(()=>[...terminalHighlights].find(h=>h.term===terminal).decorations.length),0);
  await page.evaluate(async()=>{await new Promise(r=>terminal.write('\x1b[?1049l',r));applyAppearance(appearanceDefaults);});
  await page.locator('#appearance-open').click();await page.locator('#highlight-apply').click();await page.waitForFunction(()=>[...terminalHighlights].some(h=>h.term===previewTerminal&&h.decorations.length>0));
  await page.locator('#highlight-add').click();const row=page.locator('.highlight-rule').last();await row.locator('[data-field="name"]').fill('自定义');await row.locator('[data-field="pattern"]').fill('中文测试');
  await page.locator('#highlight-save').click();await page.waitForFunction(()=>document.getElementById('highlight-status').textContent.includes('已保存'));
  await page.setViewportSize({width:1000,height:500});
  const before=await page.locator('.appearance-heading').boundingBox();
  await page.locator('.appearance-body').evaluate(el=>{el.scrollTop=el.scrollHeight;});
  const after=await page.locator('.appearance-heading').boundingBox();assert.equal(after.y,before.y);
  const close=await page.locator('#appearance-close').boundingBox();assert(close.y>=0&&close.y+close.height<=500);
  assert(await page.locator('.appearance-body').evaluate(el=>el.scrollTop>0));
  await page.locator('#appearance-close').click();await page.setViewportSize({width:1500,height:1100});await page.reload();await page.waitForFunction(()=>typeof appearance!=='undefined'&&appearance.rules.some(r=>r.pattern==='中文测试'));
  // Shared native worker validates syntax and uses correct UTF-16 offsets.
  await page.evaluate(()=>applyAppearance({enabled:true,rules:[{name:'bad',pattern:'[',regex:true,enabled:true,foreground:'#ffffff'}]}));
  await page.waitForFunction(()=>document.getElementById('highlight-status').textContent.includes('无效'));
  await page.evaluate(()=>applyAppearance(appearanceDefaults));
  assert.equal(await page.evaluate(()=>highlightEngine.engine),'javascript-regexp');
  const check=await page.evaluate(async()=>{
   const rules=[{name:'invalid',pattern:'[',regex:true,enabled:true}];
   const bad=await highlightEngine.request('test',{lines:['ab'],rules});
   const unicode=await highlightEngine.request('test',{lines:['😀中文 ERROR'],rules:[{name:'unicode',pattern:'中文',regex:false,enabled:true,case_sensitive:true}]});
   const precedence=await highlightEngine.request('test',{lines:['ERROR'],rules:[{name:'first',pattern:'ERROR',regex:false,enabled:true},{name:'second',pattern:'ERROR',regex:true,enabled:true}]});
   return {bad,unicode,precedence};
  });
  assert(check.bad.error);assert.deepEqual(check.unicode.matches,[{line:0,start:2,end:4,rule:0}]);assert.equal(check.precedence.matches.length,1);assert.equal(check.precedence.matches[0].rule,0);
  await page.evaluate(()=>applyAppearance(appearanceDefaults));
  // 50,000 retained rows must not become a 50,000-row highlight scan.
  await page.evaluate(async()=>{
   const request=highlightEngine.request;
   highlightEngine.request=function(owner,payload){window.lastHighlightScan=payload.lines;return request.call(this,owner,payload);};
   terminal.reset();terminal.resize(80,24);terminal.options.scrollback=50000;
   await new Promise(r=>terminal.write(Array.from({length:50020},(_,i)=>'ERROR history '+i+'\r\n').join(''),r));
  });
  await page.waitForFunction(()=>[...terminalHighlights].find(h=>h.term===terminal).decorations.length>0);
  assert.equal(await page.evaluate(()=>terminal.buffer.active.length>=50000),true);
  assert.equal(await page.evaluate(()=>lastHighlightScan.length<=terminal.rows),true);
  assert.equal(await page.evaluate(()=>[...terminalHighlights].find(h=>h.term===terminal).markers.every(m=>m.line>=terminal.buffer.active.viewportY)),true);
  // Native wheel scrolling suppresses xterm's public onScroll notification.
  const viewport=page.locator('#terminal .xterm-viewport');await page.locator('#terminal .xterm-screen').hover();
  const oldView=await page.evaluate(()=>terminal.buffer.active.viewportY);
  await page.mouse.wheel(0,-900);
  await page.waitForFunction(old=>terminal.buffer.active.viewportY<old,oldView);
  await page.waitForFunction(()=>{
   const h=[...terminalHighlights].find(h=>h.term===terminal),view=terminal.buffer.active.viewportY;
   return h.decorations.length>0&&h.markers.some(m=>m.line===view)&&h.markers.every(m=>m.line>=view&&m.line<view+terminal.rows);
  },null,{timeout:3000});
  const renderedRed=()=>{
   const row=terminal.element.querySelector('.xterm-rows')?.firstElementChild;if(!row||!row.textContent.startsWith('ERROR'))return false;
   const colors=[...row.querySelectorAll('span')].flatMap(el=>[...el.textContent].map(()=>getComputedStyle(el).color));
   return colors.slice(0,5).length===5&&colors.slice(0,5).every(color=>color==='rgb(255, 133, 133)');
  };
  await page.waitForFunction(renderedRed);
  // Give headless Chromium a conventional native scrollbar with a known thumb.
  await page.addStyleTag({content:'#terminal .xterm-viewport::-webkit-scrollbar{width:14px}#terminal .xterm-viewport::-webkit-scrollbar-thumb{background:#999;min-height:20px}#terminal .xterm-viewport::-webkit-scrollbar-button{display:none;width:0;height:0}'});
  // Drag the native scrollbar thumb to a different historical viewport.
  const scroll=await viewport.evaluate(el=>{const b=el.getBoundingClientRect();return {x:b.x+b.width-5,y:b.y,h:b.height,top:el.scrollTop,total:el.scrollHeight,client:el.clientHeight};});
  const thumb=Math.max(20,scroll.h*scroll.client/scroll.total),thumbY=scroll.y+(scroll.h-thumb)*scroll.top/(scroll.total-scroll.client)+thumb/2;
  const beforeDrag=await page.evaluate(()=>terminal.buffer.active.viewportY);
  await page.mouse.move(scroll.x,thumbY);await page.mouse.down();await page.mouse.move(scroll.x,scroll.y+scroll.h*.4,{steps:6});await page.mouse.up();
  await page.waitForFunction(before=>terminal.buffer.active.viewportY!==before,beforeDrag);
  await page.waitForFunction(renderedRed);
  assert.equal(await page.evaluate(()=>lastHighlightScan.length<=terminal.rows),true);
  await page.evaluate(()=>terminal.scrollToTop());
  await page.waitForFunction(()=>[...terminalHighlights].find(h=>h.term===terminal).markers.some(m=>m.line===0));
  assert.equal(await page.evaluate(()=>[...terminalHighlights].find(h=>h.term===terminal).markers.every(m=>m.line<terminal.rows)),true);
  await page.locator('#overview-toggle').click();await page.waitForFunction(()=>typeof overview!=='undefined'&&overview);
  await page.evaluate(async()=>{const c=[...overview.cards.values()].find(c=>c.terminal);await new Promise(r=>c.terminal.write('\r\nERROR 10.0.0.1',r));});
  await page.waitForFunction(()=>[...terminalHighlights].some(h=>[...overview.cards.values()].some(c=>c.terminal===h.term)&&h.decorations.length>0));
  await page.evaluate(async()=>{const c=[...overview.cards.values()].find(c=>c.terminal);await new Promise(r=>c.terminal.write(Array.from({length:100},(_,i)=>'ERROR card '+i+'\r\n').join(''),r));});
  const cardScreen=page.locator('#overview .xterm-screen').first();await cardScreen.hover();
  const cardBefore=await page.evaluate(()=>[...overview.cards.values()].find(c=>c.terminal).terminal.buffer.active.viewportY);
  await page.mouse.wheel(0,-300);
  await page.waitForFunction(before=>[...overview.cards.values()].find(c=>c.terminal).terminal.buffer.active.viewportY<before,cardBefore);
  await page.waitForFunction(()=>{
   const t=[...overview.cards.values()].find(c=>c.terminal).terminal,row=t.element.querySelector('.xterm-rows')?.firstElementChild;
   const h=[...terminalHighlights].find(h=>h.term===t);
   return row?.textContent.startsWith('ERROR')&&[...row.querySelectorAll('span')].slice(0,5).every(el=>getComputedStyle(el).color==='rgb(255, 133, 133)')&&h.markers.some(m=>m.line===t.buffer.active.viewportY);
  });
  await page.evaluate(()=>{[...overview.cards.values()][0].element.style.display='none';});
  await page.waitForFunction(()=>[...terminalHighlights].every(h=>h.term===previewTerminal||h.decorations.length===0));
  await page.evaluate(()=>{[...overview.cards.values()][0].element.style.display='';});
  await page.waitForFunction(()=>[...terminalHighlights].some(h=>h.visible&&h.decorations.length>0));
  await page.screenshot({path:'/tmp/keepsess-appearance.png',fullPage:true});
  assert.deepEqual(errors,[]);console.log('appearance passed: theme, CJK cells, ANSI, zero errors, chunks, repaint, wrap, raw selection, preview, persistence, native Worker, syntax validation, Unicode offsets, priority, 50000-row viewport-only highlighting, overview');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
