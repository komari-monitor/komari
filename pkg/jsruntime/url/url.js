(function(parseURL, domain, decodeForm) {
'use strict';
function scalar(s){return String(s).replace(/[\uD800-\uDBFF][\uDC00-\uDFFF]|[\uD800-\uDFFF]/g,c=>c.length===2?c:'\uFFFD');}
function escape(s){return encodeURIComponent(s).replace(/[!'()~]/g,c=>'%'+c.charCodeAt(0).toString(16).toUpperCase()).replace(/%20/g,'+');}
function unescape(s){return decodeForm(s);}
class URLSearchParams {
 constructor(init){this._pairs=[];this._change=null;this._load(init);}
 _load(init){this._pairs=[];if(init==null)return;if(typeof init==='string'){if(init[0]==='?')init=init.slice(1);for(const p of init.split('&')){if(!p)continue;const i=p.indexOf('=');this._pairs.push([unescape(i<0?p:p.slice(0,i)),unescape(i<0?'':p.slice(i+1))]);}}else if(init[Symbol.iterator]){for(const pair of init){const p=Array.from(pair);if(p.length!==2)throw new TypeError('Expected name/value pair');this._pairs.push([scalar(p[0]),scalar(p[1])]);}}else{for(const k of Object.keys(init))this._pairs.push([k,scalar(init[k])]);}}
 _notify(){if(this._change)this._change(this.toString());}
 get size(){return this._pairs.length;}
 append(k,v){this._pairs.push([scalar(k),scalar(v)]);this._notify();}
 delete(k,v){k=scalar(k);if(v!==undefined)v=scalar(v);this._pairs=this._pairs.filter(p=>p[0]!==k||(v!==undefined&&p[1]!==v));this._notify();}
 get(k){k=scalar(k);for(const p of this._pairs)if(p[0]===k)return p[1];return null;}
 getAll(k){k=scalar(k);return this._pairs.filter(p=>p[0]===k).map(p=>p[1]);}
 has(k,v){k=scalar(k);return this._pairs.some(p=>p[0]===k&&(v===undefined||p[1]===scalar(v)));}
 set(k,v){k=scalar(k);v=scalar(v);let found=false;this._pairs=this._pairs.filter(p=>{if(p[0]!==k)return true;if(found)return false;p[1]=v;found=true;return true;});if(!found)this._pairs.push([k,v]);this._notify();}
 sort(){this._pairs.sort((a,b)=>a[0]<b[0]?-1:a[0]>b[0]?1:0);this._notify();}
 *entries(){for(let i=0;i<this._pairs.length;i++)yield this._pairs[i].slice();}
 *keys(){for(const p of this._pairs)yield p[0];}
 *values(){for(const p of this._pairs)yield p[1];}
 [Symbol.iterator](){return this.entries();}
 forEach(fn,thisArg){for(let i=0;i<this._pairs.length;i++){const p=this._pairs[i];fn.call(thisArg,p[1],p[0],this);}}
 toString(){return this._pairs.map(p=>escape(p[0])+'='+escape(p[1])).join('&');}
}
class URL {
 constructor(input,base){this._data=JSON.parse(parseURL(scalar(input),base===undefined?undefined:scalar(base)));this._params=new URLSearchParams(this._data.search);this._params._change=s=>{this._set('search',s?'?'+s:'',false);};}
 _parse(s,sync){const d=JSON.parse(parseURL(s));this._data=d;if(sync!==false)this._params._load(d.search);}
 _set(k,v,sync){
  v=scalar(v);if(k==='href'){this._parse(v,sync);return;}
  const d=Object.assign({},this._data), special=p=>/^(https?|wss?|ftp):$/.test(p);
  if(k==='protocol'){v=v.endsWith(':')?v:v+':';v=v.toLowerCase();if(!/^[a-z][a-z0-9+.-]*:$/.test(v)||special(v)!==special(d.protocol))return;}
  if(['host','hostname','port','username','password'].includes(k)&&(!d.host||d.protocol==='file:'))return;
  if(k==='port'&&(!/^\d*$/.test(v)||Number(v)>65535))return;
  if(k==='hostname'&&(v.includes('/')||v.includes('?')||v.includes('#')||(!v.startsWith('[')&&v.includes(':'))))return;
  if(k==='pathname'&&!d.host&&!d.pathname.startsWith('/'))return;
  d[k]=v;
  if(k==='host'){d.host=v.split(/[/?#]/)[0];}
  if(k==='hostname')d.host=v+(d.port?':'+d.port:'');
  if(k==='port')d.host=d.hostname+(v?':'+v:'');
  if(k==='username'||k==='password')d[k]=encodeURIComponent(v);
  if(k==='search')d.search=v?(v[0]==='?'?v:'?'+v).replace(/#/g,'%23').replace(/ /g,'%20'):'';
  if(k==='hash')d.hash=v?(v[0]==='#'?v:'#'+v).replace(/ /g,'%20'):'';
  if(k==='pathname')d.pathname=(v[0]==='/'?v:'/'+v).replace(/\?/g,'%3F').replace(/#/g,'%23');
  const auth=d.username||d.password?d.username+(d.password?':'+d.password:'')+'@':'';
  try{this._parse(d.protocol+(d.host?'//'+auth+d.host:'')+d.pathname+d.search+d.hash,sync);}catch(e){/* Invalid component assignments leave the URL unchanged. */}
 }
 get searchParams(){return this._params;}
 get origin(){return this._data.origin;}
 toString(){return this.href;}
 toJSON(){return this.href;}
 static canParse(s,b){try{new URL(s,b);return true;}catch(e){return false;}}
 static parse(s,b){try{return new URL(s,b);}catch(e){return null;}}
}
for(const k of ['href','protocol','username','password','host','hostname','port','pathname','search','hash'])Object.defineProperty(URL.prototype,k,{enumerable:true,configurable:true,get(){return this._data[k];},set(v){this._set(k,v);}});
Object.defineProperty(URL.prototype,Symbol.toStringTag,{value:'URL'});Object.defineProperty(URLSearchParams.prototype,Symbol.toStringTag,{value:'URLSearchParams'});
return {URL,URLSearchParams,domainToASCII:s=>domain(scalar(s),false),domainToUnicode:s=>domain(scalar(s),true)};
})
