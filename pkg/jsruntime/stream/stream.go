package stream

import (
	_ "embed"
	moe "github.com/Calcium-Ion/moejs"
	e "github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
)

const ModuleName = "stream"
const PromisesModuleName = "stream/promises"

//go:embed stream.js
var streamSource string

func Load(vm *moe.Runtime, module *e.Object) {
	exports, err := native.Run(vm, streamSource)
	if err != nil {
		panic(err)
	}
	if err = native.Set(vm, module, "exports", exports); err != nil {
		panic(err)
	}
}
func LoadPromises(vm *moe.Runtime, module *e.Object) {
	stream := native.Object(vm, require.Require(vm, ModuleName))
	exports := vm.Realm().NewObject()
	for _, key := range []string{"pipeline", "finished"} {
		if err := native.Set(vm, exports, key, native.Get(vm, stream, key)); err != nil {
			panic(err)
		}
	}
	if err := native.Set(vm, module, "exports", exports); err != nil {
		panic(err)
	}
}
