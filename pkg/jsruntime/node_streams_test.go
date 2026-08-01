package jsruntime

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

func TestProcessStdioAreStreams(t *testing.T) {
	originalStdout, originalStderr := os.Stdout, os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	defer func() { os.Stdout, os.Stderr = originalStdout, originalStderr }()
	stdoutCapture := &bytes.Buffer{}
	stderrCapture := &bytes.Buffer{}
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() { defer close(stdoutDone); _, _ = io.Copy(stdoutCapture, stdoutReader) }()
	go func() { defer close(stderrDone); _, _ = io.Copy(stderrCapture, stderrReader) }()

	runtime := newStreamRuntime(t, `
		async function verify() {
			const stream = require("stream");
			const process = require("process");
			const results = [];
			results.push(process.stdout instanceof stream.Writable);
			results.push(process.stderr instanceof stream.Writable);
			results.push(process.stdin instanceof stream.Readable);
			results.push(process.stdout.write("hello stdout"));
			await new Promise((resolve) => process.stdout.write(" again", resolve));
			await new Promise((resolve) => process.stderr.write("stderr line", resolve));
			process.stdin.setEncoding("utf8");
			process.stdin.pause();
			process.stdin.resume();
			return results.join(",") === "true,true,true,true";
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("process stdio streams failed: %v", err)
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	<-stdoutDone
	<-stderrDone
	if stdoutCapture.String() != "hello stdout again" {
		t.Fatalf("stdout capture = %q", stdoutCapture.String())
	}
	if stderrCapture.String() != "stderr line" {
		t.Fatalf("stderr capture = %q", stderrCapture.String())
	}
}
func TestChildProcessStdioAreStreams(t *testing.T) {
	runtime, err := New(`
		async function verify(command) {
			const childProcess = require("child_process");
			const stream = require("stream");
			const child = childProcess.spawn(command, ["-test.run=^TestNodeChildProcessHelper$"], {
				env: Object.assign({}, process.env, { KOMARI_JSRUNTIME_CHILD_HELPER_MODE: "echo" }),
			});
			const checks = [];
			checks.push(child.stdin instanceof stream.Writable);
			checks.push(child.stdout instanceof stream.Readable);
			checks.push(child.stderr instanceof stream.Readable);
			child.stdin.write("hello ");
			child.stdin.end("stream");
			let output = "";
			child.stdout.setEncoding("utf8");
			child.stdout.on("data", (chunk) => output += chunk);
			child.stdout.on("error", () => {});
			await new Promise((resolve, reject) => {
				child.stdout.on("end", resolve);
				child.on("error", () => reject(new Error("spawn failed")));
			});
			const code = await new Promise((resolve) => {
				child.on("close", (exitCode) => resolve(exitCode));
				child.on("error", () => resolve(-1));
			});
			return code === 0 && output === "hello stream" &&
				checks.join(",") === "true,true,true" &&
				child.stdin.writable === true &&
				child.stdout.readable === true &&
				child.stdout.readableEnded === true;
		}
	`, Options{NodeJS: true, AllowExec: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify", os.Args[0]); err != nil {
		t.Fatalf("child_process stdio streams failed: %v", err)
	}
}
func TestFSStreams(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const fs = require("fs");
			const stream = require("stream");
			fs.writeFileSync("source.txt", "hello stream world");
			const checks = [];
			const chunks = [];
			const reader = fs.createReadStream("source.txt", { highWaterMark: 6 });
			checks.push(reader instanceof stream.Readable);
			reader.on("data", (chunk) => chunks.push(chunk.toString()));
			await new Promise((resolve, reject) => { reader.on("end", resolve); reader.on("error", reject); });
			const ranged = fs.createReadStream("source.txt", { start: 6, end: 11 });
			let range = "";
			ranged.setEncoding("utf8");
			ranged.on("data", (chunk) => range += chunk);
			await new Promise((resolve, reject) => { ranged.on("end", resolve); ranged.on("error", reject); });
			const openFd = await new Promise((resolve, reject) => {
				const probe = fs.createReadStream("source.txt");
				probe.on("open", resolve);
				probe.on("error", reject);
				probe.resume();
			});
			const writer = fs.createWriteStream("copy.txt");
			checks.push(writer instanceof stream.Writable);
			writer.write("part1 ");
			writer.write("part2");
			writer.end();
			await new Promise((resolve, reject) => { writer.on("finish", resolve); writer.on("error", reject); });
			await new Promise((resolve) => fs.createReadStream("source.txt").pipe(fs.createWriteStream("pipe.txt")).on("finish", resolve));
			const missing = new Promise((resolve) => {
				const bad = fs.createReadStream("missing.txt");
				bad.on("error", (error) => resolve(String(error.message).length > 0));
			});
			return chunks.join("") === "hello stream world" &&
				range === "stream" &&
				typeof openFd === "number" && openFd > 0 &&
				fs.readFileSync("copy.txt", "utf8") === "part1 part2" &&
				fs.readFileSync("pipe.txt", "utf8") === "hello stream world" &&
				reader.readableEnded === true && writer.writableFinished === true &&
				(await missing) && checks.join(",") === "true,true";
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("fs streams failed: %v", err)
	}
}
func TestHTTPServerRequestResponseStreams(t *testing.T) {
	runtime, err := New(`
		async function verify() {
			const http = require("http");
			const stream = require("stream");
			const checks = [];
			const server = http.createServer((request, response) => {
				checks.push(request instanceof stream.Readable);
				checks.push(response instanceof stream.Writable);
				request.setEncoding("utf8");
				let body = "";
				request.on("data", (chunk) => body += chunk);
				request.on("end", () => {
					response.statusCode = 201;
					response.setHeader("X-Stream", "yes");
					const writeResult = response.write("first ");
					checks.push(typeof writeResult === "boolean");
					checks.push(response.writableEnded === false);
					response.write("second");
					response.end("!");
				});
				response.on("finish", () => checks.push("finish:" + body));
				response.on("close", () => checks.push("close"));
			});
			return await new Promise((resolve) => {
				server.listen(0, "127.0.0.1", async () => {
					const response = await fetch("http://127.0.0.1:" + server.address().port + "/stream", {
						method: "POST",
						body: "request body",
					});
					const text = await response.text();
					server.close(() => checks.push("server-close"));
					await new Promise((resolve) => setTimeout(resolve, 50));
					resolve(response.status === 201 &&
						response.headers.get("x-stream") === "yes" &&
						text === "first second!" &&
						checks.join(",") === "true,true,true,true,finish:request body,close,server-close");
				});
			});
		}
	`, Options{NodeJS: true, AllowListen: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("http server request/response streams failed: %v", err)
	}
}
