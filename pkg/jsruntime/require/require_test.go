package require

import (
	"errors"
	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	n "github.com/komari-monitor/komari/pkg/jsruntime/native"
	"path/filepath"
	"testing"
)

func TestModules(t *testing.T) {
	files := map[string]string{
		"app/main.js":                       "exports.a=require('./a'); exports.json=require('./data.json'); exports.later=function(){return require('./a')}; exports.pkg=require('pkg')",
		"app/a.js":                          "exports.n=42;exports.cycle=require('./b').a",
		"app/b.js":                          "exports.a=require('./a')",
		"app/data.json":                     `{"ok":true}`,
		"app/node_modules/pkg/package.json": `{"main":"lib.js"}`,
		"app/node_modules/pkg/lib.js":       "module.exports=7",
	}
	loads := map[string]int{}
	reg := NewRegistry(WithLoader(func(p string) ([]byte, error) {
		p = filepath.ToSlash(p)
		loads[p]++
		if s, ok := files[p]; ok {
			return []byte(s), nil
		}
		return nil, ModuleFileDoesNotExistError
	}))
	vm := moejs.NewRuntime(moejs.Options{})
	reg.Enable(vm)
	v, err := n.RunScript(vm, "entry.js", "var m=require('./app/main'); m.a.n===42 && m.a.cycle===m.a && m.json.ok && m.later()===m.a && m.pkg===7")
	if err != nil || !n.Bool(v) {
		t.Fatalf("result %v error %v loads %v", v, err, loads)
	}
	if loads["app/a.js"] != 1 {
		t.Fatalf("module loaded %d times", loads["app/a.js"])
	}
	// Compiled scripts are reusable across runtimes, exports are not shared.
	vm2 := moejs.NewRuntime(moejs.Options{})
	reg.Enable(vm2)
	if _, err := n.Run(vm2, "require('./app/main')"); err != nil {
		t.Fatal(err)
	}
	if loads["app/a.js"] != 1 {
		t.Fatal("compiled cache missed")
	}
}
func TestNativeAndPermissions(t *testing.T) {
	denied := errors.New("permission denied")
	reg := NewRegistry(WithLoader(func(p string) ([]byte, error) { return nil, denied }))
	reg.RegisterNativeModule("test", func(vm *moejs.Runtime, m *engine.Object) {
		if err := n.Set(vm, m, "exports", 12); err != nil {
			panic(err)
		}
	})
	vm := moejs.NewRuntime(moejs.Options{})
	r := reg.Enable(vm)
	v, err := r.Require("test")
	if err != nil || n.Int(vm, v) != 12 {
		t.Fatal(v, err)
	}
	if _, err := r.Require("./secret"); !errors.Is(err, denied) {
		t.Fatal("permission lost", err)
	}
	v, err = n.Run(vm, "try{require('./secret');false}catch(e){e.message==='permission denied'}")
	if err != nil || !n.Bool(v) {
		t.Fatal(v, err)
	}
}
