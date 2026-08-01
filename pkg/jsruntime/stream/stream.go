package stream

import (
	_ "embed"
	"fmt"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

// ModuleName is the CommonJS name of the Node.js stream compatibility module.
const ModuleName = "stream"

// PromisesModuleName is the CommonJS name of the stream/promises module.
const PromisesModuleName = "stream/promises"

//go:embed stream.js
var streamSource string

// Load registers the Node.js-compatible stream module (Readable, Writable,
// Duplex, Transform, PassThrough, pipeline, finished, ...).
func Load(vm *goja.Runtime, module *goja.Object) {
	exports, err := vm.RunString(streamSource)
	if err != nil {
		panic(vm.NewGoError(fmt.Errorf("load stream module: %w", err)))
	}
	_ = module.Set("exports", exports)
}

// LoadPromises registers the stream/promises module with Promise-based
// pipeline and finished helpers.
func LoadPromises(vm *goja.Runtime, module *goja.Object) {
	streamModule := require.Require(vm, ModuleName).ToObject(vm)
	exports := vm.NewObject()
	_ = exports.Set("pipeline", streamModule.Get("pipeline"))
	_ = exports.Set("finished", streamModule.Get("finished"))
	_ = module.Set("exports", exports)
}
