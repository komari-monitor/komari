package fetch

import (
	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"net/http"
	"testing"
)

func TestNativeFormDataAndBody(t *testing.T) {
	vm := moejs.NewRuntime(moejs.Options{})
	m := New(nil, http.DefaultClient, 1<<20)
	if err := m.Inject(vm); err != nil {
		t.Fatal(err)
	}
	value, err := native.Run(vm, `(async function () {
 const blob = new Blob(["ab", new Uint8Array([99])], {type:"TEXT/PLAIN"});
 if (blob.size !== 3 || await blob.text() !== "abc") throw new Error("blob bytes changed");
 const form = new FormData();
 form.append("field", "one"); form.append("field", "two");
 form.append("file", new File([blob], "test.txt", {type:blob.type}));
 const request = new Request("https://example.test/", {method:"POST", body:form});
 const response = new Response(await request.arrayBuffer(), {headers:request.headers});
 const parsed = await response.formData();
 if (parsed.getAll("field").join(",") !== "one,two") throw new Error("form fields changed");
 const file = parsed.get("file");
 if (file.name !== "test.txt" || file.type !== "text/plain" || await file.text() !== "abc") throw new Error("multipart file changed");
 const urlencoded = await new Response("hello=world&hello=again", {headers:{"content-type":"application/x-www-form-urlencoded"}}).formData();
 if (urlencoded.getAll("hello").join(",") !== "world,again") throw new Error("urlencoded changed");
 return true;
 })()`)
	if err != nil {
		t.Fatal(err)
	}
	state, result, ok := moejs.PromiseResult(value)
	if !ok {
		t.Fatal("expected promise")
	}
	if state != moejs.PromiseFulfilled {
		t.Fatalf("promise state %v result %v", state, native.String(vm, result))
	}
	if !native.Bool(result) {
		t.Fatal("body/form verification failed")
	}
}
