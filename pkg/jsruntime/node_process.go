package jsruntime

import (
	"fmt"
	"math/big"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/utils"
)

func (r *Runtime) loadProcessModule(vm *goja.Runtime, module *goja.Object) {
	process := newEventEmitter(vm)
	environment := make(map[string]string)
	for _, item := range os.Environ() {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			environment[parts[0]] = parts[1]
		}
	}
	_ = process.Set("env", environment)
	_ = process.Set("argv", append([]string{os.Args[0], "script.js"}, os.Args[1:]...))
	_ = process.Set("execArgv", []string{})
	_ = process.Set("execPath", os.Args[0])
	_ = process.Set("pid", os.Getpid())
	_ = process.Set("ppid", os.Getppid())
	_ = process.Set("platform", nodePlatform())
	_ = process.Set("arch", nodeArch())
	_ = process.Set("version", "v"+strings.TrimPrefix(runtime.Version(), "go"))
	_ = process.Set("versions", map[string]string{"node": "0.0.0-goja", "go": runtime.Version(), "komari": utils.CurrentVersion, "hash": utils.VersionHash})
	_ = process.Set("release", map[string]string{"name": "node", "sourceUrl": "", "headersUrl": ""})
	_ = process.Set("title", "komari-jsruntime")
	_ = process.Set("exitCode", 0)
	_ = process.Set("connected", false)
	_ = process.Set("config", map[string]any{})
	_ = process.Set("cwd", func() string { return r.nodeCwd })
	_ = process.Set("chdir", func(path string) {
		resolved, err := r.resolveNodePath(path, true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			if err == nil {
				err = fmt.Errorf("not a directory: %s", path)
			}
			panic(vm.NewGoError(err))
		}
		r.nodeCwd = resolved
	})
	_ = process.Set("uptime", func() float64 { return time.Since(nodeProcessStartedAt).Seconds() })
	memoryUsage := vm.ToValue(func() map[string]uint64 {
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		usage, err := readNodeProcessMetrics()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return map[string]uint64{"rss": usage.rss, "heapTotal": memory.HeapSys, "heapUsed": memory.HeapAlloc, "external": memory.OtherSys, "arrayBuffers": 0}
	}).ToObject(vm)
	_ = memoryUsage.Set("rss", func() uint64 {
		usage, err := readNodeProcessMetrics()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return usage.rss
	})
	_ = process.Set("memoryUsage", memoryUsage)
	_ = process.Set("cpuUsage", func(call goja.FunctionCall) goja.Value {
		usage, err := readNodeProcessMetrics()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		userMicros, systemMicros := usage.userCPU.Microseconds(), usage.systemCPU.Microseconds()
		if previous := call.Argument(0); !goja.IsUndefined(previous) && !goja.IsNull(previous) {
			object := previous.ToObject(vm)
			userMicros -= object.Get("user").ToInteger()
			systemMicros -= object.Get("system").ToInteger()
		}
		return vm.ToValue(map[string]int64{"user": max(userMicros, 0), "system": max(systemMicros, 0)})
	})
	_ = process.Set("resourceUsage", func() map[string]any {
		usage, err := readNodeProcessMetrics()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return map[string]any{
			"userCPUTime": usage.userCPU.Microseconds(), "systemCPUTime": usage.systemCPU.Microseconds(), "maxRSS": usage.maxRSS,
			"minorPageFault": usage.minorPageFault, "majorPageFault": usage.majorPageFault,
			"fsRead": usage.fsRead, "fsWrite": usage.fsWrite,
			"voluntaryContextSwitches": usage.voluntarySwitches, "involuntaryContextSwitches": usage.involuntarySwitches,
		}
	})
	hrtime := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		now := time.Since(r.startedAt)
		seconds := int64(now / time.Second)
		nanoseconds := int64(now % time.Second)
		if previous := call.Argument(0); !goja.IsUndefined(previous) {
			var values []int64
			if err := vm.ExportTo(previous, &values); err == nil && len(values) >= 2 {
				seconds -= values[0]
				nanoseconds -= values[1]
				if nanoseconds < 0 {
					seconds--
					nanoseconds += int64(time.Second)
				}
			}
		}
		return vm.ToValue([]int64{seconds, nanoseconds})
	}).ToObject(vm)
	_ = hrtime.Set("bigint", func() *big.Int { return big.NewInt(time.Since(r.startedAt).Nanoseconds()) })
	_ = process.Set("hrtime", hrtime)
	_ = process.Set("nextTick", func(call goja.FunctionCall) goja.Value {
		callback, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		arguments := append([]goja.Value(nil), call.Arguments[1:]...)
		r.loop.SetTimeout(func(vm *goja.Runtime) {
			r.runAsyncJob(vm, "process.nextTick", func() error {
				_, err := callback(goja.Undefined(), arguments...)
				return err
			})
		}, 0)
		return goja.Undefined()
	})
	_ = process.Set("emitWarning", func(call goja.FunctionCall) goja.Value {
		r.writeConsole(consoleError, []goja.Value{vm.ToValue("Warning: " + call.Argument(0).String())}, false)
		return goja.Undefined()
	})
	_ = process.Set("kill", func(pid int, signal string) bool {
		if !r.allowExec {
			panic(vm.NewGoError(fmt.Errorf("process.kill requires AllowExec")))
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if signal == "SIGKILL" {
			err = process.Kill()
		} else {
			err = process.Signal(os.Interrupt)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return true
	})
	_ = process.Set("exit", func(call goja.FunctionCall) goja.Value {
		code := call.Argument(0).ToInteger()
		_ = process.Set("exitCode", code)
		panic(vm.NewGoError(fmt.Errorf("process.exit(%d) requested", code)))
	})
	_ = process.Set("abort", func() { panic(vm.NewGoError(fmt.Errorf("process.abort() requested"))) })
	_ = process.Set("stdout", processStream(vm, os.Stdout, true))
	_ = process.Set("stderr", processStream(vm, os.Stderr, true))
	_ = process.Set("stdin", processStream(vm, nil, false))
	_ = module.Set("exports", process)
}

func processStream(vm *goja.Runtime, output *os.File, writable bool) *goja.Object {
	stream := newEventEmitter(vm)
	_ = stream.Set("isTTY", false)
	_ = stream.Set("fd", -1)
	_ = stream.Set("write", func(call goja.FunctionCall) goja.Value {
		if !writable || output == nil {
			return vm.ToValue(false)
		}
		_, _ = fmt.Fprint(output, call.Argument(0).String())
		if callback, ok := goja.AssertFunction(call.Argument(2)); ok {
			_, _ = callback(goja.Undefined())
		}
		return vm.ToValue(true)
	})
	_ = stream.Set("setEncoding", func() *goja.Object { return stream })
	_ = stream.Set("resume", func() *goja.Object { return stream })
	_ = stream.Set("pause", func() *goja.Object { return stream })
	return stream
}
