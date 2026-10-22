// Package buffer implements Node buffers using native JavaScript typed arrays.
package buffer

import (
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	moe "github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const ModuleName = "buffer"

//go:embed buffer.js
var source string

func codecName(s string) string {
	switch strings.ToLower(s) {
	case "", "utf8", "utf-8":
		return "utf8"
	case "hex", "base64", "base64url", "ascii":
		return strings.ToLower(s)
	case "latin1", "binary":
		return "latin1"
	case "ucs2", "ucs-2", "utf16le", "utf-16le":
		return "utf16le"
	}
	return ""
}
func decode(s, enc string) []byte {
	switch codecName(enc) {
	case "hex":
		b, _ := hex.DecodeString(s)
		return b
	case "base64", "base64url":
		s = strings.Map(func(r rune) rune {
			if r == '-' {
				return '+'
			}
			if r == '_' {
				return '/'
			}
			if r > 127 {
				return -1
			}
			if strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=", r) {
				return r
			}
			return -1
		}, s)
		s = strings.TrimRight(s, "=")
		b, _ := base64.RawStdEncoding.DecodeString(s)
		return b
	case "latin1", "ascii":
		u := utf16.Encode([]rune(s))
		b := make([]byte, len(u))
		for i, v := range u {
			b[i] = byte(v)
		}
		return b
	case "utf16le":
		u := utf16.Encode([]rune(s))
		b := make([]byte, len(u)*2)
		for i, v := range u {
			b[2*i] = byte(v)
			b[2*i+1] = byte(v >> 8)
		}
		return b
	default:
		return []byte(s)
	}
}
func encode(b []byte, enc string) string {
	switch codecName(enc) {
	case "hex":
		return hex.EncodeToString(b)
	case "base64":
		return base64.StdEncoding.EncodeToString(b)
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(b)
	case "latin1", "ascii":
		r := make([]rune, len(b))
		for i, v := range b {
			if codecName(enc) == "ascii" {
				v &= 127
			}
			r[i] = rune(v)
		}
		return string(r)
	case "utf16le":
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return string(utf16.Decode(u))
	default:
		if utf8.Valid(b) {
			return string(b)
		}
		var out strings.Builder
		for len(b) > 0 {
			r, n := utf8.DecodeRune(b)
			out.WriteRune(r)
			b = b[n:]
		}
		return out.String()
	}
}

// Bytes returns a live view of the bytes of ArrayBuffer, TypedArray and DataView.
func Bytes(vm *moe.Runtime, v moe.Value) []byte {
	if v.IsObject() {
		if b, ok := v.AsObject().BufferData(); ok {
			return b
		}
	}
	if v.IsString() {
		return []byte(native.String(vm, v))
	}
	var b []byte
	if err := native.ExportTo(vm, v, &b); err != nil {
		panic(vm.Realm().TypeError("Expected a buffer or byte array"))
	}
	return b
}
func DecodeBytes(vm *moe.Runtime, arg, enc moe.Value) []byte {
	if arg.IsString() {
		s := "utf8"
		if !native.IsNil(enc) {
			s = native.String(vm, enc)
		}
		return decode(native.String(vm, arg), s)
	}
	return Bytes(vm, arg)
}

// WrapBytes shares data with a native ArrayBuffer; callers must synchronize access.
func WrapBytes(vm *moe.Runtime, data []byte) *engine.Object {
	ab, err := vm.Realm().NewArrayBuffer(data)
	if err != nil {
		panic(err)
	}
	m := require.Require(vm, ModuleName)
	ctor := native.Get(vm, m.AsObject(), "Buffer")
	from := native.Get(vm, ctor.AsObject(), "from")
	v, err := vm.CallFunction(from, ctor, engine.ObjectValue(ab))
	if err != nil {
		panic(err)
	}
	return v.AsObject()
}
func EncodeBytes(vm *moe.Runtime, data []byte, enc moe.Value) moe.Value {
	if !native.IsNil(enc) {
		s := native.String(vm, enc)
		if codecName(s) != "" {
			return native.From(vm, encode(data, s))
		}
	}
	return engine.ObjectValue(WrapBytes(vm, data))
}
func Enable(vm *moe.Runtime) {
	m := require.Require(vm, ModuleName)
	if err := vm.SetGlobal("Buffer", native.Get(vm, m.AsObject(), "Buffer")); err != nil {
		panic(err)
	}
}
func Require(vm *moe.Runtime, module *engine.Object) {
	factory, err := native.RunScript(vm, "buffer.js", source)
	if err != nil {
		panic(err)
	}
	enc := native.Bind(vm, "encode", 2, moe.NativeFunc(func(_ *moe.Realm, _ moe.Value, a []moe.Value) (moe.Value, error) {
		return native.From(vm, encode(Bytes(vm, moe.Arg(a, 0)), native.String(vm, moe.Arg(a, 1)))), nil
	}))
	dec := native.Bind(vm, "decode", 2, moe.NativeFunc(func(_ *moe.Realm, _ moe.Value, a []moe.Value) (moe.Value, error) {
		b := decode(native.String(vm, moe.Arg(a, 0)), native.String(vm, moe.Arg(a, 1)))
		ab, e := vm.Realm().NewArrayBuffer(b)
		if e != nil {
			return moe.Undefined(), e
		}
		return engine.ObjectValue(ab), nil
	}))
	exports, err := vm.CallFunction(factory, moe.Undefined(), enc, dec)
	if err != nil {
		panic(err)
	}
	if err = native.Set(vm, module, "exports", exports); err != nil {
		panic(err)
	}
}
func init() { require.RegisterCoreModule(ModuleName, Require) }
