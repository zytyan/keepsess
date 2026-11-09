const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
(async()=>{const browser=await launch({headless:true,args:['--no-sandbox']});try{
 const page=await browser.newPage({viewport:{width:1400,height:1000}});const errors=[];let snapshots=0;page.on('pageerror',e=>errors.push(e.message));page.on('response',r=>{if(r.url().includes('/api/replay?'))snapshots++;});
 await page.addInitScript(()=>{window.replayWrites=0;window.historyWasHidden=true;let terminalClass;Object.defineProperty(window,'Terminal',{configurable:true,get:()=>terminalClass,set:value=>{terminalClass=value;const write=value.prototype.write;value.prototype.write=function(data,callback){window.replayWrites++;window.historyWasHidden&&=getComputedStyle(document.getElementById('terminal')).visibility==='hidden';return write.call(this,data,callback);};}});});
 await page.goto(process.argv[2]);await page.waitForFunction(()=>typeof replaying!=='undefined'&&!replaying&&typeof terminal!=='undefined'&&terminal?.buffer.active.length>1000);
 const stats=await page.evaluate(()=>({writes:window.replayWrites,hidden:window.historyWasHidden,text:Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n')}));
 assert.match(stats.text,/row 0000 中文/);assert.match(stats.text,/row 1199 中文/);assert(stats.writes<=2,`expected bulk xterm writes, got ${stats.writes}`);assert.equal(stats.hidden,true);assert.equal(snapshots,1);assert.deepEqual(errors,[]);
 // Fingerprinted font bytes must be served from browser cache on reload.
 await page.reload();await page.waitForFunction(()=>typeof replaying!=='undefined'&&!replaying&&typeof terminal!=='undefined'&&terminal?.buffer.active.length>1000);
 const fonts=await page.evaluate(()=>performance.getEntriesByType('resource').filter(r=>r.name.includes('/fonts/')&&/\.(ttf|woff2)$/.test(r.name)).map(r=>({name:r.name,transfer:r.transferSize,body:r.decodedBodySize})));
 assert.equal(fonts.length,2);assert(fonts.every(f=>f.transfer<=2000),JSON.stringify(fonts));
 console.log(JSON.stringify({records:1200,snapshots:1,xtermWrites:stats.writes,hiddenUntilReady:stats.hidden,cachedFontTransferBytes:fonts.reduce((n,f)=>n+f.transfer,0)}));
 }finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});
