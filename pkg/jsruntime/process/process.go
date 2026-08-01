package process

import (
	_ "embed"
	"fmt"
	"math/big"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/fs"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/metrics"
	"github.com/komari-monitor/komari/utils"
)

type nodeNextTick struct {
	callback  goja.Callable
	arguments []goja.Value
}

type Module struct {
	runtime     *bridge.Runtime
	fs          *fs.Module
	allowExec   bool
	startedAt   time.Time
	reportError func(*goja.Runtime, []goja.Value)

	nextTickMu        sync.Mutex
	nextTicks         []nodeNextTick
	nextTickScheduled bool
	nextTickDraining  bool
	nextTickTurn      goja.Callable
	nextTickSchedule  goja.Callable
}

func New(runtime *bridge.Runtime, filesystem *fs.Module, allowExec bool, startedAt time.Time, reportError func(*goja.Runtime, []goja.Value)) *Module {
	return &Module{runtime: runtime, fs: filesystem, allowExec: allowExec, startedAt: startedAt, reportError: reportError}
}

func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	process := events.NewEmitter(vm)
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
	_ = process.Set("platform", metrics.Platform())
	_ = process.Set("arch", metrics.Arch())
	_ = process.Set("version", "v"+strings.TrimPrefix(runtime.Version(), "go"))
	_ = process.Set("versions", map[string]string{"node": "0.0.0-goja", "go": runtime.Version(), "komari": utils.CurrentVersion, "hash": utils.VersionHash})
	_ = process.Set("release", map[string]string{"name": "node", "sourceUrl": "", "headersUrl": ""})
	_ = process.Set("title", "komari-jsruntime")
	_ = process.Set("exitCode", 0)
	_ = process.Set("connected", false)
	_ = process.Set("config", map[string]any{})
	_ = process.Set("cwd", func() string { return m.fs.Cwd() })
	_ = process.Set("chdir", func(path string) {
		if err := m.fs.Chdir(path); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = process.Set("uptime", func() float64 { return time.Since(m.startedAt).Seconds() })
	memoryUsage := vm.ToValue(func() map[string]uint64 {
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		usage, err := metrics.ReadProcess()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return map[string]uint64{"rss": usage.RSS, "heapTotal": memory.HeapSys, "heapUsed": memory.HeapAlloc, "external": memory.OtherSys, "arrayBuffers": 0}
	}).ToObject(vm)
	_ = memoryUsage.Set("rss", func() uint64 {
		usage, err := metrics.ReadProcess()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return usage.RSS
	})
	_ = process.Set("memoryUsage", memoryUsage)
	_ = process.Set("cpuUsage", func(call goja.FunctionCall) goja.Value {
		usage, err := metrics.ReadProcess()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		userMicros, systemMicros := usage.UserCPU.Microseconds(), usage.SystemCPU.Microseconds()
		if previous := call.Argument(0); !goja.IsUndefined(previous) && !goja.IsNull(previous) {
			object := previous.ToObject(vm)
			userMicros -= object.Get("user").ToInteger()
			systemMicros -= object.Get("system").ToInteger()
		}
		return vm.ToValue(map[string]int64{"user": max(userMicros, 0), "system": max(systemMicros, 0)})
	})
	_ = process.Set("resourceUsage", func() map[string]any {
		usage, err := metrics.ReadProcess()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return map[string]any{
			"userCPUTime": usage.UserCPU.Microseconds(), "systemCPUTime": usage.SystemCPU.Microseconds(), "maxRSS": usage.MaxRSS,
			"minorPageFault": usage.MinorPageFault, "majorPageFault": usage.MajorPageFault,
			"fsRead": usage.FSRead, "fsWrite": usage.FSWrite,
			"voluntaryContextSwitches": usage.VoluntarySwitches, "involuntaryContextSwitches": usage.InvoluntarySwitches,
		}
	})
	hrtime := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		now := time.Since(m.startedAt)
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
	_ = hrtime.Set("bigint", func() *big.Int { return big.NewInt(time.Since(m.startedAt).Nanoseconds()) })
	_ = process.Set("hrtime", hrtime)
	_ = process.Set("nextTick", func(call goja.FunctionCall) goja.Value {
		callback, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		arguments := append([]goja.Value(nil), call.Arguments[1:]...)
		m.enqueueNextTick(vm, nodeNextTick{callback: callback, arguments: arguments})
		return goja.Undefined()
	})
	_ = process.Set("emitWarning", func(call goja.FunctionCall) goja.Value {
		if m.reportError != nil {
			m.reportError(vm, []goja.Value{vm.ToValue("Warning: " + call.Argument(0).String())})
		}
		return goja.Undefined()
	})
	_ = process.Set("kill", func(pid int, signal string) bool {
		if !m.allowExec {
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
		panic(vm.NewGoError(&bridge.ExitRequestedError{Code: code}))
	})
	_ = process.Set("abort", func() { panic(vm.NewGoError(fmt.Errorf("process.abort() requested"))) })
	m.attachProcessStreams(vm, process)
	if err := m.initNextTickScheduler(vm); err != nil {
		panic(vm.NewGoError(err))
	}
	_ = module.Set("exports", process)
}

//go:embed stdio.js
var stdioSource string

// attachProcessStreams wires process.stdout/stderr as Writable streams and
// process.stdin as a Readable stream backed by the jsruntime stream module.
func (m *Module) attachProcessStreams(vm *goja.Runtime, process *goja.Object) {
	factoryValue, err := vm.RunString(stdioSource)
	if err != nil {
		panic(vm.NewGoError(fmt.Errorf("load process stdio: %w", err)))
	}
	factory, _ := goja.AssertFunction(factoryValue)
	hooks := vm.NewObject()
	_ = hooks.Set("write", func(name string, chunk goja.Value, callback goja.Callable) {
		var output *os.File
		switch name {
		case "stdout":
			output = os.Stdout
		case "stderr":
			output = os.Stderr
		default:
			panic(vm.NewGoError(fmt.Errorf("process.%s.write is not supported by jsruntime; the stream is not connected", name)))
		}
		_, _ = output.Write(buffer.Bytes(vm, chunk))
		if callback != nil {
			_, _ = callback(goja.Undefined())
		}
	})
	streams, err := factory(goja.Undefined(), require.Require(vm, "stream"), hooks)
	if err != nil {
		panic(err)
	}
	streamsObject := streams.ToObject(vm)
	for _, name := range []string{"stdout", "stderr", "stdin"} {
		stream := streamsObject.Get(name).ToObject(vm)
		_ = stream.Set("isTTY", false)
		_ = stream.Set("fd", -1)
		_ = process.Set(name, stream)
	}
}
