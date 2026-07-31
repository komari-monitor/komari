package os

import (
	"net"
	"os"
	"os/user"
	"runtime"
	"unsafe"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/metrics"
)

func Load(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	hostname, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	currentUser, _ := user.Current()
	systemMetrics := func() metrics.System {
		result, err := metrics.ReadSystem()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return result
	}
	_ = exports.Set("arch", func() string { return metrics.Arch() })
	_ = exports.Set("platform", func() string { return metrics.Platform() })
	_ = exports.Set("type", func() string {
		switch runtime.GOOS {
		case "windows":
			return "Windows_NT"
		case "darwin":
			return "Darwin"
		case "freebsd":
			return "FreeBSD"
		case "openbsd":
			return "OpenBSD"
		case "netbsd":
			return "NetBSD"
		case "aix":
			return "AIX"
		default:
			return runtime.GOOS
		}
	})
	_ = exports.Set("release", func() string { return systemMetrics().Release })
	_ = exports.Set("version", func() string { return systemMetrics().Version })
	_ = exports.Set("machine", func() string { return runtime.GOARCH })
	_ = exports.Set("hostname", func() string { return hostname })
	_ = exports.Set("homedir", func() string { return home })
	_ = exports.Set("tmpdir", os.TempDir)
	_ = exports.Set("endianness", func() string {
		var value uint16 = 1
		if *(*byte)(unsafe.Pointer(&value)) == 1 {
			return "LE"
		}
		return "BE"
	})
	_ = exports.Set("EOL", map[bool]string{true: "\r\n", false: "\n"}[runtime.GOOS == "windows"])
	_ = exports.Set("devNull", map[bool]string{true: `\\.\nul`, false: "/dev/null"}[runtime.GOOS == "windows"])
	_ = exports.Set("uptime", func() float64 { return systemMetrics().Uptime })
	_ = exports.Set("loadavg", func() []float64 { result := systemMetrics(); return result.LoadAvg[:] })
	_ = exports.Set("totalmem", func() uint64 { return systemMetrics().TotalMem })
	_ = exports.Set("freemem", func() uint64 { return systemMetrics().FreeMem })
	_ = exports.Set("availableParallelism", runtime.NumCPU)
	_ = exports.Set("cpus", func() []map[string]any { return metrics.CPUInfoValues(systemMetrics().CPUs) })
	_ = exports.Set("userInfo", func() map[string]any {
		if currentUser == nil {
			return map[string]any{"uid": -1, "gid": -1, "username": "", "homedir": home, "shell": ""}
		}
		return map[string]any{"uid": currentUser.Uid, "gid": currentUser.Gid, "username": currentUser.Username, "homedir": currentUser.HomeDir, "shell": ""}
	})
	_ = exports.Set("networkInterfaces", nodeNetworkInterfaces)
	_ = exports.Set("constants", map[string]any{"signals": map[string]int{"SIGINT": 2, "SIGTERM": 15, "SIGKILL": 9}, "errno": map[string]int{}})
	_ = module.Set("exports", exports)
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
			result[networkInterface.Name] = append(result[networkInterface.Name], map[string]any{
				"address": ip.String(), "netmask": net.IP(network.Mask).String(), "family": family,
				"mac": networkInterface.HardwareAddr.String(), "internal": networkInterface.Flags&net.FlagLoopback != 0, "cidr": address.String(),
			})
		}
	}
	return result
}
