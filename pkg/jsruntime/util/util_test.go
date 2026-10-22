package util

import (
	moe "github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"testing"
)

func TestNativeFormat(t *testing.T) {
	vm := moe.NewRuntime(moe.Options{})
	require.NewRegistry().Enable(vm)
	for _, s := range []string{
		`require("util").format("value=%d",2)==="value=2"`,
		`require("util").format("%s %d %j %%","ok",3,{a:1})==='ok 3 {"a":1} %'`,
		`require("util").format("%i %f", "12px", "1.5em")==="12 1.5"`,
		`require("util").format("x",1,true)==="x 1 true"`,
		`require("util").format()===""`,
		`(function(){const x={};x.x=x;return require("util").format("%j",x)==="[Circular]"})()`,
	} {
		v, err := native.Run(vm, s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if !native.Bool(v) {
			t.Errorf("false: %s", s)
		}
	}
}
