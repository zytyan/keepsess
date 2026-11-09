/* Arbitrary user regex stays isolated behind the Worker's bounded deadline. */
'use strict';
importScripts('highlight-matcher.js');
const matcher=new HighlightMatcher();
self.onmessage=({data:{lines,rules}})=>self.postMessage(matcher.match(lines,rules));
self.postMessage({ready:true,engine:'javascript-regexp'});
