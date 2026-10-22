package jsruntime

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestNativeLookupAndPromiseGetterDeadline(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		check        bool
	}{
		{"hasFunction", `Object.defineProperty(globalThis,"bad",{get(){while(true){}}}); function healthy(){return true}`, true},
		{"callLookup", `Object.defineProperty(globalThis,"bad",{get(){while(true){}}}); function healthy(){return true}`, false},
		{"promiseThen", `function bad(){const p=Promise.resolve(true);Object.defineProperty(p,"then",{get(){while(true){}}});return p} function healthy(){return true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := New(tc.source, Options{BaseDir: t.TempDir(), Console: io.Discard, Timeout: 50 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			done := make(chan error, 1)
			go func() {
				if tc.check {
					if rt.HasFunction("bad") {
						done <- io.ErrUnexpectedEOF
						return
					}
					done <- nil
				} else {
					done <- rt.Call("bad")
				}
			}()
			select {
			case err := <-done:
				if !tc.check && (err == nil || !strings.Contains(err.Error(), "timeout")) {
					t.Fatalf("expected timeout: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("lookup escaped deadline")
			}
			if err := rt.Call("healthy"); err != nil {
				t.Fatalf("runtime not reusable: %v", err)
			}
		})
	}
}
