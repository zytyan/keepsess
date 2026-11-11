const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
const records=Number(process.argv[3]||1200);
(async()=>{const browser=await launch({headless:true,args:['--no-sandbox']});try{
 const page=await browser.newPage({viewport:{width:1400,height:1000}});const errors=[];let snapshots=0;page.on('pageerror',e=>errors.push(e.message));page.on('response',r=>{if(r.url().includes('/api/replay?'))snapshots++;});
 await page.addInitScript(()=>{window.replayHighlightScans=0;document.addEventListener('DOMContentLoaded',()=>{const scan=TerminalHighlight.prototype.scan;TerminalHighlight.prototype.scan=function(){if(this.term.keepsessReplaying)window.replayHighlightScans++;return scan.call(this);};});localStorage.setItem('keepsess.scrollback','50000');window.replayWrites=0;window.historyWasHidden=true;let terminalClass;Object.defineProperty(window,'Terminal',{configurable:true,get:()=>terminalClass,set:value=>{terminalClass=value;const write=value.prototype.write;value.prototype.write=function(data,callback){window.replayWrites++;window.historyWasHidden&&=getComputedStyle(document.getElementById('terminal')).visibility==='hidden';return write.call(this,data,callback);};}});});
 const started=Date.now();await page.goto(process.argv[2]);await page.waitForFunction(()=>typeof replaying!=='undefined'&&!replaying&&typeof terminal!=='undefined'&&terminal?.buffer.active.length>1000);
 const stats=await page.evaluate(()=>({highlightScans:window.replayHighlightScans,writes:window.replayWrites,hidden:window.historyWasHidden,text:Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n')}));
 assert.match(stats.text,/row 0000 中文/);assert(stats.text.includes('row '+String(records-1).padStart(4,'0')+' 中文'));assert.equal(stats.highlightScans,0);const replayMs=Date.now()-started;assert(stats.writes<=Math.ceil(records*200/65536)+6,`expected bounded batched xterm writes, got ${stats.writes}`);assert.equal(stats.hidden,true);assert.equal(snapshots,1);assert.deepEqual(errors,[]);
 // Fingerprinted font bytes must be served from browser cache on reload.
 await page.reload();await page.waitForFunction(()=>typeof replaying!=='undefined'&&!replaying&&typeof terminal!=='undefined'&&terminal?.buffer.active.length>1000);
 const fonts=await page.evaluate(()=>performance.getEntriesByType('resource').filter(r=>r.name.includes('/fonts/')&&/\.(ttf|woff2)$/.test(r.name)).map(r=>({name:r.name,transfer:r.transferSize,body:r.decodedBodySize})));
 assert.equal(fonts.length,2);assert(fonts.every(f=>f.transfer<=2000),JSON.stringify(fonts));
 console.log(JSON.stringify({records,replayMs,highlightScansDuringReplay:stats.highlightScans,snapshots:1,xtermWrites:stats.writes,hiddenUntilReady:stats.hidden,cachedFontTransferBytes:fonts.reduce((n,f)=>n+f.transfer,0)}));
 }finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});
