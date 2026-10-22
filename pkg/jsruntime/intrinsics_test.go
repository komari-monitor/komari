package jsruntime

import (
	"io"
	"testing"
	"time"
)

func TestMutableIntrinsicsAreIsolated(t *testing.T) {
	for _, nodeJS := range []bool{false, true} {
		name := "Web"
		if nodeJS {
			name = "Node"
		}
		t.Run(name, func(t *testing.T) {
			opts := Options{NodeJS: nodeJS, BaseDir: t.TempDir(), Console: io.Discard, Timeout: time.Second}
			first, err := New(`
    Object.defineProperty(Reflect,"decorate",{value:function(){return "decorated"},configurable:true,writable:true});
    Object.defineProperty(Array.prototype,"__komariPluginProbe",{value:"private",configurable:true});
    function verify(){return Reflect.decorate()==="decorated"&&[].__komariPluginProbe==="private";}
   `, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			if err := first.Call("verify"); err != nil {
				t.Fatal(err)
			}
			second, err := New(`
    if(Object.prototype.hasOwnProperty.call(Reflect,"decorate")||Object.prototype.hasOwnProperty.call(Array.prototype,"__komariPluginProbe")){throw new Error("intrinsics leaked between plugins");}
    Reflect.decorate=function(){return "second"};
    function verify(){return Reflect.decorate()==="second"&&typeof [].__komariPluginProbe==="undefined";}
   `, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			if err := second.Call("verify"); err != nil {
				t.Fatal(err)
			}
			if err := first.Call("verify"); err != nil {
				t.Fatalf("second runtime changed first: %v", err)
			}
		})
	}
}
