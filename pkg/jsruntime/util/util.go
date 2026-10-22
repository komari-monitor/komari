// Package util supplies JavaScript formatting without reflected Go callbacks.
package util

import (
	"bytes"
	_ "embed"
	moe "github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
)

const ModuleName = "util"

//go:embed util.js
var source string

func Require(vm *moe.Runtime, module *engine.Object) {
	v, err := native.RunScript(vm, "util.js", source)
	if err != nil {
		panic(err)
	}
	if err = native.Set(vm, module, "exports", v); err != nil {
		panic(err)
	}
}

type Util struct{ runtime *moe.Runtime }

func New(vm *moe.Runtime) *Util { return &Util{runtime: vm} }
func (u *Util) Format(b *bytes.Buffer, format string, args ...moe.Value) {
	values := append([]moe.Value{native.From(u.runtime, format)}, args...)
	b.WriteString(FormatConsole(u.runtime, values))
}
func FormatConsole(vm *moe.Runtime, args []moe.Value) string {
	m := require.Require(vm, ModuleName)
	f := native.Get(vm, m.AsObject(), "format")
	v, err := vm.CallFunction(f, m, args...)
	if err != nil {
		panic(err)
	}
	return native.String(vm, v)
}
func init() { require.RegisterCoreModule(ModuleName, Require) }
