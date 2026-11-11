/* Compact output frames; JSON is retained for infrequent control messages. */
'use strict';
const wireText=new TextDecoder();
function outputMessage(raw){
 const b=raw instanceof Uint8Array?raw:new Uint8Array(raw);
 if(b[0]===0)return JSON.parse(wireText.decode(b.subarray(1)));
 if(b.length<20||b[0]!==1||b[17]>2)throw new Error('无效输出帧');
 const v=new DataView(b.buffer,b.byteOffset,b.byteLength),seq=Number(v.getBigUint64(1)),epoch=Number(v.getBigUint64(9));
 const end=20+b[18]+b[19];if(end>b.length||!Number.isSafeInteger(seq)||!Number.isSafeInteger(epoch))throw new Error('无效输出游标');
 return {type:'event',event:{kind:'output',seq,epoch,encoding:['utf-8','gbk','shift-jis'][b[17]],session:wireText.decode(b.subarray(20,20+b[18])),handle:wireText.decode(b.subarray(20+b[18],end)),data:b.subarray(end)}};
}
function outputBytes(data){return data instanceof Uint8Array?data:Uint8Array.from(atob(data),c=>c.charCodeAt(0));}
function replayMessages(buffer){
 const b=new Uint8Array(buffer),v=new DataView(buffer),messages=[];
 if(wireText.decode(b.subarray(0,4))!=='KSR1')throw new Error('无效历史快照');
 for(let i=4;i<b.length;){if(i+4>b.length)throw new Error('历史快照不完整');const n=v.getUint32(i);i+=4;if(!n||i+n>b.length)throw new Error('历史帧不完整');const frame=b.subarray(i,i+n);messages.push(frame[0]===0?JSON.parse(wireText.decode(frame.subarray(1))):outputMessage(frame));i+=n;}
 if(messages.at(-1)?.type!=='replay_end')throw new Error('历史快照缺少结束游标');return messages;
}
