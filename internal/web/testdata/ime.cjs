const assert=require('node:assert/strict');
module.exports=async function(page){
 const cdp=await page.context().newCDPSession(page);
 const sent=()=>page.evaluate(()=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join(''));
 const compose=async text=>{await cdp.send('Input.imeSetComposition',{text,selectionStart:text.length,selectionEnd:text.length});};
 try{
  await page.locator('#terminal .xterm-helper-textarea').focus();
  const before=await sent();
  await compose('ni');await compose('你好');
  await page.waitForTimeout(30);assert.equal(await sent(),before,'candidate text must not be sent');
  await page.evaluate(()=>terminal.textarea.dispatchEvent(new CompositionEvent('compositionend',{data:'伪造文本',bubbles:true})));
  assert.equal(await sent(),before,'an untrusted end must not inject a different candidate');
  await cdp.send('Input.insertText',{text:'你好'});
  await page.waitForFunction(before=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join('')===before+'你好',before);
  await page.waitForFunction(()=>!operationPending&&!inputQueue.length);
  await compose('nihao');await compose('你好');await cdp.send('Input.insertText',{text:'你好'});
  await page.waitForFunction(before=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join('')===before+'你好你好',before);
  await page.waitForFunction(()=>!operationPending&&!inputQueue.length);
  await compose('quxiao');await compose('');await page.waitForTimeout(30);
  assert.equal(await sent(),before+'你好你好','cancel must not send candidate text');
  await page.keyboard.insertText('你好');await page.keyboard.type('abc');await page.keyboard.press('Control+j');
  await page.waitForFunction(before=>window.sent.filter(m=>m.type==='input').map(m=>m.data).join('')===before+'你好你好你好abc\n',before);
  await page.waitForFunction(()=>!operationPending&&!inputQueue.length);
  await page.evaluate(()=>{const t=terminal.textarea;t.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));t.dispatchEvent(new InputEvent('input',{inputType:'insertCompositionText',data:'伪造',isComposing:true,bubbles:true}));t.dispatchEvent(new CompositionEvent('compositionend',{data:'伪造',bubbles:true}));});
  await page.waitForTimeout(30);assert.equal(await sent(),before+'你好你好你好abc\n','synthetic events must not send text');
  await page.locator('#unlock').click();await page.waitForFunction(()=>!unlocked);
  await page.locator('#terminal .xterm-helper-textarea').focus();await compose('只读');await cdp.send('Input.insertText',{text:'只读'});
  await page.waitForTimeout(30);assert.equal(await sent(),before+'你好你好你好abc\n','readonly IME must not send');
  await page.locator('#unlock').click();await page.waitForFunction(()=>unlocked);
  // Exercise the same trusted IME path with a DOM-only terminal too.
  await page.evaluate(async()=>{const host=document.createElement('div');host.id='ime-fixture';host.style.cssText='position:fixed;left:300px;top:150px;width:600px;height:150px;z-index:80';document.body.append(host);window.imeTerm=new Terminal({...TERMINAL_APPEARANCE,fontFamily:WEB_FONT_FAMILY});window.imeCaptured=[];imeTerm.open(host);attachTerminalInput(imeTerm,text=>imeCaptured.push(text));imeTerm.focus();});
  try{await compose('zhongwen');await compose('中文');await cdp.send('Input.insertText',{text:'中文'});await page.waitForFunction(()=>imeCaptured.join('')==='中文');assert.equal(await page.evaluate(()=>imeCaptured.length),1);}finally{await page.evaluate(()=>{imeTerm.dispose();document.getElementById('ime-fixture').remove();delete window.imeTerm;delete window.imeCaptured;});}
 }finally{await cdp.detach();}
};
