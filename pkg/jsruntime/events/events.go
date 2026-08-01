package events

import (
	_ "embed"
	"fmt"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

func Load(vm *goja.Runtime, module *goja.Object) {
	constructor, err := vm.RunString(eventEmitterSource)
	if err != nil {
		panic(vm.NewGoError(fmt.Errorf("load events module: %w", err)))
	}
	_ = constructor.ToObject(vm).Set("EventEmitter", constructor)
	_ = constructor.ToObject(vm).Set("defaultMaxListeners", 10)
	_ = module.Set("exports", constructor)
}

func NewEmitter(vm *goja.Runtime) *goja.Object {
	constructor := require.Require(vm, "events")
	object, err := vm.New(constructor)
	if err != nil {
		panic(err)
	}
	return object
}

func Emit(vm *goja.Runtime, emitter *goja.Object, name string, values ...any) error {
	emit, ok := goja.AssertFunction(emitter.Get("emit"))
	if !ok {
		return fmt.Errorf("EventEmitter.emit is not callable")
	}
	arguments := make([]goja.Value, 1, len(values)+1)
	arguments[0] = vm.ToValue(name)
	for _, value := range values {
		if jsValue, ok := value.(goja.Value); ok {
			arguments = append(arguments, jsValue)
		} else {
			arguments = append(arguments, vm.ToValue(value))
		}
	}
	_, err := emit(emitter, arguments...)
	return err
}

//go:embed events.js
var eventEmitterSource string
