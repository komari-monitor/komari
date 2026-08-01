package jsruntime

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeCoreModulesAndECMAScriptBuiltins(t *testing.T) {
	baseDir := t.TempDir()
	runtime, err := New(`
		async function verify() {
			const fs = require("node:fs");
			const path = require("path");
			const os = require("os");
			const EventEmitter = require("events");
			fs.mkdirSync("data", { recursive: true });
			fs.writeFileSync(path.join("data", "sync.txt"), "sync", "utf8");
			await fs.promises.writeFile("data/async.txt", Buffer.from("async"));
			const asyncText = await fs.promises.readFile("data/async.txt", "utf8");
			const callbackText = await new Promise((resolve, reject) => fs.readFile("data/sync.txt", "utf8", (error, value) => error ? reject(error) : resolve(value)));
			const entries = fs.readdirSync("data", { withFileTypes: true });
			const stat = fs.statSync("data/sync.txt");
			const emitter = new EventEmitter();
			let eventValue = 0;
			emitter.once("value", (value) => eventValue = value);
			emitter.emit("value", 7);
			const mapped = [1, 2, 3, 4].map((value) => value * 2).filter((value) => value > 4).reduce((sum, value) => sum + value, 0);
			return asyncText === "async" && callbackText === "sync" && stat.isFile() && stat.size === 4 &&
				entries.some((entry) => entry.name === "sync.txt" && entry.isFile()) &&
				path.basename(path.join("a", "b.txt")) === "b.txt" && typeof os.platform() === "string" &&
				process === require("process") && process.cwd() === __dirname && typeof process.nextTick === "function" &&
				eventValue === 7 && mapped === 14 && JSON.parse(JSON.stringify({ ok: true })).ok && eval("2 + 3") === 5;
		}
	`, Options{NodeJS: true, BaseDir: baseDir, Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("Node core modules failed: %v", err)
	}
}

func TestNodeFileAccessConfinementAndAllowAll(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "base")
	if err := os.Mkdir(baseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.js"), []byte(`module.exports = "outside module";`), 0o600); err != nil {
		t.Fatal(err)
	}

	confined, err := New(`
		function verify() {
			let fsDenied = false;
			let requireDenied = false;
			try { require("fs").readFileSync("../outside.txt", "utf8"); }
			catch (error) { fsDenied = String(error).includes("escapes BaseDir"); }
			try { require("../outside.js"); }
			catch (error) { requireDenied = String(error).includes("escapes BaseDir"); }
			return fsDenied && requireDenied;
		}
	`, Options{NodeJS: true, BaseDir: baseDir, Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := confined.Call("verify"); err != nil {
		confined.Close()
		t.Fatalf("confined fs access failed: %v", err)
	}
	confined.Close()

	unrestricted, err := New(`
		function verify() {
			return require("fs").readFileSync("../outside.txt", "utf8") === "outside" &&
				require("../outside.js") === "outside module";
		}
	`, Options{NodeJS: true, BaseDir: baseDir, AllowAllFileAccess: true, Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer unrestricted.Close()
	if err := unrestricted.Call("verify"); err != nil {
		t.Fatalf("AllowAllFileAccess failed: %v", err)
	}
}

func TestStorageDirIsConfinedAdditionalRoot(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "base")
	storageDir := filepath.Join(root, "plugin-data")
	otherStorage := filepath.Join(root, "plugin-data-other")
	for _, dir := range []string{baseDir, storageDir, otherStorage} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	runtime, err := New(`
		const fs = require("fs");
		const path = require("path");
		fs.writeFileSync(path.join(__storageDir__, "saved.txt"), "hello", "utf8");
		async function verify() {
			if (await fs.promises.readFile(path.join(__storageDir__, "saved.txt"), "utf8") !== "hello") {
				return "storage data not readable";
			}
			if (typeof __storageDir__ !== "string" || __storageDir__.length === 0) {
				return "__storageDir__ missing";
			}
			let wroteBase = false;
			try { fs.writeFileSync("in-base.txt", "x"); wroteBase = true; } catch (error) {}
			if (!wroteBase) {
				return "BaseDir no longer writable";
			}
			let baseReachedStorage = false;
			try { fs.readFileSync(path.join("..", path.basename(__storageDir__), "saved.txt"), "utf8"); }
			catch (error) { baseReachedStorage = true; }
			if (!baseReachedStorage) {
				return "BaseDir reached the storage dir";
			}
			let escapedStorage = false;
			try { fs.writeFileSync(path.join(__storageDir__, "..", "escaped.txt"), "x"); }
			catch (error) { escapedStorage = true; }
			if (!escapedStorage) {
				return "escaped the storage dir";
			}
			let reachedOtherStorage = false;
			try { fs.writeFileSync(path.join(__storageDir__, "..", "plugin-data-other", "x.txt"), "x"); }
			catch (error) { reachedOtherStorage = true; }
			if (!reachedOtherStorage) {
				return "reached another plugin storage dir";
			}
			return true;
		}
	`, Options{NodeJS: true, BaseDir: baseDir, StorageDir: storageDir, Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("storage dir confinement failed: %v", err)
	}
}

func TestNodeRequireUsesCurrentDirectoryAsImplicitBaseDir(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "base")
	if err := os.Mkdir(baseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.js"), []byte(`module.exports = true;`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(baseDir)

	runtime, err := New(`
		function verify() {
			try { require("../outside.js"); return false; }
			catch (error) { return String(error).includes("escapes BaseDir"); }
		}
	`, Options{NodeJS: true, Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("implicit Node.js BaseDir confinement failed: %v", err)
	}
}

func TestProcessExitZeroIsNormalTermination(t *testing.T) {
	baseDir := t.TempDir()
	newRuntime := func(script string) *Runtime {
		t.Helper()
		runtime, err := New(script, Options{NodeJS: true, BaseDir: baseDir, Console: io.Discard, Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(runtime.Close)
		return runtime
	}

	if err := newRuntime(`
		function sendMessage() { process.exit(0); }
	`).Call("sendMessage"); err != nil {
		t.Fatalf("process.exit(0) should be a normal termination: %v", err)
	}
	if err := newRuntime(`
		async function sendMessage() { process.exit(0); }
	`).Call("sendMessage"); err != nil {
		t.Fatalf("async process.exit(0) should be a normal termination: %v", err)
	}

	failure, err := New(`
		function sendMessage() { process.exit(3); }
	`, Options{NodeJS: true, BaseDir: baseDir, Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer failure.Close()
	if err := failure.Call("sendMessage"); err == nil || !strings.Contains(err.Error(), "exited with code 3") {
		t.Fatalf("process.exit(3) should fail with the exit code, got: %v", err)
	}
}

func TestAsyncProcessExitZeroIsNotReportedAsFailure(t *testing.T) {
	var output bytes.Buffer
	runtime, err := New(`
		function sendMessage() {
			setTimeout(() => process.exit(0), 0);
			return true;
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: &output, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("sendMessage"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if strings.Contains(output.String(), "callback failed") {
		t.Fatalf("process.exit(0) in a timer was reported as a failure: %s", output.String())
	}
}

func TestHTTPClientRequestErrorWithoutListenerStillCloses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	var output bytes.Buffer
	runtime, err := New(`
		function sendMessage(url) {
			return new Promise((resolve) => {
				const request = require("http").get(url);
				request.on("close", () => resolve(true));
			});
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: &output, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("sendMessage", url); err != nil {
		t.Fatalf("request close did not fire after connection failure: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if !strings.Contains(output.String(), "setTimeout callback failed") {
		t.Fatalf("unhandled request error was not surfaced: %s", output.String())
	}
}

func TestNodeFSSymlinkOperationsDoNotFollowFinalLink(t *testing.T) {
	baseDir := t.TempDir()
	target := filepath.Join(baseDir, "target.txt")
	link := filepath.Join(baseDir, "link.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	targetDir := filepath.Join(baseDir, "target-dir")
	linkDir := filepath.Join(baseDir, "link-dir")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}

	runtime, err := New(`
		async function verify() {
			const fs = require("fs");
			const stat = fs.lstatSync("link.txt");
			const destination = fs.readlinkSync("link.txt");
			await fs.promises.unlink("link.txt");
			await fs.promises.rm("link-dir", { recursive: true });
			return stat.isSymbolicLink() && destination.length > 0 && !fs.existsSync("link.txt") && !fs.existsSync("link-dir") &&
				fs.readFileSync("target.txt", "utf8") === "keep" && fs.readFileSync("target-dir/keep.txt", "utf8") === "keep";
		}
	`, Options{NodeJS: true, BaseDir: baseDir, Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("symlink operation followed final link: %v", err)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "keep" {
		t.Fatalf("symlink target changed: content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(targetDir, "keep.txt")); err != nil || string(content) != "keep" {
		t.Fatalf("directory symlink target changed: content=%q err=%v", content, err)
	}
}

func TestNodeFSClosedRootNeverFallsBackToHostPath(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "base")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(baseDir, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}

	jsRuntime, err := New(`function verify() { return true; }`, Options{
		NodeJS: true, BaseDir: baseDir, Console: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()

	resolved, err := jsRuntime.resolveNodePath(filepath.Join("parent", "escaped.txt"), true)
	if err != nil {
		t.Fatal(err)
	}
	jsRuntime.fsModule.Close()
	if err := os.Rename(filepath.Join(baseDir, "parent"), filepath.Join(baseDir, "original-parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(baseDir, "parent")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if err := jsRuntime.nodeWriteFile(resolved, []byte("escape"), 0o600); err == nil {
		t.Fatal("closed filesystem accepted a path operation")
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("closed filesystem fell back outside BaseDir: %v", err)
	}
}

func TestNodeFSAccessModeIsValidated(t *testing.T) {
	jsRuntime, err := New(`
		async function verify() {
			const fs = require("fs");
			fs.writeFileSync("access.txt", "ok");
			let syncInvalid = false;
			try { fs.accessSync("access.txt", 8); }
			catch (error) { syncInvalid = error.code === "EINVAL"; }
			const asyncInvalid = await new Promise((resolve) => fs.access("access.txt", 8, (error) => resolve(error && error.code === "EINVAL")));
			const exists = await new Promise((resolve) => fs.access("access.txt", fs.constants.F_OK, (error) => resolve(!error)));
			return syncInvalid && asyncInvalid && exists;
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	if err := jsRuntime.Call("verify"); err != nil {
		t.Fatalf("fs.access mode validation failed: %v", err)
	}
}

func TestNodeFSCallbackAndPromiseShapes(t *testing.T) {
	runtime, err := New(`
		async function verify() {
			const fs = require("fs");
			const handle = await fs.promises.open("shape.txt", "w+");
			if (typeof handle.fd !== "number" || typeof handle.close !== "function") return false;
			const written = await handle.write(Buffer.from("shape"), 0, 5, 0);
			const target = Buffer.alloc(5);
			const read = await handle.read(target, 0, 5, 0);
			const callbackShape = await new Promise((resolve, reject) => {
				const second = Buffer.alloc(5);
				fs.read(handle.fd, second, 0, 5, 0, (error, bytesRead, buffer) => {
					if (error) reject(error); else resolve(bytesRead === 5 && buffer === second && buffer.toString() === "shape");
				});
			});
			await handle.close();
			return written.bytesWritten === 5 && written.buffer.toString() === "shape" &&
				read.bytesRead === 5 && read.buffer === target && target.toString() === "shape" && callbackShape;
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("fs callback or Promise shape failed: %v", err)
	}
}

func TestNodeFSAsyncIOLeavesEventLoopResponsive(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	runtime, err := New(`
		function verify(fd) {
			const fs = require("fs");
			fs.read(fd, Buffer.alloc(1), 0, 1, null, () => {});
			return new Promise((resolve) => setTimeout(() => resolve(true), 20));
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 500 * time.Millisecond})
	if err != nil {
		reader.Close()
		t.Fatal(err)
	}
	defer runtime.Close()

	resourceID := runtime.addNodeResource(func() { _ = reader.Close() })
	if resourceID == 0 {
		t.Fatal("failed to register pipe")
	}
	runtime.fileMu.Lock()
	runtime.fileID++
	fd := runtime.fileID
	runtime.files[fd] = nodeFileHandle{file: reader, resourceID: resourceID}
	runtime.fileMu.Unlock()

	if err := runtime.Call("verify", fd); err != nil {
		t.Fatalf("fs.read blocked the event loop: %v", err)
	}
	_, _ = writer.Write([]byte{1})
}

func TestNodeResourceRegistrationAfterCloseClosesImmediately(t *testing.T) {
	runtime, err := New(`function verify() { return true; }`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	var closed atomic.Bool
	if id := runtime.addNodeResource(func() { closed.Store(true) }); id != 0 {
		t.Fatalf("closed Runtime accepted resource %d", id)
	}
	if !closed.Load() {
		t.Fatal("late resource was not closed")
	}
}

func TestNodeRootRejectsParentSymlinkSwap(t *testing.T) {
	baseDir := t.TempDir()
	parent := filepath.Join(baseDir, "parent")
	outside := t.TempDir()
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	jsRuntime, err := New(`function verify() { return true; }`, Options{NodeJS: true, BaseDir: baseDir, Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	resolved, err := jsRuntime.resolveNodePath(filepath.Join("parent", "escaped.txt"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, filepath.Join(baseDir, "original-parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := jsRuntime.nodeWriteFile(resolved, []byte("escape"), 0o600); err == nil {
		t.Fatal("rooted write followed a swapped parent symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("write escaped BaseDir: %v", err)
	}
}

func TestNodeFSErrorsExposeNodeFields(t *testing.T) {
	jsRuntime, err := New(`
		async function verify() {
			try {
				await require("fs").promises.readFile("missing.txt");
				return false;
			} catch (error) {
				return error.name === "Error" && error.code === "ENOENT" &&
					typeof error.errno === "number" && error.errno < 0 && typeof error.syscall === "string" &&
					typeof error.path === "string";
			}
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	if err := jsRuntime.Call("verify"); err != nil {
		t.Fatalf("fs error shape is not Node-compatible: %v", err)
	}
}

func TestNodePathCrossPlatformSemantics(t *testing.T) {
	runtime, err := New(`
		function verify() {
			const path = require("path");
			return path.extname("a.") === "." && path.extname("a..") === "." && path.extname(".hidden") === "" &&
				path.posix.basename("/") === "" && path.posix.parse("/").base === "" && path.posix.parse("foo").dir === "" &&
				path.posix.normalize("a\\b") === "a\\b" && path.posix.normalize("foo/bar//") === "foo/bar/" &&
				path.posix.join("foo", "bar/") === "foo/bar/" &&
				path.posix.relative("/", "/a") === "a" && path.posix.relative("/a", "/") === ".." &&
				path.posix.resolve("a").endsWith("/a") &&
				path.win32.normalize("C:/foo/../bar/a.") === "C:\\bar\\a." &&
				path.win32.basename("C:foo") === "foo" && path.win32.parse("C:foo").base === "foo" &&
				path.win32.parse("\\\\server\\share\\").root === "\\\\server\\share\\" &&
				path.win32.resolve("C:\\base", "C:foo") === "C:\\base\\foo" &&
				path.win32.resolve("C:\\base", "\\foo") === "C:\\foo" &&
				path.win32.join("C:\\a", "b") === "C:\\a\\b" && path.win32.isAbsolute("C:\\a") &&
				path.win32.resolve("C:\\a") === "C:\\a" &&
				path.win32.relative("C:\\a\\b", "C:\\a\\c") === "..\\c" &&
				path.win32.format({ dir: "C:\\a", name: "b", ext: "txt" }) === "C:\\a\\b.txt" &&
				path.win32.toNamespacedPath("C:\\a") === "\\\\?\\C:\\a";
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("path compatibility failed: %v", err)
	}
}

func TestProcessNextTickPrecedesPromiseMicrotasks(t *testing.T) {
	jsRuntime, err := New(`
		function verify() {
			const order = [];
			Promise.resolve().then(() => {
				order.push("promise-1");
				process.nextTick(() => order.push("tick-2"));
			});
			Promise.resolve().then(() => order.push("promise-2"));
			process.nextTick(() => order.push("tick-1"));
			return new Promise((resolve) => setImmediate(() => resolve(order.join(",") === "tick-1,promise-1,promise-2,tick-2")));
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	if err := jsRuntime.Call("verify"); err != nil {
		t.Fatalf("process.nextTick ordering failed: %v", err)
	}
}

func TestProcessNextTickPrecedesPromisesInAsyncHostCallback(t *testing.T) {
	jsRuntime, err := New(`
		function verify() {
			return new Promise((resolve) => {
				require("fs").exists("missing.txt", () => {
					const order = [];
					Promise.resolve().then(() => order.push("promise"));
					process.nextTick(() => order.push("tick"));
					setImmediate(() => resolve(order.join(",") === "tick,promise"));
				});
			});
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	if err := jsRuntime.Call("verify"); err != nil {
		t.Fatalf("process.nextTick ordering in an async host callback failed: %v", err)
	}
}

func TestFetchResponseBodyLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("response-too-large"))
	}))
	defer server.Close()

	jsRuntime, err := New(`
		async function verify(url) {
			try {
				await fetch(url);
				return false;
			} catch (error) {
				return error.name === "TypeError" && String(error).includes("HTTP body exceeds 8 bytes");
			}
		}
	`, Options{Console: io.Discard, Timeout: time.Second, MaxHTTPBodyBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	if err := jsRuntime.Call("verify", server.URL); err != nil {
		t.Fatalf("fetch response limit failed: %v", err)
	}
}

func TestNodeOSAndProcessMetrics(t *testing.T) {
	runtime, err := New(`
		function verify() {
			const os = require("os");
			const memory = process.memoryUsage();
			const cpu = process.cpuUsage();
			const resources = process.resourceUsage();
			return os.totalmem() > 0 && os.freemem() > 0 && os.uptime() > 0 && os.cpus().length > 0 &&
				typeof os.release() === "string" && os.release().length > 0 &&
				typeof process.memoryUsage.rss === "function" && process.memoryUsage.rss() > 0 && memory.rss > 0 &&
				cpu.user >= 0 && cpu.system >= 0 && resources.userCPUTime >= 0 && resources.systemCPUTime >= 0 && resources.maxRSS > 0;
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("os/process metrics failed: %v", err)
	}
}

func TestChildProcessPermissionAndExecution(t *testing.T) {
	denied, err := New(`
		function verify() {
			try { require("child_process"); return false; }
			catch (error) { return String(error).includes("AllowExec"); }
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := denied.Call("verify"); err != nil {
		denied.Close()
		t.Fatalf("child_process permission failed: %v", err)
	}
	denied.Close()

	command := "printf child"
	if runtime.GOOS == "windows" {
		command = "echo child"
	}
	allowed, err := New(`
		function verify(command) {
			const childProcess = require("child_process");
			const syncOutput = childProcess.execSync(command, { encoding: "utf8" }).trim();
			return new Promise((resolve) => {
				const child = childProcess.spawn(command, [], { shell: true, encoding: "utf8" });
				let output = "";
				child.stdout.on("data", (chunk) => output += String(chunk));
				child.on("close", (code) => resolve(code === 0 && syncOutput === "child" && output.trim() === "child"));
				child.on("error", () => resolve(false));
			});
		}
	`, Options{NodeJS: true, AllowExec: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer allowed.Close()
	if err := allowed.Call("verify", command); err != nil {
		t.Fatalf("child_process execution failed: %v", err)
	}
}

func TestChildProcessOutputLimitAndExitCode(t *testing.T) {
	jsRuntime, err := New(`
		function execFile(command, environment, maxBuffer) {
			return new Promise((resolve) => {
				require("child_process").execFile(
					command,
					["-test.run=^TestNodeChildProcessHelper$"],
					{ env: environment, encoding: "utf8", maxBuffer },
					(error, stdout) => resolve({ error, stdout })
				);
			});
		}
		async function verify(command, outputEnvironment, exitEnvironment) {
			const limited = await execFile(command, outputEnvironment, 4096);
			const commandLimited = await execFile(command, outputEnvironment, 64);
			const exited = await execFile(command, exitEnvironment, 4096);
			const valid = limited.error && limited.error.code === "ERR_CHILD_PROCESS_STDIO_MAXBUFFER" &&
				String(limited.error).includes("stdout maxBuffer length exceeded") && limited.stdout.length === 128 &&
				commandLimited.error && commandLimited.error.code === "ERR_CHILD_PROCESS_STDIO_MAXBUFFER" && commandLimited.stdout.length === 64 &&
				exited.error && exited.error.code === 7;
			if (!valid) {
				throw new Error(JSON.stringify({
					limitedError: String(limited.error), limitedCode: limited.error && limited.error.code,
					limitedLength: limited.stdout && limited.stdout.length,
					commandLimitedError: String(commandLimited.error), commandLimitedLength: commandLimited.stdout && commandLimited.stdout.length,
					exitedError: String(exited.error), exitedCode: exited.error && exited.error.code
				}));
			}
			return true;
		}
	`, Options{
		NodeJS: true, AllowExec: true, BaseDir: t.TempDir(), Console: io.Discard,
		Timeout: 3 * time.Second, MaxChildOutputBytes: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	if jsRuntime.maxChildOutputBytes != 128 {
		t.Fatalf("MaxChildOutputBytes = %d, want 128", jsRuntime.maxChildOutputBytes)
	}
	if err := jsRuntime.Call(
		"verify",
		os.Args[0],
		childHelperEnvironment("output"),
		childHelperEnvironment("exit"),
	); err != nil {
		t.Fatalf("child_process output limit or exit code failed: %v", err)
	}
}

func TestChildProcessSyncTimeoutLeavesRuntimeUsable(t *testing.T) {
	jsRuntime, err := New(`
		function verify(command, environment) {
			try {
				require("child_process").execFileSync(
					command,
					["-test.run=^TestNodeChildProcessHelper$"],
					{ env: environment, timeout: 40 }
				);
				return false;
			} catch (_) {
				return true;
			}
		}
		function healthy() { return eval("6 * 7") === 42; }
	`, Options{NodeJS: true, AllowExec: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	if err := jsRuntime.Call("verify", os.Args[0], childHelperEnvironment("sleep")); err != nil {
		t.Fatalf("child_process sync timeout failed: %v", err)
	}
	if err := jsRuntime.Call("healthy"); err != nil {
		t.Fatalf("runtime was unusable after child_process sync timeout: %v", err)
	}
}

func TestRuntimeCloseClearsNodeFileDescriptors(t *testing.T) {
	jsRuntime, err := New(`
		function verify() {
			require("fs").openSync("held.txt", "w");
			return true;
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := jsRuntime.Call("verify"); err != nil {
		jsRuntime.Close()
		t.Fatal(err)
	}
	jsRuntime.fileMu.Lock()
	openBeforeClose := len(jsRuntime.files)
	jsRuntime.fileMu.Unlock()
	if openBeforeClose != 1 {
		jsRuntime.Close()
		t.Fatalf("expected one open descriptor, got %d", openBeforeClose)
	}
	jsRuntime.Close()
	jsRuntime.fileMu.Lock()
	openAfterClose := len(jsRuntime.files)
	jsRuntime.fileMu.Unlock()
	if openAfterClose != 0 {
		t.Fatalf("Runtime.Close left %d descriptors registered", openAfterClose)
	}
}

func TestNodeNetAndHTTPServers(t *testing.T) {
	runtime, err := New(`
		function verifyNet() {
			const net = require("net");
			return new Promise((resolve) => {
				const server = net.createServer((socket) => socket.on("data", (data) => socket.end(data)));
				server.listen(0, "127.0.0.1", () => {
					const client = net.connect(server.address().port, "127.0.0.1");
					let output = "";
					client.setEncoding("utf8");
					client.on("connect", () => client.write("echo"));
					client.on("data", (data) => output += data);
					client.on("end", () => server.close(() => resolve(output === "echo")));
					client.on("error", () => resolve(false));
				});
			});
		}
		function verifyHTTP() {
			const http = require("http");
			return new Promise((resolve) => {
				const server = http.createServer((request, response) => {
					response.statusCode = 201;
					response.setHeader("X-Node", "yes");
					response.end("ready");
				});
				server.listen(0, "127.0.0.1", async () => {
					const response = await fetch("http://127.0.0.1:" + server.address().port + "/test");
					const text = await response.text();
					server.close(() => resolve(response.status === 201 && response.headers.get("x-node") === "yes" && text === "ready"));
				});
			});
		}
	`, Options{NodeJS: true, AllowListen: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verifyNet"); err != nil {
		t.Fatalf("Node net server failed: %v", err)
	}
	if err := runtime.Call("verifyHTTP"); err != nil {
		t.Fatalf("Node HTTP server failed: %v", err)
	}
}

func TestNodeCloseCancelsPendingListen(t *testing.T) {
	jsRuntime, err := New(`
		function verify(moduleName) {
			const api = require(moduleName);
			return new Promise((resolve) => {
				let listened = false;
				const servers = [];
				for (let index = 0; index < 32; index++) {
					const server = api.createServer();
					servers.push(server);
					server.on("listening", () => { listened = true; });
					server.listen(0, "127.0.0.1");
					let duplicateRejected = false;
					try { server.listen(0, "127.0.0.1"); }
					catch (_) { duplicateRejected = true; }
					if (!duplicateRejected) return resolve(false);
					server.close();
				}
				setTimeout(() => resolve(!listened && servers.every((server) => server.address() === null)), 100);
			});
		}
		function verifyReuse(moduleName) {
			const api = require(moduleName);
			return new Promise((resolve) => {
				const server = api.createServer();
				let staleCallbackCalls = 0;
				server.listen(0, "127.0.0.1", () => { staleCallbackCalls++; });
				server.close();
				setTimeout(() => {
					server.listen(0, "127.0.0.1", () => {
						server.close(() => resolve(staleCallbackCalls === 0));
					});
				}, 20);
			});
		}
	`, Options{NodeJS: true, AllowListen: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer jsRuntime.Close()
	for _, moduleName := range []string{"net", "http"} {
		if err := jsRuntime.Call("verify", moduleName); err != nil {
			t.Fatalf("%s pending listen was not cancelled: %v", moduleName, err)
		}
		if err := jsRuntime.Call("verifyReuse", moduleName); err != nil {
			t.Fatalf("%s server retained a cancelled listen callback: %v", moduleName, err)
		}
	}
}

func TestNodeListenPermission(t *testing.T) {
	runtime, err := New(`
		function verify() {
			try { require("net").createServer().listen(0); return false; }
			catch (error) { return String(error).includes("AllowListen"); }
		}
	`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("listen permission failed: %v", err)
	}
}

func TestEvalRemainsInterruptible(t *testing.T) {
	runtime, err := New(`function verify() { eval("for (;;) {}"); return true; }`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected eval timeout, got %v", err)
	}
}

const childHelperModeEnvironment = "KOMARI_JSRUNTIME_CHILD_HELPER_MODE"

func childHelperEnvironment(mode string) map[string]string {
	environment := make(map[string]string)
	for _, item := range os.Environ() {
		name, value, found := strings.Cut(item, "=")
		if found && name != "" {
			environment[name] = value
		}
	}
	environment[childHelperModeEnvironment] = mode
	return environment
}

func TestNodeChildProcessHelper(t *testing.T) {
	switch os.Getenv(childHelperModeEnvironment) {
	case "":
		return
	case "output":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 4096))
		os.Exit(0)
	case "exit":
		os.Exit(7)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "echo":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
