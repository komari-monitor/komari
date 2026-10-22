package process

import (
	_ "embed"
	"fmt"
	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/buffer"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/fs"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/metrics"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"github.com/komari-monitor/komari/utils"
	"math/big"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

type nodeNextTick struct {
	callback  moejs.Value
	arguments []moejs.Value
}
type Module struct {
	runtime           *bridge.Runtime
	fs                *fs.Module
	allowExec         bool
	startedAt         time.Time
	reportError       func(*moejs.Runtime, []moejs.Value)
	nextTickMu        sync.Mutex
	nextTicks         []nodeNextTick
	nextTickScheduled bool
	nextTickDraining  bool
	nextTickTurn      moejs.Value
	nextTickSchedule  moejs.Value
}

func New(runtime *bridge.Runtime, filesystem *fs.Module, allowExec bool, startedAt time.Time, reportError func(*moejs.Runtime, []moejs.Value)) *Module {
	return &Module{runtime: runtime, fs: filesystem, allowExec: allowExec, startedAt: startedAt, reportError: reportError}
}
func (m *Module) Load(vm *moejs.Runtime, module *engine.Object) {
	process := events.NewEmitter(vm)
	environment := make(map[string]string)
	for _, item := range os.Environ() {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			environment[parts[0]] = parts[1]
		}
	}
	_ = native.Set(vm, process, "env", environment)
	_ = native.Set(vm, process, "argv", append([]string{os.Args[0], "script.js"}, os.Args[1:]...))
	_ = native.Set(vm, process, "execArgv", []string{})
	_ = native.Set(vm, process, "execPath", os.Args[0])
	_ = native.Set(vm, process, "pid", os.Getpid())
	_ = native.Set(vm, process, "ppid", os.Getppid())
	_ = native.Set(vm, process, "platform", metrics.Platform())
	_ = native.Set(vm, process, "arch", metrics.Arch())
	_ = native.Set(vm, process, "version", "v"+strings.TrimPrefix(runtime.Version(), "go"))
	_ = native.Set(vm, process, "versions", map[string]string{"node": "0.0.0-moejs", "go": runtime.Version(), "komari": utils.CurrentVersion, "hash": utils.VersionHash})
	_ = native.Set(vm, process, "release", map[string]string{"name": "node", "sourceUrl": "", "headersUrl": ""})
	_ = native.Set(vm, process, "title", "komari-jsruntime")
	_ = native.Set(vm, process, "exitCode", 0)
	_ = native.Set(vm, process, "connected", false)
	_ = native.Set(vm, process, "config", map[string]any{})
	_ = native.Set(vm, process, "cwd", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, m.fs.Cwd()), nil
	}))
	_ = native.Set(vm, process, "chdir", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		path := native.String(vm, moejs.Arg(args, 0))
		if err := m.fs.Chdir(path); err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	_ = native.Set(vm, process, "uptime", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, time.Since(m.startedAt).Seconds()), nil
	}))
	memoryUsage := native.Object(vm, native.From(vm, native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		usage, err := metrics.ReadProcess()
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, map[string]uint64{"rss": usage.RSS, "heapTotal": memory.HeapSys, "heapUsed": memory.HeapAlloc, "external": memory.OtherSys, "arrayBuffers": 0}), nil
	})))
	_ = native.Set(vm, memoryUsage, "rss", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		usage, err := metrics.ReadProcess()
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, usage.RSS), nil
	}))
	_ = native.Set(vm, process, "memoryUsage", memoryUsage)
	_ = native.Set(vm, process, "cpuUsage", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		usage, err := metrics.ReadProcess()
		if err != nil {
			panic(native.GoError(vm, err))
		}
		userMicros, systemMicros := usage.UserCPU.Microseconds(), usage.SystemCPU.Microseconds()
		if previous := moejs.Arg(args, 0); !previous.IsUndefined() && !previous.IsNull() {
			object := native.Object(vm, previous)
			userMicros -= native.Int(vm, native.Get(vm, object, "user"))
			systemMicros -= native.Int(vm, native.Get(vm, object, "system"))
		}
		return native.From(vm, map[string]int64{"user": max(userMicros, 0), "system": max(systemMicros, 0)}), nil
	}))
	_ = native.Set(vm, process, "resourceUsage", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		usage, err := metrics.ReadProcess()
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, map[string]any{"userCPUTime": usage.UserCPU.Microseconds(), "systemCPUTime": usage.SystemCPU.Microseconds(), "maxRSS": usage.MaxRSS, "minorPageFault": usage.MinorPageFault, "majorPageFault": usage.MajorPageFault, "fsRead": usage.FSRead, "fsWrite": usage.FSWrite, "voluntaryContextSwitches": usage.VoluntarySwitches, "involuntaryContextSwitches": usage.InvoluntarySwitches}), nil
	}))
	hrtime := native.Object(vm, native.From(vm, native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		now := time.Since(m.startedAt)
		seconds := int64(now / time.Second)
		nanoseconds := int64(now % time.Second)
		if previous := moejs.Arg(args, 0); !previous.IsUndefined() {
			var values []int64
			if err := native.ExportTo(vm, previous, &values); err == nil && len(values) >= 2 {
				seconds -= values[0]
				nanoseconds -= values[1]
				if nanoseconds < 0 {
					seconds--
					nanoseconds += int64(time.Second)
				}
			}
		}
		return native.From(vm, []int64{seconds, nanoseconds}), nil
	})))
	_ = native.Set(vm, hrtime, "bigint", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, big.NewInt(time.Since(m.startedAt).Nanoseconds())), nil
	}))
	_ = native.Set(vm, process, "hrtime", hrtime)
	_ = native.Set(vm, process, "nextTick", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		callback, ok := checkedFunction(moejs.Arg(args, 0))
		if !ok {
			panic(native.TypeError(vm, "callback must be a function"))
		}
		arguments := append([]moejs.Value(nil), args[1:]...)
		m.enqueueNextTick(vm, nodeNextTick{callback: callback, arguments: arguments})
		return native.From(vm, moejs.Undefined()), nil
	}))
	_ = native.Set(vm, process, "emitWarning", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if m.reportError != nil {
			m.reportError(vm, []moejs.Value{native.From(vm, "Warning: "+native.String(vm, moejs.Arg(args, 0)))})
		}
		return native.From(vm, moejs.Undefined()), nil
	}))
	_ = native.Set(vm, process, "kill", native.Bind(vm, "", 2, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		pid := int(native.Int(vm, moejs.Arg(args, 0)))
		signal := native.String(vm, moejs.Arg(args, 1))
		if !m.allowExec {
			panic(native.GoError(vm, fmt.Errorf("process.kill requires AllowExec")))
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		if signal == "SIGKILL" {
			err = process.Kill()
		} else {
			err = process.Signal(os.Interrupt)
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, true), nil
	}))
	_ = native.Set(vm, process, "exit", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		code := native.Int(vm, moejs.Arg(args, 0))
		_ = native.Set(vm, process, "exitCode", code)
		panic(native.GoError(vm, &bridge.ExitRequestedError{Code: code}))
	}))
	_ = native.Set(vm, process, "abort", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("process.abort() requested")))
	}))
	m.attachProcessStreams(vm, process)
	if err := m.initNextTickScheduler(vm); err != nil {
		panic(native.GoError(vm, err))
	}
	_ = native.Set(vm, module, "exports", process)
}

