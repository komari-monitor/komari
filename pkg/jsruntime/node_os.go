package jsruntime

import (
	"net"
	"os"
	"os/user"
	"runtime"
	"unsafe"

	"github.com/dop251/goja"
)

func (r *Runtime) loadOSModule(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	hostname, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	currentUser, _ := user.Current()
	systemMetrics := func() nodeSystemMetrics {
		metrics, err := readNodeSystemMetrics()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return metrics
	}
	_ = exports.Set("arch", func() string { return nodeArch() })
	_ = exports.Set("platform", func() string { return nodePlatform() })
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
	_ = exports.Set("release", func() string { return systemMetrics().release })
	_ = exports.Set("version", func() string { return systemMetrics().version })
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
	_ = exports.Set("uptime", func() float64 { return systemMetrics().uptime })
	_ = exports.Set("loadavg", func() []float64 { metrics := systemMetrics(); return metrics.loadavg[:] })
	_ = exports.Set("totalmem", func() uint64 { return systemMetrics().totalMem })
	_ = exports.Set("freemem", func() uint64 { return systemMetrics().freeMem })
	_ = exports.Set("availableParallelism", runtime.NumCPU)
	_ = exports.Set("cpus", func() []map[string]any { return cpuInfoValues(systemMetrics().cpus) })
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

func nodeArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func nodePlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}
