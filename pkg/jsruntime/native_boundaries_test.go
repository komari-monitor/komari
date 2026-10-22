package jsruntime

import (
	"io"
	"testing"
	"time"
)

func TestNativeTypedArrayBoundaries(t *testing.T) {
	rt, err := New(`function run(){
 const c=require("crypto");
 for(const C of [Float32Array,Float64Array]){let rejected=false;try{c.getRandomValues(new C(4))}catch(e){rejected=e instanceof TypeError}if(!rejected)throw new Error("float accepted")}
 const v=new Uint16Array(4);if(c.getRandomValues(v)!==v)throw new Error("identity");
 const b=new Uint8Array([1,2,3,4]);c.randomFillSync(new DataView(b.buffer,1,2));if(b[0]!==1||b[3]!==4)throw new Error("view bounds");
 return true;}`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if err := rt.Call("run"); err != nil {
		t.Fatal(err)
	}
}