//go:embed stdio.js
var stdioSource string

func (m *Module) attachProcessStreams(vm *moejs.Runtime, process *engine.Object) {
	factoryValue, err := native.Run(vm, stdioSource)
	if err != nil {
		panic(native.GoError(vm, fmt.Errorf("load process stdio: %w", err)))
	}
	factory, _ := checkedFunction(factoryValue)
	hooks := vm.Realm().NewObject()
	_ = native.Set(vm, hooks, "write", native.Bind(vm, "", 3, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		chunk := moejs.Arg(args, 1)
		callback := moejs.Arg(args, 2)
		var output *os.File
		switch name {
		case "stdout":
			output = os.Stdout
		case "stderr":
			output = os.Stderr
		default:
			panic(native.GoError(vm, fmt.Errorf("process.%s.write is not supported by jsruntime; the stream is not connected", name)))
		}
		_, _ = output.Write(buffer.Bytes(vm, chunk))
		if !native.IsNil(callback) {
			_, _ = native.Call(vm, callback, moejs.Undefined())
		}
		return moejs.Undefined(), nil
	}))
	streams, err := native.Call(vm, factory, moejs.Undefined(), require.Require(vm, "stream"), engine.ObjectValue(hooks))
	if err != nil {
		panic(err)
	}
	streamsObject := native.Object(vm, streams)
	for _, name := range []string{"stdout", "stderr", "stdin"} {
		stream := native.Object(vm, native.Get(vm, streamsObject, name))
		_ = native.Set(vm, stream, "isTTY", false)
		_ = native.Set(vm, stream, "fd", -1)
		_ = native.Set(vm, process, name, stream)
	}
}
func checkedFunction(v moejs.Value) (moejs.Value, bool) { return v, engine.IsCallable(v) }
