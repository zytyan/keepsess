const assert=require('node:assert/strict');
// Check the browser's actual rendered fonts, not just a CSS family string.
module.exports=async function verifyTypography(page){
 assert.equal(await page.evaluate(()=>terminalFontsLoaded),true,'bundled fonts did not load');
 const families=await page.evaluate(()=>['body','#title','#mode'].map(selector=>getComputedStyle(document.querySelector(selector)).fontFamily));
 for(const family of families){assert.match(family,/Noto Sans SC/);assert.match(family,/sans-serif$/);}
 assert.equal(await page.evaluate(()=>terminal.options.fontFamily===WEB_FONT_FAMILY),true);
 await page.evaluate(()=>{const probe=document.createElement('div');probe.id='font-probe';probe.style.cssText='position:fixed;left:0;top:0;opacity:0;pointer-events:none;font-family:'+WEB_FONT_FAMILY;for(const[id,text]of [['latin','Keepsess English 0123456789'],['cjk','中文终端会话历史']]){const span=document.createElement('span');span.id='font-probe-'+id;span.textContent=text;probe.append(span);}document.body.append(probe);});
 await page.evaluate(()=>document.getElementById('font-probe').getBoundingClientRect());
 await page.evaluate(()=>new Promise(requestAnimationFrame));
 const cdp=await page.context().newCDPSession(page);
 try{
  await cdp.send('DOM.enable');await cdp.send('CSS.enable');const {root}=await cdp.send('DOM.getDocument');
  for(const [id,expected]of [['latin',/JetBrains Mono/],['cjk',/Noto Sans SC/]]){
   const {nodeId}=await cdp.send('DOM.querySelector',{nodeId:root.nodeId,selector:'#font-probe-'+id});
   const {fonts}=await cdp.send('CSS.getPlatformFontsForNode',{nodeId});
   assert(fonts.some(f=>expected.test(f.familyName)&&f.isCustomFont&&f.glyphCount>0),JSON.stringify({id,fonts}));
  }
 }finally{await cdp.detach();await page.evaluate(()=>document.getElementById('font-probe')?.remove());}
};
