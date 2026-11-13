const assert=require('node:assert/strict');
module.exports=async page=>{
 await page.waitForFunction(()=>!replaying&&socket?.readyState===1&&!document.getElementById('mode').disabled);
 assert.equal(await page.evaluate(()=>humanPage),false);
 assert.equal(await page.locator('#mode').getAttribute('aria-pressed'),'true');
 assert.equal(await page.evaluate(()=>terminal.options.disableStdin),true);
 const before=await page.evaluate(()=>sent.filter(m=>m.type==='input').length);
 await page.locator('.xterm-helper-textarea').focus();await page.keyboard.type('blocked');
 assert.equal(await page.evaluate(()=>sent.filter(m=>m.type==='input').length),before);
 // Session read_only remains authoritative even on a live stream page.
 await page.evaluate(()=>{sessions.get(selected).read_only=true;updateControls();});
 assert(await page.locator('#mode').isDisabled());
 await page.evaluate(()=>{sessions.get(selected).read_only=false;updateControls();});
 await page.locator('#mode').click();await page.waitForFunction(()=>unlocked);
 assert.equal(await page.locator('#mode').textContent(),'只读');
 assert.equal(await page.locator('#mode').getAttribute('aria-pressed'),'false');
 assert.equal(await page.evaluate(()=>terminal.options.disableStdin),false);
 await page.locator('.xterm-helper-textarea').focus();await page.keyboard.type('show version');await page.keyboard.press('Enter');
 await page.waitForFunction(()=>!operationPending&&!inputQueue.length);
 // The simulators deliberately require LF; verify Enter stays a raw CR.
 assert.equal(await page.evaluate(n=>sent.filter(m=>m.type==='input').slice(n).map(m=>m.data).join(''),before),'show version\r');
 await page.keyboard.press('Control+j');
 await page.waitForFunction(()=>!operationPending&&!inputQueue.length&&Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes('SwitchOS 1.0'));
 assert.equal(await page.evaluate(n=>sent.filter(m=>m.type==='input').slice(n).map(m=>m.data).join(''),before),'show version\r\n');
 await page.locator('#mode').click();await page.waitForFunction(()=>!unlocked);
 assert.equal(await page.evaluate(()=>terminal.options.disableStdin),true);
 await page.locator('.xterm-helper-textarea').focus();await page.keyboard.type('blocked');
 assert.equal(await page.evaluate(n=>sent.filter(m=>m.type==='input').slice(n).map(m=>m.data).join(''),before),'show version\r\n');
 // Selecting even the same session creates a fresh, locked socket.
 await page.locator('#mode').click();await page.waitForFunction(()=>unlocked);
 await page.evaluate(()=>selectSession(selected));
 await page.waitForFunction(()=>!replaying&&socket?.readyState===1&&!document.getElementById('mode').disabled);
 assert.equal(await page.evaluate(()=>unlocked),false);
 assert.equal(await page.locator('#mode').getAttribute('aria-pressed'),'true');
};
