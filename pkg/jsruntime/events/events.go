package events

import (
	_ "embed"
	moe "github.com/Calcium-Ion/moejs"
	e "github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
)

func Load(vm *moe.Runtime, module *e.Object) {
	constructor, err := native.Run(vm, eventEmitterSource)
	if err != nil {
		panic(err)
	}
	object := native.Object(vm, constructor)
	if err = native.Set(vm, object, "EventEmitter", constructor); err != nil {
		panic(err)
	}
	_ = native.Set(vm, object, "defaultMaxListeners", 10)
	if err = native.Set(vm, module, "exports", constructor); err != nil {
		panic(err)
	}
}
func NewEmitter(vm *moe.Runtime) *e.Object {
	object, err := native.New(vm, require.Require(vm, "events"))
	if err != nil {
		panic(err)
	}
	return object
}
func Emit(vm *moe.Runtime, emitter *e.Object, name string, values ...any) error {
	args := make([]moe.Value, 1, len(values)+1)
	args[0] = moe.String(name)
	for _, x := range values {
		v, err := vm.FromGo(x)
		if err != nil {
			return err
		}
		args = append(args, v)
	}
	_, err := vm.CallFunction(native.Get(vm, emitter, "emit"), e.ObjectValue(emitter), args...)
	return err
}

//go:embed events.js
var eventEmitterSource string
