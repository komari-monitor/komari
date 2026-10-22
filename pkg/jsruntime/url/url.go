// Package url supplies native URL parsing and JavaScript web URL objects.
package url

import (
	_ "embed"
	"encoding/json"
	moe "github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"golang.org/x/net/idna"
	"net"
	neturl "net/url"
	"strconv"
	"strings"
)

const ModuleName = "url"

//go:embed url.js
var source string

func parse(input, base string) (map[string]string, error) {
	u, err := neturl.Parse(strings.TrimSpace(input))
	if err != nil {
		return nil, err
	}
	if base != "" {
		b, e := neturl.Parse(base)
		if e != nil || b.Scheme == "" {
			return nil, &neturl.Error{Op: "parse", URL: base, Err: net.InvalidAddrError("Invalid base URL")}
		}
		u = b.ResolveReference(u)
	}
	if u.Scheme == "" {
		return nil, net.InvalidAddrError("Invalid URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	hostname := strings.ToLower(u.Hostname())
	if h, e := idna.Lookup.ToASCII(hostname); e == nil {
		hostname = h
	}
	port := u.Port()
	if port != "" {
		p, e := strconv.Atoi(port)
		if e != nil || p > 65535 {
			return nil, net.InvalidAddrError("Invalid port")
		}
		port = strconv.Itoa(p)
	}
	if (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "ftp" || u.Scheme == "ws" || u.Scheme == "wss") && hostname == "" {
		return nil, net.InvalidAddrError("Invalid host")
	}
	if ((u.Scheme == "https" || u.Scheme == "wss") && port == "443") || ((u.Scheme == "http" || u.Scheme == "ws") && port == "80") || (u.Scheme == "ftp" && port == "21") {
		port = ""
	}
	host := hostname
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	u.Host = host
	if host != "" && u.Path == "" {
		u.Path = "/"
	}
	username, password := "", ""
	if u.User != nil {
		parts := strings.SplitN(u.User.String(), ":", 2)
		username = parts[0]
		if len(parts) == 2 {
			password = parts[1]
		}
	}
	origin := "null"
	if u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "ftp" || u.Scheme == "ws" || u.Scheme == "wss" {
		origin = u.Scheme + "://" + host
	}
	search := ""
	if u.RawQuery != "" || u.ForceQuery {
		search = "?" + u.RawQuery
	}
	hash := ""
	if u.Fragment != "" {
		hash = "#" + u.EscapedFragment()
	}
	return map[string]string{"href": u.String(), "protocol": u.Scheme + ":", "hostname": func() string {
		if strings.Contains(hostname, ":") {
			return "[" + hostname + "]"
		}
		return hostname
	}(), "host": host, "port": port, "pathname": u.EscapedPath(), "search": search, "hash": hash, "username": username, "password": password, "origin": origin}, nil
}
func Require(vm *moe.Runtime, module *engine.Object) {
	factory, err := native.RunScript(vm, "url.js", source)
	if err != nil {
		panic(err)
	}
	parser := native.Bind(vm, "parseURL", 2, moe.NativeFunc(func(_ *moe.Realm, _ moe.Value, a []moe.Value) (moe.Value, error) {
		base := ""
		if !native.IsNil(moe.Arg(a, 1)) {
			base = native.String(vm, moe.Arg(a, 1))
		}
		m, e := parse(native.String(vm, moe.Arg(a, 0)), base)
		if e != nil {
			return moe.Undefined(), vm.Realm().TypeError("Invalid URL: %s", e)
		}
		b, _ := json.Marshal(m)
		return native.From(vm, string(b)), nil
	}))
	domain := native.Bind(vm, "domain", 2, moe.NativeFunc(func(_ *moe.Realm, _ moe.Value, a []moe.Value) (moe.Value, error) {
		s := native.String(vm, moe.Arg(a, 0))
		var result string
		var e error
		if native.Bool(moe.Arg(a, 1)) {
			result, e = idna.Lookup.ToUnicode(s)
		} else {
			result, e = idna.Lookup.ToASCII(s)
		}
		if e != nil {
			result = ""
		}
		return native.From(vm, result), nil
	}))
	decodeForm := native.Bind(vm, "decodeForm", 1, moe.NativeFunc(func(_ *moe.Realm, _ moe.Value, a []moe.Value) (moe.Value, error) {
		s := native.String(vm, moe.Arg(a, 0))
		var out strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == '+' {
				out.WriteByte(' ')
				continue
			}
			if s[i] == '%' && i+2 < len(s) {
				if b, e := strconv.ParseUint(s[i+1:i+3], 16, 8); e == nil {
					out.WriteByte(byte(b))
					i += 2
					continue
				}
			}
			out.WriteByte(s[i])
		}
		return native.From(vm, string([]rune(out.String()))), nil
	}))
	v, err := vm.CallFunction(factory, moe.Undefined(), parser, domain, decodeForm)
	if err != nil {
		panic(err)
	}
	if err = native.Set(vm, module, "exports", v); err != nil {
		panic(err)
	}
}
func Enable(vm *moe.Runtime) {
	m := require.Require(vm, ModuleName).AsObject()
	for _, name := range []string{"URL", "URLSearchParams"} {
		if err := vm.SetGlobal(name, native.Get(vm, m, name)); err != nil {
			panic(err)
		}
	}
}
func init() { require.RegisterCoreModule(ModuleName, Require) }
