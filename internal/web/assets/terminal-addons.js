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
 clear(){for(const d of this.items)d.dispose();this.items=[];this.last=null;}
 paint(spans){
  if(this.last===spans)return;
  this.clear();this.last=spans;
  for(const span of spans){const r=this.rules[span.rule];if(!r)continue;
   const d=this.term.registerDecoration({marker:span.marker,x:span.options.x,width:span.options.width,foregroundColor:r.foreground||undefined,backgroundColor:r.background||undefined,layer:'bottom'});
   if(d)this.items.push(d);
  }
 }
 dispose(){this.clear();}
}
let terminalSearch=null;
function attachTerminalSearch(term){
 terminalSearch=new SearchAddon.SearchAddon({highlightLimit:1000});term.loadAddon(terminalSearch);
 const owned=terminalSearch;
 owned.onDidChangeResults(({resultIndex,resultCount})=>{if(terminalSearch===owned)document.getElementById('search-status').textContent=resultCount?`${resultIndex+1} / ${resultCount}`:'无匹配';});
 term.register({dispose:()=>{if(terminalSearch===owned){terminalSearch=null;document.getElementById('search-status').textContent='';}}});
}
function searchTerminal(backwards=false){
 if(!terminalSearch)return;
 const text=document.getElementById('search-text').value;
 if(!text){terminalSearch.clearDecorations();terminal?.clearSelection();document.getElementById('search-status').textContent='';return;}
 const light=document.documentElement.dataset.theme==='light';
 const options={caseSensitive:document.getElementById('search-case').checked,decorations:{matchBackground:light?'#f5df9e':'#665124',activeMatchBackground:light?'#efbd55':'#8c5f1c',matchOverviewRuler:'#c69435',activeMatchColorOverviewRuler:'#e7a51c'}};
 const found=backwards?terminalSearch.findPrevious(text,options):terminalSearch.findNext(text,options);
 if(!found)document.getElementById('search-status').textContent='无匹配';
}
document.getElementById('terminal-search').addEventListener('submit',event=>{event.preventDefault();searchTerminal();});
document.getElementById('search-prev').onclick=()=>searchTerminal(true);
document.getElementById('search-clear').onclick=()=>{document.getElementById('search-text').value='';searchTerminal();};
document.getElementById('search-text').addEventListener('keydown',event=>{if(event.key==='Enter'&&event.shiftKey){event.preventDefault();searchTerminal(true);}if(event.key==='Escape'){document.getElementById('search-clear').click();document.getElementById('terminal-search').hidden=true;terminal?.focus();}});
document.addEventListener('keydown',event=>{if((event.ctrlKey||event.metaKey)&&event.key.toLowerCase()==='f'&&!document.querySelector('dialog[open]')){event.preventDefault();document.getElementById('terminal-search').hidden=false;document.getElementById('search-text').focus();document.getElementById('search-text').select();}});
