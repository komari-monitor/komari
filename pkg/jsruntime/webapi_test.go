package jsruntime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchAPIClassesAndBodyMethods(t *testing.T) {
	runtime, err := New(`
		async function verify() {
			const headers = new Headers([["X-Test", " one "]]);
			headers.append("x-test", "two");
			headers.set("X-Other", "value");
			const visited = [];
			headers.forEach((value, name, owner) => visited.push(owner === headers ? name + "=" + value : "bad"));
			const request = new Request("https://example.test/", { method: "post", headers, body: "hello" });
			const requestClone = request.clone();
			const requestText = await request.text();
			const response = Response.json({ accepted: true }, { status: 201 });
			const responseClone = response.clone();
			const parsed = await response.json();
			const bytes = await responseClone.bytes();
			const blob = new Blob(["ab", new Uint8Array([99])], { type: "TEXT/PLAIN" });
			const file = new File([blob], "test.txt", { type: blob.type, lastModified: 10 });
			const form = new FormData();
			form.append("value", "first");
			form.append("value", "second");
			form.set("other", file);
			const exposedInternals = Object.getOwnPropertyNames(globalThis).filter((name) => name.startsWith("__komari"));
			const instanceInternals = [headers, request, response, blob, form, new XMLHttpRequest()]
				.flatMap((value) => Object.getOwnPropertyNames(value).filter((name) => name.startsWith("_")));
			return typeof EventTarget === "function" &&
				typeof AbortController === "function" &&
				headers.get("X-Test") === "one, two" &&
				headers.has("x-other") &&
				Array.from(headers.keys()).join(",") === "x-other,x-test" &&
				visited.join(";") === "x-other=value;x-test=one, two" &&
				request.method === "POST" && request.headers.get("content-type") === "text/plain;charset=UTF-8" &&
				requestText === "hello" && request.bodyUsed && !requestClone.bodyUsed &&
				parsed.accepted && response.status === 201 && response.ok && response.bodyUsed &&
				bytes.length > 0 && blob.size === 3 && await blob.text() === "abc" &&
				file.name === "test.txt" && file.lastModified === 10 && exposedInternals.length === 0 && instanceInternals.length === 0 &&
				form.getAll("value").join(",") === "first,second" && form.get("other") === file;
		}
	`, Options{Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("Fetch API classes failed: %v", err)
	}
}

func TestFetchRequestResponseAndRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/echo":
			body, _ := io.ReadAll(request.Body)
			if request.Method != http.MethodPost || string(body) != "payload" || request.Header.Get("X-Test") != "yes" {
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			if request.Header.Get("Content-Type") != "text/plain;charset=UTF-8" {
				http.Error(response, "bad content type", http.StatusBadRequest)
				return
			}
			response.Header().Add("X-Multi", "one")
			response.Header().Add("X-Multi", "two")
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"accepted":true}`)
		case "/redirect":
			http.Redirect(response, request, "/final", http.StatusFound)
		case "/final":
			_, _ = io.WriteString(response, "final")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runtime, err := New(`
		async function verify(baseURL) {
			const request = new Request(baseURL + "/echo", { method: "POST", headers: { "X-Test": "yes" }, body: "payload" });
			const response = await fetch(request);
			const clone = response.clone();
			const body = await response.json();
			const cloneText = await clone.text();
			const redirected = await fetch(baseURL + "/redirect");
			return request.bodyUsed && response.status === 201 && response.statusText === "Created" &&
				response.type === "basic" && response.url.endsWith("/echo") &&
				response.headers instanceof Headers && response.headers.get("x-multi") === "one, two" &&
				body.accepted === true && JSON.parse(cloneText).accepted === true &&
				redirected.redirected && redirected.url.endsWith("/final") && await redirected.text() === "final";
		}
	`, Options{Console: io.Discard, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("verify", server.URL); err != nil {
		t.Fatalf("Fetch request/response failed: %v", err)
	}
}

func TestFetchFormDataAndResponseFormData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		file, header, err := request.FormFile("upload")
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		content, _ := io.ReadAll(file)
		if request.FormValue("name") != "komari" || header.Filename != "note.txt" || string(content) != "file body" {
			http.Error(response, "invalid multipart data", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = io.WriteString(response, "accepted=yes&accepted=again")
	}))
	defer server.Close()

	runtime, err := New(`
		async function verify(url) {
			const data = new FormData();
			data.append("name", "komari");
			data.append("upload", new Blob(["file body"], { type: "text/plain" }), "note.txt");
			const response = await fetch(url, { method: "POST", body: data });
			const result = await response.formData();
			return response.status === 200 && result.getAll("accepted").sort().join(",") === "again,yes";
		}
	`, Options{Console: io.Discard, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("verify", server.URL); err != nil {
		t.Fatalf("Fetch FormData failed: %v", err)
	}
}

func TestAbortControllerCancelsFetch(t *testing.T) {
	requestCanceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
			requestCanceled <- struct{}{}
		case <-time.After(time.Second):
			_, _ = io.WriteString(response, "late")
		}
	}))
	defer server.Close()

	runtime, err := New(`
		async function verify(url) {
			const controller = new AbortController();
			const pending = fetch(url, { signal: controller.signal });
			setTimeout(() => controller.abort(), 10);
			try { await pending; return false; }
			catch (error) { return controller.signal.aborted && error === controller.signal.reason && error.name === "AbortError"; }
		}
	`, Options{Console: io.Discard, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("verify", server.URL); err != nil {
		t.Fatalf("Fetch abort failed: %v", err)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("HTTP request context was not canceled")
	}
}

func TestXMLHttpRequestStatesEventsAndResponseTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("X-Test", "xhr")
		_, _ = io.WriteString(response, `{"accepted":true}`)
	}))
	defer server.Close()

	runtime, err := New(`
		function verify(url) {
			return new Promise((resolve) => {
				const xhr = new XMLHttpRequest();
				const events = [];
				xhr.addEventListener("readystatechange", () => events.push("rs" + xhr.readyState));
				for (const name of ["loadstart", "progress", "load", "loadend"]) xhr.addEventListener(name, () => events.push(name));
				xhr.open("GET", url);
				xhr.responseType = "json";
				xhr.onloadend = () => resolve(
					XMLHttpRequest.UNSENT === 0 && xhr.OPENED === 1 && XMLHttpRequest.DONE === 4 &&
					xhr.status === 200 && xhr.statusText === "OK" && xhr.response.accepted === true &&
					xhr.responseURL === url && xhr.responseXML === null &&
					xhr.getResponseHeader("x-test") === "xhr" && xhr.getAllResponseHeaders().endsWith("\r\n") &&
					events.join(",") === "rs1,loadstart,rs2,rs3,progress,rs4,load,loadend"
				);
				xhr.send();
			});
		}
	`, Options{Console: io.Discard, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("verify", server.URL); err != nil {
		t.Fatalf("XHR states/events failed: %v", err)
	}
}

func TestXMLHttpRequestBinaryResponseTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/octet-stream")
		_, _ = response.Write([]byte{1, 2, 3, 4})
	}))
	defer server.Close()

	runtime, err := New(`
		function load(url, type) {
			return new Promise((resolve) => {
				const xhr = new XMLHttpRequest();
				xhr.open("GET", url);
				xhr.responseType = type;
				xhr.onload = () => {
					let responseTextThrows = false;
					try { void xhr.responseText; } catch (error) { responseTextThrows = error.name === "InvalidStateError"; }
					resolve(responseTextThrows && (type === "arraybuffer" ?
						xhr.response instanceof ArrayBuffer && new Uint8Array(xhr.response).join(",") === "1,2,3,4" :
						xhr.response instanceof Blob && xhr.response.size === 4 && xhr.response.type === "application/octet-stream"));
				};
				xhr.onerror = () => resolve(false);
				xhr.send();
			});
		}
		function verify(url) {
			return Promise.all([load(url, "arraybuffer"), load(url, "blob")]).then((results) => results.every(Boolean));
		}
	`, Options{Console: io.Discard, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("verify", server.URL); err != nil {
		t.Fatalf("XHR binary response types failed: %v", err)
	}
}

func TestXMLHttpRequestTimeoutAbortAndSynchronousMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/sync" {
			response.Header().Set("X-Mode", "sync")
			_, _ = io.WriteString(response, "ready")
			return
		}
		select {
		case <-request.Context().Done():
		case <-time.After(time.Second):
			_, _ = io.WriteString(response, "late")
		}
	}))
	defer server.Close()

	runtime, err := New(`
		function verify(baseURL) {
			const sync = new XMLHttpRequest();
			sync.open("GET", baseURL + "/sync", false);
			sync.send();
			if (sync.readyState !== sync.DONE || sync.responseText !== "ready" || sync.getResponseHeader("x-mode") !== "sync") return false;
			return Promise.all([
				new Promise((resolve) => {
					const xhr = new XMLHttpRequest();
					xhr.open("GET", baseURL + "/slow");
					xhr.timeout = 20;
					xhr.ontimeout = () => resolve(xhr.readyState === xhr.DONE && xhr.status === 0);
					xhr.onerror = () => resolve(false);
					xhr.send();
				}),
				new Promise((resolve) => {
					const xhr = new XMLHttpRequest();
					xhr.open("GET", baseURL + "/slow");
					xhr.onabort = () => resolve(xhr.readyState === xhr.UNSENT && xhr.status === 0);
					xhr.onerror = () => resolve(false);
					xhr.send();
					setTimeout(() => xhr.abort(), 10);
				})
			]).then((results) => results.every(Boolean));
		}
	`, Options{Console: io.Discard, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("verify", server.URL); err != nil {
		t.Fatalf("XHR timeout/abort/sync failed: %v", err)
	}
}
