'use strict';
// Observe human input before xterm's textarea handlers. onData also includes
// parser replies, so it must never be used as the application input source.
function attachTerminalInput(term,send){
 const events=new AbortController(),options={capture:true,signal:events.signal};
 let composing=false,candidate='',hasCandidate=false,keyText='',committed=null;
 const textarea=term.textarea;
 const commit=text=>{
  composing=false;candidate='';hasCandidate=false;
  committed=text?{text,value:textarea.value}:null;
  if(text)send(text);
 };
 const duplicate=event=>committed&&event.data===committed.text&&textarea.value===committed.value;
 term.element.addEventListener('compositionstart',event=>{
  if(event.target!==textarea||!event.isTrusted)return;
  composing=true;candidate='';hasCandidate=false;committed=null;keyText='';
 },options);
 term.element.addEventListener('compositionend',event=>{
  if(event.target!==textarea)return;
  if(!composing)return; // An input event may already have committed this cycle.
  // Chromium can end a trusted IME cycle with an untrusted compositionend.
  // Such an event may only confirm the exact text from its trusted input;
  // it cannot inject its own data or start a composition by itself.
  // Cancellation can end without a final input event; discard the cycle
  // without forwarding any data, even if the browser marks its end untrusted.
  if(!event.isTrusted&&event.data===''){commit('');return;}
  if(!event.isTrusted&&(!hasCandidate||event.data!==candidate))return;
  commit(event.isTrusted?event.data:candidate);
 },options);
 term.element.addEventListener('input',event=>{
  if(event.target!==textarea||!event.isTrusted)return;
  const type=event.inputType;
  if(event.isComposing){if(composing){candidate=event.data||'';hasCandidate=true;}return;}
  if(type==='insertFromComposition'||type==='insertCompositionText'){
   if(duplicate(event)){committed=null;return;}
   commit(event.data||'');return;
  }
  if(type!=='insertText')return;
  if(composing){candidate=event.data||'';hasCandidate=true;return;}
  if(duplicate(event)){committed=null;return;}
  committed=null;
  if(event.data&&event.data!==keyText)send(event.data);
 },options);
 term.element.addEventListener('keydown',event=>{
  if(event.target===textarea&&event.isTrusted&&!composing&&!event.isComposing&&event.keyCode!==229)committed=null;
 },options);
 const key=term.onKey(({key,domEvent})=>{
  if(domEvent.isTrusted&&!composing&&!domEvent.isComposing&&domEvent.keyCode!==229){keyText=key;send(key);}
 });
 term.element.addEventListener('keyup',event=>{if(event.target===textarea)keyText='';},options);
 term.element.addEventListener('paste',event=>{
  if(event.target!==textarea||!event.isTrusted)return;
  event.preventDefault();event.stopImmediatePropagation();committed=null;
  send(event.clipboardData.getData('text/plain'));
 },options);
 term.register({dispose:()=>{events.abort();key.dispose();}});
}
