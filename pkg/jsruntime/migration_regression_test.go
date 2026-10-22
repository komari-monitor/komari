package jsruntime

import (
	"io"
	"testing"
	"time"
)

// Migration regressions exercise engine/host boundaries rather than language-only
// microbenchmarks. Keep this suite identical in the goja comparison worktree.
func TestRuntimeMigrationBoundaries(t *testing.T) {
	tests := []struct{ name, script string }{
		{"hostThrowCatch", `function run() { try { require("fs").readFileSync("missing-file"); } catch(e) { return e instanceof Error && typeof e.message === "string"; } return false; }`},
		{"bufferLiveWrite", `function run() { const b=Buffer.alloc(8); const view=b.subarray(2,6); require("crypto").randomFillSync(view); return b[0]===0 && b[1]===0 && b[6]===0 && b[7]===0 && view.some(x=>x!==0); }`},
		{"floatRandomRejected", `function run() { try { crypto.getRandomValues(new Float32Array(4)); } catch(e) { return true; } return false; }`},
		{"constructorAndURL", `function run() { const u=new URL("/a?x=1", "https://example.com"); return u.hostname==="example.com" && u.searchParams.get("x")==="1"; }`},
		{"promiseRejectCatch", `async function run() { try { await Promise.reject(new Error("expected")); } catch(e) { return e.message==="expected"; } return false; }`},
		{"commonJSIdentity", `function run() { return require("buffer")===require("node:buffer") && Buffer===require("buffer").Buffer; }`},
	}
	for _, tc := range tests {
		b := tc
		t.Run(b.name, func(t *testing.T) {
			rt, err := New(b.script, Options{BaseDir: t.TempDir(), NodeJS: true, Console: io.Discard, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			if err := rt.Call("run"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
