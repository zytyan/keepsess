/* Paint in xterm's render callback. Native Custom Highlight registration can
 * still present an uncolored frame in headed Edge, even with cached matches.
 * Only text nodes are wrapped; cells, input, history and ANSI remain xterm's. */
'use strict';
class DOMKeywordLayer {
 static supported(term){return typeof term.registerDOMRenderFilter==='function'&&!!term.element.querySelector('.xterm-rows');}
 constructor(term){this.term=term;this.rules=[];this.rows=new Map();}
 configure(rules){this.clear();this.rules=rules;}
 unwrap(state){
  const parents=new Set();
  for(const span of state.spans){if(!span.isConnected)continue;const parent=span.parentNode;span.replaceWith(...span.childNodes);parents.add(parent);}
  for(const parent of parents)parent.normalize();
 }
 clear(){for(const state of this.rows.values())this.unwrap(state);this.rows.clear();}
 paint(spans){
  const term=this.term,buffer=term.buffer.active,rows=term.element.querySelector('.xterm-rows');
  if(!rows||buffer.type!=='normal'){this.clear();return;}
  const byRow=new Map();
  for(const span of spans){const index=span.marker.line-buffer.viewportY;if(index<0||index>=term.rows)continue;let hits=byRow.get(index);if(!hits){hits=[];byRow.set(index,hits);}hits.push(span);}
  for(const [row,state] of this.rows){if(row.parentNode!==rows){this.unwrap(state);this.rows.delete(row);}}
  for(let index=0;index<Math.min(rows.children.length,term.rows);index++){
   const row=rows.children[index],hits=byRow.get(index)||[];
   const key=hits.map(s=>`${s.options.x}/${s.options.width}/${s.rule}`).join(';'),old=this.rows.get(row);
   if(old&&old.key===key&&old.roots.length===row.childNodes.length&&old.roots.every((node,i)=>node===row.childNodes[i]))continue;
   if(old)this.unwrap(old);
   if(!hits.length){this.rows.delete(row);continue;}
   const line=buffer.getLine(buffer.viewportY+index);if(!line)continue;
   const offsets=[],cols=Math.min(term.cols,4096);let offset=0;
   for(let x=0;x<cols;x++){offsets[x]=offset;const cell=line.getCell(x);if(cell?.getWidth())offset+=cell.isInvisible()?1:(cell.getChars()||' ').length;}
   offsets[cols]=offset;
   const ranges=hits.map(s=>({start:offsets[s.options.x],end:offsets[s.options.x+s.options.width],rule:s.rule})).filter(s=>s.start!==undefined&&s.end>s.start).sort((a,b)=>a.start-b.start||a.rule-b.rule);
   const walker=document.createTreeWalker(row,NodeFilter.SHOW_TEXT),nodes=[];let node,total=0;
   while((node=walker.nextNode())){nodes.push({node,start:total,end:total+node.length});total+=node.length;}
   const wrapped=[];
   for(const entry of nodes){
    const parts=ranges.filter(s=>s.start<entry.end&&s.end>entry.start);if(!parts.length)continue;
    const fragment=document.createDocumentFragment(),text=entry.node.data;let cursor=0;
    for(const part of parts){
     const start=Math.max(cursor,part.start-entry.start),end=Math.min(text.length,part.end-entry.start),rule=this.rules[part.rule];if(start>=end||!rule)continue;
     if(start>cursor)fragment.append(document.createTextNode(text.slice(cursor,start)));
     const span=document.createElement('span');span.className='keepsess-keyword';span.textContent=text.slice(start,end);
     if(/^#[0-9a-f]{6}$/i.test(rule.foreground||''))span.style.color=rule.foreground;
     if(/^#[0-9a-f]{6}$/i.test(rule.background||''))span.style.backgroundColor=rule.background;
     fragment.append(span);wrapped.push(span);cursor=end;
    }
    if(cursor<text.length)fragment.append(document.createTextNode(text.slice(cursor)));
    entry.node.replaceWith(fragment);
   }
   this.rows.set(row,{key,roots:[...row.childNodes],spans:wrapped});
  }
 }
 dispose(){this.clear();}
}
