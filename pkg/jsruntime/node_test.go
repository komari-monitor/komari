package jsruntime

import (
	"io"
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

func TestNodePathCrossPlatformSemantics(t *testing.T) {
	runtime, err := New(`
		function verify() {
			const path = require("path");
			return path.extname("a.") === "." && path.extname("a..") === "." && path.extname(".hidden") === "" &&
				path.posix.normalize("a\\b") === "a\\b" &&
				path.posix.resolve("a").endsWith("/a") &&
				path.win32.normalize("C:/foo/../bar/a.") === "C:\\bar\\a." &&
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
