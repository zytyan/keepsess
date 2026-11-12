/* Shared browser-native matcher. Input, scans, results and lifetime are bounded. */
'use strict';
class HighlightMatcher {
constructor(){this.compiled=[];this.rulesKey='';}
compile(rules){
 const key=JSON.stringify(rules);if(key===this.rulesKey)return;
 const next=[];
 for(const [index,r] of rules.entries()){
  const pattern=r.regex?r.pattern:r.pattern.replace(/[.*+?^${}()|[\]\\]/g,'\\$&');
  next.push({...r,index,re:new RegExp(pattern,r.case_sensitive?'gu':'giu')});
 }
 this.compiled=next;this.rulesKey=key;
}
match(lines,rules){
 try{
  if(rules.length>32||lines.length>200||lines.reduce((n,s)=>n+s.length,0)>32768)throw Error('高亮请求超过上限');
  for(const r of rules)if(!r.pattern||new TextEncoder().encode(r.pattern).length>512)throw Error('表达式须为 1..512 字节');
  this.compile(rules);const matches=[];
  for(let line=0;line<lines.length;line++){
   const text=lines[line],occupied=new Uint8Array(text.length);
   const zeros=[...text.matchAll(/\b0\s+errors?\b/giu)].map(m=>[m.index,m.index+m[0].length]);
   for(const r of this.compiled){
    if(!r.enabled)continue;r.re.lastIndex=0;
    let examined=0,m;
    while((m=r.re.exec(text))&&examined++<=text.length){
     const start=m.index,end=start+m[0].length;
     if(start===end){r.re.lastIndex=start+(text.codePointAt(start)>0xffff?2:1);continue;}
     if(r.name==='错误'&&zeros.some(([a,b])=>start>=a&&end<=b))continue;
     if(occupied.subarray(start,end).some(Boolean))continue;
     occupied.fill(1,start,end);matches.push({line,start,end,rule:r.index});
    }
   }
  }
  return {matches,completed_lines:lines.length};
 }catch(error){return {error:error.message};}
}
}
