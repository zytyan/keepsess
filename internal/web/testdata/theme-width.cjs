const assert=require('node:assert/strict');
module.exports=async function(page){
 assert.equal(await page.getAttribute('html','data-theme'),'dark');
 await page.locator('#appearance-open').click();
 await page.selectOption('#theme-mode','light');
 assert.equal(await page.getAttribute('html','data-theme'),'light');
 assert.equal(await page.evaluate(()=>terminal.options.theme.background),'#ffffff');
 assert.equal(await page.evaluate(()=>previewTerminal.options.theme.background),'#ffffff');
 if(process.env.KEEPSESS_THEME_SCREENSHOT)await page.screenshot({path:process.env.KEEPSESS_THEME_SCREENSHOT,fullPage:true});
 const restored=await page.context().newPage();
 try{await restored.goto(page.url());await restored.waitForFunction(()=>document.documentElement.dataset.theme==='light');}finally{await restored.close();}
 await page.selectOption('#theme-mode','system');
 await page.emulateMedia({colorScheme:'dark'});
 await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');
 assert.equal(await page.getAttribute('html','data-theme'),'dark');
 await page.emulateMedia({colorScheme:'light'});
 await page.waitForFunction(()=>document.documentElement.dataset.theme==='light');
 assert.equal(await page.getAttribute('html','data-theme'),'light');
 await page.selectOption('#theme-mode','dark');
 await page.locator('#appearance-close').click();
 await page.locator('#instance-selected').click();
 const positioned=await page.evaluate(()=>{const row=document.querySelector('.instance-option-row'),icon=row.querySelector('.instance-rename'),a=row.getBoundingClientRect(),b=icon.getBoundingClientRect();return b.left>a.left+a.width/2&&b.right<=a.right&&b.top>=a.top&&b.bottom<=a.bottom&&!icon.textContent.trim()&&!!icon.querySelector('svg');});
 assert(positioned,'rename icon must be inside the right edge of its dropdown row');
 const prior=await page.evaluate(()=>instance),archive=await page.evaluate(()=>instanceItems.find(i=>!i.current).id);
 await page.locator('.instance-option-row').last().locator('.instance-rename').click();
 assert.equal(await page.evaluate(()=>instance),prior,'rename must not select another instance');
 assert.equal(await page.evaluate(()=>namingInstance),archive,'rename must target the clicked instance');
 await page.locator('#instance-name-cancel').click();
 // Simulate a machine where offscreen font metrics report double width.
 // The DOM renderer must use its own CSS pixel metrics at every scale.
 const result=await page.evaluate(async()=>{
  const proto=globalThis.OffscreenCanvasRenderingContext2D?.prototype,original=proto?.measureText;
  if(proto)proto.measureText=function(text){const m=original.call(this,text);return {width:m.width*2,fontBoundingBoxAscent:m.fontBoundingBoxAscent,fontBoundingBoxDescent:m.fontBoundingBoxDescent};};
  const host=document.createElement('div');host.style.cssText='position:fixed;left:0;top:0;width:800px;height:200px';document.body.append(host);
  const term=new Terminal({...TERMINAL_APPEARANCE,cols:40,rows:5,fontFamily:WEB_FONT_FAMILY,fontSize:14});
  try{
   term.open(host);await new Promise(resolve=>term.write('ASCII 0123 中文',resolve));
   await new Promise(requestAnimationFrame);
   const probe=host.querySelector('.xterm-char-measure-element');
   return {probe:!!probe,cell:host.querySelector('.xterm-screen').getBoundingClientRect().width/40,measured:probe?.offsetWidth/32,text:term.buffer.active.getLine(0).translateToString(true)};
  }finally{term.dispose();host.remove();if(proto)proto.measureText=original;}
 });
 assert(result.probe,'cell measurement must use the DOM path');
 const renderer=await page.evaluate(()=>terminal.keepsessRenderer);
 assert(['webgl','dom'].includes(renderer));
 await page.evaluate(()=>{document.getElementById('search-text').value='out';searchTerminal();});
 assert(await page.evaluate(()=>terminal.getSelection().includes('out')),'official search addon must select a retained match');
 await page.locator('#search-toggle').click();await page.locator('#search-clear').click();await page.locator('#search-toggle').click();
 assert.equal(await page.evaluate(()=>terminal.getSelection()),'');
 if(renderer==='webgl'){
  assert(await page.locator('#terminal canvas').count()>0);
  await page.evaluate(async()=>{
   const host=document.createElement('div');host.style.cssText='position:fixed;left:0;top:0;width:600px;height:120px';document.body.append(host);
   const term=new Terminal({...TERMINAL_APPEARANCE,fontFamily:WEB_FONT_FAMILY});term.open(host);attachTerminalRenderer(term);attachTerminalAppearance(term);
   window.contextLossFixture={host,term};
   await new Promise(resolve=>term.write('retained after context loss',resolve));
   const gl=[...host.querySelectorAll('canvas')].map(c=>c.getContext('webgl2')).find(Boolean);
   if(!gl)throw Error('WebGL renderer has no context');gl.getExtension('WEBGL_lose_context').loseContext();
  });
  await page.waitForFunction(()=>contextLossFixture.term.keepsessRenderer==='dom');
  assert.equal(await page.evaluate(()=>contextLossFixture.term.buffer.active.getLine(0).translateToString(true)),'retained after context loss');
  await page.evaluate(()=>{contextLossFixture.term.dispose();contextLossFixture.host.remove();delete window.contextLossFixture;});
 }
 console.log('theme/width/search renderer:',renderer);
 assert(Math.abs(result.cell-result.measured)<1,JSON.stringify(result));
 assert.equal(result.text,'ASCII 0123 中文');
};
