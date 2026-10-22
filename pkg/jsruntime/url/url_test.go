package url

import (
	moe "github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"testing"
)

func TestNativeURL(t *testing.T) {
	vm := moe.NewRuntime(moe.Options{})
	require.NewRegistry().Enable(vm)
	Enable(vm)
	for _, s := range []string{
		`new URL("/a?x=1","https://example.com").hostname==="example.com"`,
		`new URL("/a?x=1","https://example.com").searchParams.get("x")==="1"`,
		`(function(){const p=new URLSearchParams();p.set("chat_id","-100 123");p.set("text","通知 & test");return p.toString()==="chat_id=-100+123&text=%E9%80%9A%E7%9F%A5+%26+test"})()`,
		`(function(){const u=new URL("https://user:pass@example.com:443/a?x=1#h");const p=u.searchParams;p.append("x","2");if(u.href!=="https://user:pass@example.com/a?x=1&x=2#h")return false;u.search="?a=3";return p===u.searchParams&&p.get("a")==="3"})()`,
		`(function(){const p=new URLSearchParams([["b","2"],["a","1"],["a","3"]]);p.sort();return p.getAll("a").join()==="1,3"&&Array.from(p.keys()).join()==="a,a,b"})()`,
		`require("url").domainToASCII("例え.テスト")==="xn--r8jz45g.xn--zckzah"`,
		`new URL("https://example.com").pathname==="/"`,
		`!URL.canParse("relative")`,
		`new URLSearchParams("x=%E9%80%9A%E7%9F%A5%FF&y=%ZZ").get("x")==="通知\uFFFD"`,
		`new URLSearchParams([["x","\uD800"]]).toString()==="x=%EF%BF%BD"`,
		`String(new URL("https://example.com"))==="https://example.com/"`,
		`Object.isFrozen(Object.prototype)&&Object.isFrozen(Uint8Array.prototype)`,
		`(function(){const u=new URL("https://a.test:81/x");u.hostname="b.test";u.port="443";u.protocol="http:";u.pathname="a?b#c";u.search="?q=#";u.hash="x y";return u.href==="http://b.test/a%3Fb%23c?q=%23#x%20y"})()`,
		`(function(){const u=new URL("https://a.test/");u.username="a b";u.password="x@y";u.pathname="ok";return u.username==="a%20b"&&u.password==="x%40y"&&u.href==="https://a%20b:x%40y@a.test/ok"})()`,
		`(function(){const u=new URL("https://a.test/");const p=u.searchParams;u.href="https://b.test/?a=1";p.append("b","2");return p===u.searchParams&&u.href==="https://b.test/?a=1&b=2"})()`,
		`(function(){const u=new URL("https://a.test/");u.port="99999";u.protocol="file:";u.hostname="bad:host";return u.href==="https://a.test/"&&!URL.canParse("http://a:99999")&&!URL.canParse("http:")})()`,
		`new URL("wss://a.test:443/").port===""`,
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
