package buffer

import (
	moe "github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"testing"
)

func TestNativeBuffer(t *testing.T) {
	vm := moe.NewRuntime(moe.Options{})
	require.NewRegistry().Enable(vm)
	Enable(vm)
	for _, s := range []string{

		`Object.getPrototypeOf(Buffer.prototype) === Uint8Array.prototype`,
		`Object.isFrozen(Object.prototype)&&Object.isFrozen(Uint8Array.prototype)`,
		`(function(){const b=Buffer.alloc(1);return b.copy(Buffer.alloc(0),999)===0&&Buffer.byteLength("zz","hex")===1})()`,
		`(function(){for(const f of [()=>Buffer.from({}),()=>Buffer.alloc(4).readUIntLE(0,1.5),()=>Buffer.alloc(8).writeBigUInt64LE(-1n),()=>Buffer.alloc(8).writeUIntLE(NaN,0,2)]){try{f();return false;}catch(e){}}return true;})()`,
		`(function(){const a=new Uint8Array([1,2,3]);const b=Buffer.from(a);b[0]=9;const ab=a.buffer;const v=Buffer.from(ab,1,1);v[0]=7;return a[0]===1&&a[1]===7&&v.length===1&&b instanceof Uint8Array})()`,
		`Buffer.from("hello").toString() === "hello"`,
		`Buffer.from("hello").toString("hex") === "68656c6c6f"`,
		`Buffer.from("68656c6c6f", "hex").toString() === "hello"`,
		`Buffer.from("通知").toString() === "通知"`,
		`Buffer.from("aGVsbG8=", "base64").toString() === "hello"`,
		`Buffer.from("hello") instanceof Buffer`,
		`Buffer.alloc(4, "ab").toString() === "abab"`,
		`Buffer.concat([Buffer.from("a"),Buffer.from("b")]).toString() === "ab"`,
		`(function(){const b=Buffer.alloc(16);b.writeUInt32LE(0x12345678,1);b.writeInt16BE(-123,5);b.writeDoubleBE(1.25,8);return b.readUInt32LE(1)===0x12345678&&b.readInt16BE(5)===-123&&b.readDoubleBE(8)===1.25})()`,
		`(function(){const b=Buffer.from("abcd");const v=b.slice(1,3);v[0]=120;return b.toString()==="axcd"&&Buffer.isBuffer(v)})()`,
		`(function(){const b=Buffer.alloc(8);b.writeBigUInt64LE(123n);return b.readBigUInt64LE()===123n})()`,
	} {
		v, err := native.Run(vm, s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if !native.Bool(v) {
			t.Errorf("false: %s", s)
		}
	}
	data := []byte("wrapped")
	b := WrapBytes(vm, data)
	if err := vm.SetGlobal("wrapped", engine.ObjectValue(b)); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Run(vm, "wrapped[0]=65"); err != nil {
		t.Fatal(err)
	}
	if data[0] != 65 {
		t.Fatal("WrapBytes did not share backing storage")
	}
	view, err := native.Run(vm, "wrapped.subarray(1,3)")
	if err != nil {
		t.Fatal(err)
	}
	raw := Bytes(vm, view)
	raw[0] = 66
	if data[1] != 66 || len(raw) != 2 {
		t.Fatal("Bytes did not return a live bounded subarray")
	}
	for _, source := range []string{"new DataView(wrapped.buffer,1,2)", "new Uint16Array(wrapped.buffer,0,2)", "wrapped.buffer"} {
		v, err := native.Run(vm, source)
		if err != nil {
			t.Fatal(err)
		}
		b := Bytes(vm, v)
		if len(b) == 0 {
			t.Fatalf("empty bytes: %s", source)
		}
		b[0] = 77
		offset := 0
		if source == "new DataView(wrapped.buffer,1,2)" {
			offset = 1
			if len(b) != 2 {
				t.Fatal("unbounded DataView")
			}
		}
		if data[offset] != 77 {
			t.Fatalf("bytes not live: %s", source)
		}
	}
}
