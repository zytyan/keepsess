const assert=require('node:assert/strict');
module.exports=async function(page){
 await page.context().grantPermissions(['clipboard-read','clipboard-write']);
 async function check(expression){
  const expected=await page.evaluate(expression=>{const term=eval(expression);const b=term.buffer.active;let y=0;while(y<b.length&&b.getLine(y).translateToString(true).trim().length<8)y++;term.select(0,y,8);return term.getSelection();},expression);
  assert(expected);
  const target=page.locator(expression==='terminal'?'#terminal .xterm-screen':'#menu-fixture .xterm-screen');
  await target.click({button:'right',position:{x:10,y:10}});
  const menu=page.locator('.terminal-context-menu:not([hidden])');await menu.waitFor();
  assert.equal(await page.evaluate(expression=>eval(expression).getSelection(),expression),expected,'right click must preserve selected text');
  await menu.getByRole('menuitem',{name:'复制',exact:true}).click();
  await page.waitForFunction(()=>document.getElementById('status').textContent==='已复制选中的终端文本。');
  assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),expected);
  await page.evaluate(expression=>eval(expression).clearSelection(),expression);
  await target.click({button:'right',position:{x:10,y:10}});
  await menu.waitFor();assert(await menu.getByRole('menuitem',{name:'复制',exact:true}).isDisabled());
  await menu.getByRole('menuitem',{name:'全选',exact:true}).click();
  assert(await page.evaluate(expression=>eval(expression).getSelection().length>8,expression));
  await target.click({button:'right',position:{x:10,y:10}});await menu.waitFor();
  await page.keyboard.press('Escape');assert.equal(await menu.count(),0);
 }
 const inputs=await page.evaluate(()=>window.sent.filter(m=>m.type==='input').length);
 await check('terminal');
 await page.evaluate(async()=>{const host=document.createElement('div');host.id='menu-fixture';host.style.cssText='position:fixed;left:300px;top:200px;width:600px;height:150px;z-index:80;background:#0a1019';document.body.append(host);window.menuTerm=new Terminal({...TERMINAL_APPEARANCE,fontFamily:WEB_FONT_FAMILY,cols:60,rows:5});menuTerm.open(host);attachTerminalContextMenu(menuTerm);await new Promise(r=>menuTerm.write('中文 copy exact text\r\nsecond line',r));});
 try{await check('menuTerm');}finally{await page.evaluate(()=>{menuTerm.dispose();document.getElementById('menu-fixture').remove();delete window.menuTerm;});}
 assert.equal(await page.evaluate(()=>window.sent.filter(m=>m.type==='input').length),inputs,'menu actions must never send input');
 await page.evaluate(()=>terminal.clearSelection());
};
