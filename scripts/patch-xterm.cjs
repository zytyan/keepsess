// Reapply keepsess's render notification patch to upstream xterm.js 5.5.0.
// No build-time dependency: the patched asset is committed and embedded by Go.
const fs=require('node:fs'),path=require('node:path');
const file=path.join(__dirname,'../internal/web/assets/vendor/xterm.js');
const changes=[
 ['this.register(this._renderService.onRenderedViewportChange((e=>this._onRender.fire(e))))','this.register(this._renderService.onRender((e=>this._onRender.fire(e))))'],
 ['this._widthCache,-1,-1))}}get _terminalSelector()', 'this._widthCache,-1,-1))}this._terminal._onRender.fire({start:e,end:t})}get _terminalSelector()'],
 ['n?(o===s?t:r)-1:-1))}}};t.DomRenderer=', 'n?(o===s?t:r)-1:-1))}this._terminal._onRender.fire({start:i,end:s})}};t.DomRenderer='],
 ['renderRows(e,t){const i=this._bufferService.buffer,s=i.ybase+i.y', 'renderRows(e,t){if(this._terminal._keepsessDOMRenderFilter&&!this._terminal._keepsessDOMRenderFilter(e,t))return;const i=this._bufferService.buffer,s=i.ybase+i.y'],
 ['r=Math.min(r,this._bufferService.cols);const a=this._bufferService.buffer,h=a.ybase+a.y', 'r=Math.min(r,this._bufferService.cols);if(this._terminal._keepsessDOMRenderFilter&&!this._terminal._keepsessDOMRenderFilter(i,s))return;const a=this._bufferService.buffer,h=a.ybase+a.y'],
 ['get onRender(){return this._core.onRender}', 'get onRender(){return this._core.onRender}registerDOMRenderFilter(e){const t=this._core;if(t._keepsessDOMRenderFilter)throw Error("DOM render filter already registered");t._keepsessDOMRenderFilter=e;return{dispose:()=>{if(t._keepsessDOMRenderFilter===e)t._keepsessDOMRenderFilter=void 0}}}'],
];
let source=fs.readFileSync(file,'utf8');
for(const [before,after] of changes){
 if(source.split(after).length===2)continue;
 if(source.split(before).length!==2)throw Error('Unexpected xterm build: inspect render notifications before patching');
 source=source.replace(before,after);
}
fs.writeFileSync(file,source);console.log('xterm render notification patch applied (idempotent)');
