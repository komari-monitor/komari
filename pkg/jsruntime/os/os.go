package os

import (
	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/metrics"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"net"
	"os"
	"os/user"
	"runtime"
	"unsafe"
)

func Load(vm *moejs.Runtime, module *engine.Object) {
	exports := vm.Realm().NewObject()
	hostname, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	currentUser, _ := user.Current()
	systemMetrics := func() metrics.System {
		result, err := metrics.ReadSystem()
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return result
	}
	_ = native.Set(vm, exports, "arch", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, metrics.Arch()), nil
	}))
	_ = native.Set(vm, exports, "platform", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, metrics.Platform()), nil
	}))
	_ = native.Set(vm, exports, "type", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		switch runtime.GOOS {
		case "windows":
			return native.From(vm, "Windows_NT"), nil
		case "darwin":
			return native.From(vm, "Darwin"), nil
		case "freebsd":
			return native.From(vm, "FreeBSD"), nil
		case "openbsd":
			return native.From(vm, "OpenBSD"), nil
		case "netbsd":
			return native.From(vm, "NetBSD"), nil
		case "aix":
			return native.From(vm, "AIX"), nil
		default:
			return native.From(vm, runtime.GOOS), nil
		}
	}))
	_ = native.Set(vm, exports, "release", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, systemMetrics().Release), nil
	}))
	_ = native.Set(vm, exports, "version", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, systemMetrics().Version), nil
	}))
	_ = native.Set(vm, exports, "machine", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, runtime.GOARCH), nil
	}))
	_ = native.Set(vm, exports, "hostname", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, hostname), nil
	}))
	_ = native.Set(vm, exports, "homedir", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, home), nil
	}))
	_ = native.Set(vm, exports, "tmpdir", native.Bind(vm, "tmpdir", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, os.TempDir()), nil
	}))
	_ = native.Set(vm, exports, "endianness", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		var value uint16 = 1
		if *(*byte)(unsafe.Pointer(&value)) == 1 {
			return native.From(vm, "LE"), nil
		}
		return native.From(vm, "BE"), nil
	}))
	_ = native.Set(vm, exports, "EOL", map[bool]string{true: "\r\n", false: "\n"}[runtime.GOOS == "windows"])
	_ = native.Set(vm, exports, "devNull", map[bool]string{true: `\\.\nul`, false: "/dev/null"}[runtime.GOOS == "windows"])
	_ = native.Set(vm, exports, "uptime", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, systemMetrics().Uptime), nil
	}))
	_ = native.Set(vm, exports, "loadavg", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		result := systemMetrics()
		return native.From(vm, result.LoadAvg[:]), nil
	}))
	_ = native.Set(vm, exports, "totalmem", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, systemMetrics().TotalMem), nil
	}))
	_ = native.Set(vm, exports, "freemem", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, systemMetrics().FreeMem), nil
	}))
	_ = native.Set(vm, exports, "availableParallelism", native.Bind(vm, "availableParallelism", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, runtime.NumCPU()), nil
	}))
	_ = native.Set(vm, exports, "cpus", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, metrics.CPUInfoValues(systemMetrics().CPUs)), nil
	}))
	_ = native.Set(vm, exports, "userInfo", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if currentUser == nil {
			return native.From(vm, map[string]any{"uid": -1, "gid": -1, "username": "", "homedir": home, "shell": ""}), nil
		}
		return native.From(vm, map[string]any{"uid": currentUser.Uid, "gid": currentUser.Gid, "username": currentUser.Username, "homedir": currentUser.HomeDir, "shell": ""}), nil
	}))
	_ = native.Set(vm, exports, "networkInterfaces", native.Bind(vm, "networkInterfaces", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, nodeNetworkInterfaces()), nil
	}))
	_ = native.Set(vm, exports, "constants", map[string]any{"signals": map[string]int{"SIGINT": 2, "SIGTERM": 15, "SIGKILL": 9}, "errno": map[string]int{}})
	_ = native.Set(vm, module, "exports", exports)
}
func nodeNetworkInterfaces() map[string][]map[string]any {
	result := make(map[string][]map[string]any)
	interfaces, _ := net.Interfaces()
	for _, networkInterface := range interfaces {
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			ip, network, err := net.ParseCIDR(address.String())
			if err != nil {
				continue
			}
			family := "IPv6"
			if ip.To4() != nil {
				family = "IPv4"
			}
			result[networkInterface.Name] = append(result[networkInterface.Name], map[string]any{"address": ip.String(), "netmask": net.IP(network.Mask).String(), "family": family, "mac": networkInterface.HardwareAddr.String(), "internal": networkInterface.Flags&net.FlagLoopback != 0, "cidr": address.String()})
		}
	}
	return result
}
