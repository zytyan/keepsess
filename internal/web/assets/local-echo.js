/* Temporary input preview. Never write speculative text into xterm's buffer. */
'use strict';
class LocalEchoPreview {
 constructor(term) {
  this.term=term;this.text='';this.escape='';
  this.layer=document.createElement('div');this.layer.className='local-echo-preview';
  this.layer.setAttribute('aria-hidden','true');term.element.querySelector('.xterm-screen').append(this.layer);
  this.context=document.createElement('canvas').getContext('2d');
  this.segmenter=typeof Intl.Segmenter==='function'?new Intl.Segmenter(undefined,{granularity:'grapheme'}):null;
  this.listeners=[term.onRender(()=>this.render()),term.onScroll(()=>this.render()),term.onResize(()=>this.render())];
 }
 clear(){this.text='';this.escape='';this.layer.replaceChildren();}
 dispose(){this.clear();for(const listener of this.listeners)listener.dispose();this.layer.remove();}
 parts(text){return this.segmenter?Array.from(this.segmenter.segment(text),s=>s.segment):Array.from(text);}
 add(data){
  for(const ch of data){
   // Suppress key/control sequences in the preview only, including split CSI/OSC.
   if(this.escape){
    if(this.escape==='esc'){this.escape=ch==='['||ch==='O'?'csi':ch===']'?'osc':/[ -/]/.test(ch)?'intermediate':'';}
    else if(this.escape==='csi'||this.escape==='intermediate'){if(ch>='@'&&ch<='~'||this.escape==='intermediate'&&ch>='0')this.escape='';}
    else if(this.escape==='osc'){if(ch==='\x07')this.escape='';else if(ch==='\x1b')this.escape='osc-end';}
    else this.escape=ch==='\\'?'':'osc';
    continue;
   }
   if(ch==='\x1b'){this.escape='esc';continue;}
   if(ch==='\b'||ch==='\x7f'){this.text=this.parts(this.text).slice(0,-1).join('');continue;}
   if(ch==='\r'||ch==='\n')this.text+='\n';
   else if(ch==='\t')this.text+='\t';
   else if(ch>=' '&&!(ch>='\x80'&&ch<='\x9f'))this.text+=ch;
   // Bounded independently of the send queue; clipping never changes sent input.
   if(this.text.length>4096){this.text=this.text.slice(0,4096);break;}
  }
  this.render();
 }
 render(){
  this.layer.replaceChildren();if(!this.text)return;
  const term=this.term,buffer=term.buffer.active,screen=this.layer.parentElement;
  const width=screen.clientWidth/term.cols,height=screen.clientHeight/term.rows;
  if(!width||!height)return;
  const font=term.options;this.layer.style.font=`${font.fontSize}px ${font.fontFamily}`;this.context.font=`${font.fontSize}px ${font.fontFamily}`;
  const unit=this.context.measureText('M').width||width,fragment=document.createDocumentFragment();
  let x=buffer.cursorX,y=buffer.baseY+buffer.cursorY-buffer.viewportY;
  for(const ch of this.parts(this.text)){
   if(ch==='\n'){x=0;y++;continue;}
   if(ch==='\t'){x+=8-x%8;continue;}
   const cells=Math.min(2,Math.max(1,Math.round(this.context.measureText(ch).width/unit)));
   if(x+cells>term.cols){x=0;y++;}if(y>=term.rows)break;
   if(y>=0){const span=document.createElement('span');span.textContent=ch;Object.assign(span.style,{left:`${x*width}px`,top:`${y*height}px`,width:`${cells*width}px`,height:`${height}px`,lineHeight:`${height}px`});fragment.append(span);}
   x+=cells;
  }
  this.layer.append(fragment);
 }
}
