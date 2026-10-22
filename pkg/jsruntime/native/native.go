// Package native contains small operations on MoeJS values for Komari host modules.
// It defines no runtime/value/object wrappers and performs no reflected binding.
package native

import (
	"fmt"
	moe "github.com/Calcium-Ion/moejs"
	e "github.com/Calcium-Ion/moejs/engine"
	"math"
)

func Callable(v moe.Value) (moe.Value, bool) { return v, e.IsCallable(v) }
func AsObject(v moe.Value) (*e.Object, bool) {
	if !v.IsObject() {
		return nil, false
	}
	return v.AsObject(), true
}

func IsNil(v moe.Value) bool { return v.IsUndefined() || v.IsNull() }
func From(vm *moe.Runtime, x any) moe.Value {
	v, err := vm.FromGo(x)
	if err != nil {
		panic(err)
	}
	return v
}
func Get(vm *moe.Runtime, o *e.Object, key string) moe.Value {
	if o == nil {
		return moe.Undefined()
	}
	v, err := vm.Get(e.ObjectValue(o), key)
	if err != nil {
		panic(err)
	}
	return v
}
func Set(vm *moe.Runtime, o *e.Object, key string, x any) error {
	v, err := vm.FromGo(x)
	if err != nil {
		return err
	}
	return o.SetProp(vm.Realm(), vm.Realm().KeyFromGoString(key), v)
}
func Global(vm *moe.Runtime, name string) moe.Value      { return Get(vm, vm.Realm().Global, name) }
func Run(vm *moe.Runtime, src string) (moe.Value, error) { return RunScript(vm, "", src) }
func RunScript(vm *moe.Runtime, name, src string) (moe.Value, error) {
	s, err := moe.CompileScript(name, src)
	if err != nil {
		return moe.Undefined(), err
	}
	return vm.RunScript(s)
}
func Object(vm *moe.Runtime, v moe.Value) *e.Object {
	o, err := vm.Realm().ToObject(v)
	if err != nil {
		panic(err)
	}
	return o
}
func String(vm *moe.Runtime, v moe.Value) string {
	s, err := vm.Realm().ToString(v)
	if err != nil {
		panic(err)
	}
	return s.GoString()
}
func Float(vm *moe.Runtime, v moe.Value) float64 {
	n, err := vm.Realm().ToNumber(v)
	if err != nil {
		panic(err)
	}
	return n
}
func Int(vm *moe.Runtime, v moe.Value) int64 {
	n := Float(vm, v)
	if math.IsNaN(n) || n == 0 {
		return 0
	}
	if n >= math.MaxInt64 {
		return math.MaxInt64
	}
	if n <= math.MinInt64 {
		return math.MinInt64
	}
	return int64(n)
}
func Bool(v moe.Value) bool {
	switch v.Type() {
	case e.TypeNull, e.TypeUndefined:
		return false
	case e.TypeBoolean:
		return v.AsBool()
	case e.TypeNumber:
		n := v.AsNumber()
		return n != 0 && !math.IsNaN(n)
	case e.TypeString:
		return v.AsString().Len() != 0
	case e.TypeBigInt:
		return !v.AsBigInt().IsZero()
	default:
		return true
	}
}
func Export(vm *moe.Runtime, v moe.Value) any {
	x, err := vm.ToGo(v)
	if err != nil {
		panic(err)
	}
	return x
}
func ExportTo(vm *moe.Runtime, v moe.Value, target any) error {
	if p, ok := target.(*[]byte); ok && v.IsObject() {
		if b, ok := v.AsObject().BufferData(); ok {
			*p = b
			return nil
		}
	}
	return vm.ToGoInto(v, target)
}
func Call(vm *moe.Runtime, fn, this moe.Value, args ...moe.Value) (moe.Value, error) {
	return vm.CallFunction(fn, this, args...)
}
func New(vm *moe.Runtime, ctor moe.Value, args ...moe.Value) (*e.Object, error) {
	r := vm.Realm()
	r.HoldJobs()
	v, err := r.Construct(ctor, args, nil)
	err = r.ReleaseJobs(err)
	if err != nil {
		return nil, err
	}
	if !v.IsObject() {
		return nil, fmt.Errorf("constructor returned non-object")
	}
	return v.AsObject(), nil
}
func TypeError(vm *moe.Runtime, format string, args ...any) error {
	return vm.Realm().TypeError(format, args...)
}
func GoError(vm *moe.Runtime, err error) moe.Value {
	v, ok := vm.ThrownValue(err)
	if !ok {
		return moe.Undefined()
	}
	return v
}
func Throw(vm *moe.Runtime, err error) moe.Value { panic(err) }

// Bind installs an explicit native signature, recovering only errors thrown by
// host operations. This is not an automatic Go function/argument adapter.
func Bind(vm *moe.Runtime, name string, arity int, fn moe.NativeFunc) moe.Value {
	wrapped := func(r *moe.Realm, this moe.Value, args []moe.Value) (out moe.Value, err error) {
		defer func() {
			if p := recover(); p != nil {
				switch x := p.(type) {
				case error:
					err = x
				case moe.Value:
					err = &e.Exception{Value: x}
				default:
					panic(p)
				}
			}
		}()
		return fn(r, this, args)
	}
	return e.ObjectValue(vm.Realm().NewNativeFunction(e.FromGoString(name), arity, wrapped))
}
func Function(vm *moe.Runtime, o *e.Object, name string, fn moe.NativeFunc) error {
	return Set(vm, o, name, Bind(vm, name, 0, fn))
}
