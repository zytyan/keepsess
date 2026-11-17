const assert=require('node:assert/strict');
module.exports=async page=>{
 const wasUnlocked=await page.evaluate(()=>unlocked);
 const all=()=>page.evaluate(()=>sent.filter(m=>m.type==='input').map(m=>m.data).join(''));
 const idle=()=>page.waitForFunction(()=>!operationPending&&!inputQueue.length);
 const choose=async mode=>{await page.locator('#appearance-open').click();await page.locator('#backspace-mode').selectOption(mode);await page.locator('#appearance-close').click();await page.locator('#terminal .xterm-helper-textarea').focus();};
 await idle();
 if(!wasUnlocked){await page.locator('#mode').click();await page.waitForFunction(()=>unlocked);}
 const press=async key=>{const before=await all();await page.keyboard.press(key);await idle();return (await all()).slice(before.length);};
 await choose('default');
 const keys=['Backspace','Shift+Backspace','Control+Backspace','Alt+Backspace','Meta+Backspace','Delete'];
 const defaults=[];for(const key of keys)defaults.push(await press(key));
 for(const mode of ['bs','del']){
  await choose(mode);
  for(let i=0;i<keys.length;i++)assert.equal(await press(keys[i]),i===0?(mode==='bs'?'\x08':'\x7f'):defaults[i],mode+' '+keys[i]);
 }
 await page.context().grantPermissions(['clipboard-read','clipboard-write']);
 await page.evaluate(()=>navigator.clipboard.writeText('paste\x08\x7ftext'));
 const beforePaste=await all(),cdp=await page.context().newCDPSession(page);
 try{await cdp.send('Input.dispatchKeyEvent',{type:'keyDown',commands:['Paste']});await idle();assert.equal((await all()).slice(beforePaste.length),'paste\x08\x7ftext','paste preserves control bytes');}finally{await cdp.detach();}
 await page.locator('#mode').click();await page.waitForFunction(()=>!unlocked);
 const locked=await all();await page.locator('#terminal .xterm-helper-textarea').focus();await page.keyboard.press('Backspace');await page.waitForTimeout(30);assert.equal(await all(),locked,'readonly Backspace');
 // The preference validator is also used for imports; old v1 exports stay valid.
 await page.evaluate(()=>{
  const old=structuredClone(preferences);delete old.backspaceMode;
  if(validatePreferences(old).backspaceMode!=='default')throw Error('old preference default');
  for(const invalid of ['BS','',null,7]){let rejected=false;try{validatePreferences({...old,backspaceMode:invalid});}catch(e){rejected=true;}if(!rejected)throw Error('invalid backspace mode accepted');}
 });
 const peer=await page.context().newPage();
 try{
  await peer.goto(page.url());await peer.waitForFunction(()=>typeof preferences!=='undefined');
  assert.equal(await peer.evaluate(()=>preferences.backspaceMode),'del','preference survives new page');
  await peer.reload();await peer.waitForFunction(()=>typeof preferences!=='undefined'&&preferences.backspaceMode==='del');
  await choose('bs');await peer.waitForFunction(()=>preferences.backspaceMode==='bs');
  await page.locator('#appearance-open').click();await page.locator('[data-settings=config]').click();
  const download=page.waitForEvent('download');await page.locator('#preferences-export').click();
  const exported=JSON.parse(await require('node:fs/promises').readFile(await (await download).path(),'utf8'));
  assert.equal(exported.backspaceMode,'bs');exported.backspaceMode='del';
  await page.locator('#preferences-import').setInputFiles({name:'backspace.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(exported))});
  await page.waitForFunction(()=>preferences.backspaceMode==='del');await peer.waitForFunction(()=>preferences.backspaceMode==='del');
  await page.locator('#preferences-reset').click();await page.waitForFunction(()=>preferences.backspaceMode==='default');await peer.waitForFunction(()=>preferences.backspaceMode==='default');
  await page.locator('[data-settings=general]').click();await page.locator('#appearance-close').click();
 }finally{await peer.close();}
 if(wasUnlocked){await page.locator('#mode').click();await page.waitForFunction(()=>unlocked);}
 console.log('backspace passed: '+await page.evaluate(()=>humanPage?'terminal tab':sessions.get(selected).protocol));
};
