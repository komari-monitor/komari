(function(encode, decode) {
'use strict';
const encodings = ['utf8','utf-8','hex','base64','base64url','ascii','latin1','binary','ucs2','ucs-2','utf16le','utf-16le'];
function encoding(e) { e = e === undefined ? 'utf8' : String(e).toLowerCase(); if (!encodings.includes(e)) throw new TypeError('Unknown encoding: '+e); return e; }
function size(n) { if (typeof n !== 'number') throw new TypeError('size must be a number'); if (!Number.isInteger(n)||n<0||n>0x7fffffff) throw new RangeError('Invalid buffer size'); return n; }
function bounds(b,o,n) { if (!Number.isInteger(o)||!Number.isInteger(n)||n<0||o<0||o+n>b.length) throw new RangeError('Offset is outside the bounds of the Buffer'); }
class Buffer extends Uint8Array {
 constructor(a,b,c) { if(typeof a==='string') super(decode(a,encoding(b))); else super(a,b,c); }
 static from(a,b,c) { if(typeof a==='number'||a==null) throw new TypeError('Invalid Buffer input'); if(typeof a==='string')return new Buffer(decode(a,encoding(b))); if(a instanceof ArrayBuffer) return new Buffer(a,b,c); if(a.type==='Buffer'&&Array.isArray(a.data)) a=a.data; if(a.valueOf){const v=a.valueOf();if(v!==a)return Buffer.from(v,b,c);}if(typeof a.length!=='number'&&!ArrayBuffer.isView(a)&&!a[Symbol.iterator])throw new TypeError('Invalid Buffer input'); return new Buffer(a); }
 static alloc(n,fill,enc) {const b=new Buffer(size(n));if(fill!==undefined)b.fill(fill,0,b.length,enc);return b;}
 static allocUnsafe(n) {return new Buffer(size(n));}
 static allocUnsafeSlow(n) {return Buffer.allocUnsafe(n);}
 static isBuffer(b) {return b instanceof Buffer;}
 static isEncoding(e) {return typeof e==='string'&&encodings.includes(e.toLowerCase());}
 static byteLength(s,e) {if(typeof s!=='string'){if(s instanceof ArrayBuffer||ArrayBuffer.isView(s))return s.byteLength;throw new TypeError('Expected string or buffer');}e=encoding(e);return e==='hex'?Math.floor(s.length/2):decode(s,e).byteLength;}
 static concat(list,total) {if(!Array.isArray(list))throw new TypeError('Expected array');if(total===undefined)total=list.reduce((n,b)=>n+b.length,0);const b=Buffer.alloc(total);let o=0;for(const p of list){const n=Math.min(p.length,total-o);if(n<=0)break;b.set(p.subarray(0,n),o);o+=n;}return b;}
 static compare(a,b) {for(let i=0;i<Math.min(a.length,b.length);i++)if(a[i]!==b[i])return a[i]<b[i]?-1:1;return a.length===b.length?0:(a.length<b.length?-1:1);}
 toString(e,start,end) {e=encoding(e);start=Math.max(0,start===undefined?0:Math.trunc(start));end=Math.min(this.length,end===undefined?this.length:Math.trunc(end));return encode(this.subarray(start,Math.max(start,end)),e);}
 toLocaleString() {return this.toString();}
 toJSON() {return {type:'Buffer',data:Array.from(this)};}
 slice(s,e) {return this.subarray(s,e);}
 equals(b) {return Buffer.compare(this,b)===0;}
 compare(b,ts,te,ss,se) {return Buffer.compare(this.subarray(ss,se),b.subarray(ts,te));}
 copy(target,ts,ss,se) {ts=ts===undefined?0:ts;ss=ss===undefined?0:ss;se=se===undefined?this.length:se;if(ts<0||ss<0||se<0)throw new RangeError('Invalid offset');const n=Math.max(0,Math.min(se-ss,this.length-ss,target.length-ts));if(n>0)target.set(this.subarray(ss,ss+n),ts);return n;}
 fill(v,start,end,e) {if(typeof start==='string'){e=start;start=0;}if(typeof end==='string'){e=end;end=this.length;}start=start===undefined?0:start;end=end===undefined?this.length:end;bounds(this,start,Math.max(0,end-start));if(typeof v==='number') {super.fill(v,start,end);return this;}const p=typeof v==='string'?Buffer.from(v,e):v;if(!p||!p.length){if(v===''){super.fill(0,start,end);return this;}throw new TypeError('Invalid fill value');}for(let i=start;i<end;i++)this[i]=p[(i-start)%p.length];return this;}
 write(s,o,n,e) {if(typeof o==='string'){e=o;o=0;n=this.length;}o=o===undefined?0:o;if(typeof n==='string'){e=n;n=undefined;}n=n===undefined?this.length-o:n;bounds(this,o,n);let b=Buffer.from(s,e);let count=Math.min(n,b.length);if((e===undefined||/^utf-?8$/i.test(e))&&count<b.length){while(count>0&&(b[count]&192)===128)count--;}this.set(b.subarray(0,count),o);return count;}
 indexOf(v,o,e) {return this._find(v,o,e,false);}
 lastIndexOf(v,o,e) {return this._find(v,o,e,true);}
 includes(v,o,e) {return this.indexOf(v,o,e)!==-1;}
 _find(v,o,e,reverse) {if(typeof o==='string'){e=o;o=undefined;}if(typeof v==='number')v=Buffer.from([v]);else if(typeof v==='string')v=Buffer.from(v,e);o=o===undefined?(reverse?this.length:0):Math.trunc(o);if(o<0)o=Math.max(0,this.length+o);if(!v.length)return Math.min(o,this.length);if(reverse){for(let i=Math.min(o,this.length-v.length);i>=0;i--){let j=0;while(j<v.length&&this[i+j]===v[j])j++;if(j===v.length)return i;}}else{for(let i=o;i<=this.length-v.length;i++){let j=0;while(j<v.length&&this[i+j]===v[j])j++;if(j===v.length)return i;}}return -1;}
}
for(const width of [8,16,32,64]) for(const little of [false,true]) for(const signed of [false,true]) {
 if(width===8&&little)continue;
 const big=width===64, bytes=width/8, suffix=width+(width===8?'':little?'LE':'BE');
 const kind=big?(signed?'BigInt':'BigUInt'):(signed?'Int':'UInt');
 const dvkind=big?(signed?'BigInt64':'BigUint64'):(signed?'Int':'Uint')+width;
 Buffer.prototype['read'+kind+suffix]=function(o){o=o===undefined?0:o;bounds(this,o,bytes);return new DataView(this.buffer,this.byteOffset,this.byteLength)['get'+dvkind](o,little);};
 Buffer.prototype['write'+kind+suffix]=function(v,o){o=o===undefined?0:o;bounds(this,o,bytes);if(big&&(typeof v!=='bigint'||v<(signed?-(1n<<63n):0n)||v>(signed?(1n<<63n)-1n:(1n<<64n)-1n)))throw new RangeError('Value out of range');if(!big&&(!Number.isFinite(v)||v<(signed?-(2**(width-1)):0)||v>(signed?2**(width-1)-1:2**width-1)))throw new RangeError('Value out of range');new DataView(this.buffer,this.byteOffset,this.byteLength)['set'+dvkind](o,v,little);return o+bytes;};
 if(!signed){Buffer.prototype['read'+(big?'BigUint':'Uint')+suffix]=Buffer.prototype['read'+kind+suffix];Buffer.prototype['write'+(big?'BigUint':'Uint')+suffix]=Buffer.prototype['write'+kind+suffix];}
}
for(const pair of [['Float',32],['Double',64]])for(const little of [false,true]){const name=pair[0]+(little?'LE':'BE'),bytes=pair[1]/8;Buffer.prototype['read'+name]=function(o){o=o===undefined?0:o;bounds(this,o,bytes);return new DataView(this.buffer,this.byteOffset,this.byteLength)['getFloat'+pair[1]](o,little);};Buffer.prototype['write'+name]=function(v,o){o=o===undefined?0:o;bounds(this,o,bytes);new DataView(this.buffer,this.byteOffset,this.byteLength)['setFloat'+pair[1]](o,v,little);return o+bytes;};}
for(const little of [false,true])for(const signed of [false,true]){const name=(signed?'Int':'UInt')+(little?'LE':'BE');Buffer.prototype['read'+name]=function(o,n){if(!Number.isInteger(n)||n<1||n>6)throw new RangeError('Invalid byteLength');bounds(this,o,n);let v=0;for(let i=0;i<n;i++)v=v*256+this[o+(little?n-i-1:i)];if(signed&&v>=2**(n*8-1))v-=2**(n*8);return v;};Buffer.prototype['write'+name]=function(v,o,n){if(!Number.isInteger(n)||n<1||n>6)throw new RangeError('Invalid byteLength');bounds(this,o,n);const min=signed?-(2**(n*8-1)):0,max=signed?2**(n*8-1)-1:2**(n*8)-1;if(!Number.isFinite(v)||v<min||v>max)throw new RangeError('Value out of range');if(v<0)v+=2**(n*8);for(let i=0;i<n;i++){this[o+(little?i:n-i-1)]=v%256;v=Math.floor(v/256);}return o+n;};if(!signed){Buffer.prototype['readUint'+(little?'LE':'BE')]=Buffer.prototype['read'+name];Buffer.prototype['writeUint'+(little?'LE':'BE')]=Buffer.prototype['write'+name];}}
for(const n of [2,4,8])Buffer.prototype['swap'+n*8]=function(){if(this.length%n)throw new RangeError('Buffer size must be a multiple of '+n);for(let i=0;i<this.length;i+=n)for(let j=0;j<n/2;j++){const x=this[i+j];this[i+j]=this[i+n-j-1];this[i+n-j-1]=x;}return this;};
return {Buffer,SlowBuffer:Buffer.alloc,INSPECT_MAX_BYTES:50,kMaxLength:0x7fffffff,constants:{MAX_LENGTH:0x7fffffff,MAX_STRING_LENGTH:0x1fffffe8}};
})
