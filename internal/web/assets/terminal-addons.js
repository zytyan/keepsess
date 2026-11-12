'use strict';
// Keep all rendering in xterm's renderer. DOM is a fallback for disabled GPUs
// and lost contexts; no output is replayed when changing renderers.
function attachTerminalRenderer(term){
 term.keepsessRenderer='dom';
 let addon=null,loss=null;
 const fallback=()=>{
  loss?.dispose();loss=null;
  const old=addon;addon=null;
  try{old?.dispose();}catch(_){}
  term.keepsessRenderer='dom';
  const h=[...terminalHighlights].find(h=>h.term===term);
  if(h){h.dispose();attachTerminalAppearance(term);}
  term.refresh(0,term.rows-1);
 };
 try{
  addon=new WebglAddon.WebglAddon();
  term.loadAddon(addon);term.keepsessRenderer='webgl';
  loss=addon.onContextLoss(fallback);
 }catch(_){fallback();}
 term.register({dispose:()=>{loss?.dispose();loss=null;}});
 return term.keepsessRenderer;
}
class WebGLKeywordLayer{
 static supported(term){return term.keepsessRenderer==='webgl';}
 constructor(term){this.term=term;this.rules=[];this.items=[];this.last=null;}
 configure(rules){this.clear();this.rules=rules;}
 clear(refresh=true){const changed=this.items.length>0;for(const d of this.items)d.dispose();this.items=[];this.last=null;if(changed&&refresh)this.term.refresh(0,this.term.rows-1);}
 paint(spans){
  if(this.last===spans)return;
  this.clear(false);this.last=spans;
  for(const span of spans){const r=this.rules[span.rule];if(!r)continue;
   const d=this.term.registerDecoration({marker:span.marker,x:span.options.x,width:span.options.width,foregroundColor:r.foreground||undefined,backgroundColor:r.background||undefined,layer:'bottom'});
   if(d)this.items.push(d);
  }
  this.term.refresh(0,this.term.rows-1);
 }
 dispose(){this.clear();}
}
let terminalSearch=null,terminalQuery=null;
function attachTerminalSearch(term){terminalSearch=new SearchAddon.SearchAddon({highlightLimit:1000});term.loadAddon(terminalSearch);terminalQuery=new TerminalQuery(term,terminalSearch);term.register({dispose:()=>terminalQuery?.term===term&&terminalQuery.dispose()});}
function searchTerminal(backwards=false){terminalQuery?.navigate(backwards?-1:1);}
