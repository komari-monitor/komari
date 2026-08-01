package jsruntime

import (
	"io"
	"testing"
	"time"
)

func newStreamRuntime(t *testing.T, script string) *Runtime {
	t.Helper()
	runtime, err := New(script, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func TestStreamReadableDataEventsAndBackpressure(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Readable } = require("stream");
			const chunks = [];
			const events = [];
			const source = new Readable();
			let pushResult = true;
			source.on("data", (chunk) => {
				chunks.push(chunk.toString());
				pushResult = pushResult && source.readableFlowing === true;
			});
			source.on("end", () => events.push("end"));
			source.on("close", () => events.push("close"));
			pushResult = source.push("hello ");
			pushResult = pushResult && source.push("world");
			source.push(null);
			await new Promise((resolve) => source.once("close", resolve));
			const paused = new Readable({ highWaterMark: 2 });
			const pausedResults = [];
			pausedResults.push(paused.push("aaaa"));
			pausedResults.push(paused.push("bbbb"));
			paused.push(null);
			const pieces = [];
			let piece;
			while ((piece = paused.read(2)) !== null) pieces.push(piece.toString());
			return chunks.join("") === "hello world" &&
				events.join(",") === "end,close" &&
				source.readableEnded === true &&
				source.destroyed === true &&
				pushResult === true &&
				pausedResults.join(",") === "false,false" &&
				pieces.join(",") === "aa,aa,bb,bb" &&
				paused.readableEnded === true &&
				Readable.isDisturbed(source) === true;
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream readable flow failed: %v", err)
	}
}
func TestStreamReadablePullModeAndEncoding(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Readable } = require("stream");
			const source = ["alpha", "beta", "gamma"];
			let index = 0;
			const pulled = new Readable({
				read() {
					if (index < source.length) this.push(source[index++]);
					else this.push(null);
				},
			});
			const received = [];
			let readableEvents = 0;
			pulled.on("readable", () => {
				readableEvents++;
				let chunk;
				while ((chunk = pulled.read()) !== null) received.push(chunk.toString());
			});
			await new Promise((resolve) => pulled.once("end", resolve));
			const withEncoding = new Readable();
			withEncoding.setEncoding("utf8");
			withEncoding.push("komari");
			withEncoding.push(null);
			const encoded = withEncoding.read();
			const reordered = new Readable();
			reordered.push("world");
			reordered.unshift("hello ");
			reordered.push(null);
			const first = reordered.read(6);
			const second = reordered.read();
			return received.join(",") === "alpha,beta,gamma" &&
				readableEvents > 0 &&
				pulled.readableEnded === true &&
				typeof encoded === "string" && encoded === "komari" &&
				first.toString() === "hello " && second.toString() === "world";
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream readable pull mode failed: %v", err)
	}
}

func TestStreamReadableFromAndAsyncIteration(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Readable } = require("stream");
			const fromArray = Readable.from([1, 2, 3]);
			const collected = [];
			const arrayIterator = fromArray[Symbol.asyncIterator]();
			let arrayStep;
			while (!(arrayStep = await arrayIterator.next()).done) collected.push(arrayStep.value);
			const fromAsync = Readable.from({
				[Symbol.asyncIterator]() {
					let index = 0;
					const values = ["x", "y", "z"];
					return {
						next() {
							if (index < values.length) return Promise.resolve({ value: values[index++], done: false });
							return Promise.resolve({ value: undefined, done: true });
						},
					};
				},
			});
			let text = "";
			const asyncIterator = fromAsync[Symbol.asyncIterator]();
			let asyncStep;
			while (!(asyncStep = await asyncIterator.next()).done) text += asyncStep.value;
			const fromString = Readable.from("komari");
			const stringChunks = [];
			const stringIterator = fromString[Symbol.asyncIterator]();
			let stringStep;
			while (!(stringStep = await stringIterator.next()).done) stringChunks.push(stringStep.value);
			const done = Readable.from([]);
			let endCount = 0;
			done.on("end", () => endCount++);
			done.resume();
			await new Promise((resolve) => done.once("close", resolve));
			return collected.join(",") === "1,2,3" &&
				text === "xyz" &&
				stringChunks.join("") === "komari" &&
				typeof fromArray[Symbol.asyncIterator] === "function" &&
				endCount === 1 && done.readableEnded === true;
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream Readable.from failed: %v", err)
	}
}
func TestStreamWritableFinishDrainAndCork(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Writable } = require("stream");
			const written = [];
			const writer = new Writable({
				write(chunk, encoding, callback) {
					written.push(chunk.toString());
					callback();
				},
			});
			writer.write("a");
			writer.write("b");
			const finishPromise = new Promise((resolve) => writer.once("finish", resolve));
			writer.end("c");
			await finishPromise;
			const slow = new Writable({
				highWaterMark: 4,
				write(chunk, encoding, callback) { setTimeout(callback, 5); },
			});
			const results = [];
			results.push(slow.write(Buffer.from("aaaa")));
			results.push(slow.write("b"));
			await new Promise((resolve) => slow.once("drain", resolve));
			results.push("drain");
			const endPromise = new Promise((resolve) => slow.once("finish", resolve));
			slow.end();
			await endPromise;
			const corked = new Writable({
				write(chunk, encoding, callback) {
					written.push("[" + chunk.toString() + "]");
					callback();
				},
			});
			corked.cork();
			corked.write("1");
			corked.write("2");
			const before = corked.writableLength;
			corked.uncork();
			const finish2 = new Promise((resolve) => corked.once("finish", resolve));
			corked.end();
			await finish2;
			return written.join("") === "abc[1][2]" &&
				writer.writableFinished === true &&
				writer.writableEnded === true &&
				results.join(",") === "false,false,drain" &&
				slow.writableNeedDrain === false &&
				before === 2 && corked.writableCorked === 0;
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream writable failed: %v", err)
	}
}

func TestStreamDuplex(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Duplex } = require("stream");
			const duplex = new Duplex({
				read() {
					this.push("from-nowhere");
					this.push(null);
				},
				write(chunk, encoding, callback) {
					this.push(chunk.toString().toUpperCase());
					callback();
				},
			});
			const seen = [];
			duplex.on("data", (chunk) => seen.push(chunk.toString()));
			duplex.write("abc");
			duplex.end();
			await new Promise((resolve) => duplex.once("close", resolve));
			return duplex.readable === true &&
				duplex.writable === true &&
				duplex.readableEnded === true &&
				duplex.writableFinished === true &&
				seen.join(",") === "ABC,from-nowhere";
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream duplex failed: %v", err)
	}
}
func TestStreamTransformAndPassThrough(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Readable, Transform, PassThrough, Writable } = require("stream");
			const upper = new Transform({
				transform(chunk, encoding, callback) {
					callback(null, chunk.toString().toUpperCase());
				},
				flush(callback) {
					this.push("!");
					callback();
				},
			});
			const output = [];
			const dest = new Writable({
				write(chunk, encoding, callback) { output.push(chunk.toString()); callback(); },
			});
			const source = Readable.from(["a", "b", "c"]);
			source.pipe(upper).pipe(dest);
			await new Promise((resolve, reject) => dest.once("finish", resolve).once("error", reject));
			const pass = new PassThrough({ objectMode: true });
			const passed = [];
			pass.on("data", (chunk) => passed.push(chunk));
			pass.write({ id: 1 });
			pass.write({ id: 2 });
			pass.end();
			await new Promise((resolve) => pass.once("close", resolve));
			return output.join("") === "ABC!" && passed.length === 2 && passed[0].id === 1 && passed[1].id === 2;
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream transform failed: %v", err)
	}
}

func TestStreamPipelineCallbackPromiseAndSourceKinds(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Readable, Transform, Writable, pipeline } = require("stream");
			const output = [];
			const dest = () => new Writable({
				write(chunk, encoding, callback) { output.push(chunk.toString()); callback(); },
			});
			const upper = () => new Transform({
				transform(chunk, encoding, callback) { callback(null, chunk.toString().toUpperCase()); },
			});
			await new Promise((resolve, reject) =>
				pipeline(Readable.from(["hello", "world"]), upper(), dest(), (error) => error ? reject(error) : resolve()));
			await pipeline(() => ["from-", "generator"], dest());
			await pipeline(Readable.from(["a", "b"]), [upper(), upper()], dest());
			let callbackFormReturn;
			await new Promise((resolve, reject) => {
				callbackFormReturn = pipeline(Readable.from(["z"]), dest(), (error) => error ? reject(error) : resolve());
			});
			const promiseForm = pipeline(Readable.from(["y"]), dest());
			await promiseForm;
			let missingArgs = null;
			try { await pipeline(Readable.from(["x"])); } catch (error) { missingArgs = error.message; }
			return output.join("|") === "HELLO|WORLD|from-|generator|A|B|z|y" &&
				callbackFormReturn === undefined &&
				typeof promiseForm.then === "function" &&
				typeof missingArgs === "string" && missingArgs.includes("at least two");
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream pipeline failed: %v", err)
	}
}
func TestStreamPipelineErrorPropagation(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Readable, Transform, Writable, pipeline, finished } = require("stream");
			const boom = new Transform({
				transform(chunk, encoding, callback) { callback(new Error("boom")); },
			});
			const dest = new Writable({
				write(chunk, encoding, callback) { callback(); },
			});
			let caught = null;
			try {
				await pipeline(Readable.from(["data"]), boom, dest);
			} catch (error) {
				caught = error.message;
			}
			const rejected = new Transform({
				transform(chunk, encoding, callback) { callback(new Error("rejected")); },
			});
			const dest2 = new Writable({ write(chunk, encoding, callback) { callback(); } });
			const callbackError = await new Promise((resolve) => {
				pipeline(Readable.from(["data"]), rejected, dest2, (error) => resolve(error ? error.message : null));
			});
			return caught === "boom" && callbackError === "rejected" && boom.destroyed === true && dest.destroyed === true;
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream pipeline error propagation failed: %v", err)
	}
}

func TestStreamFinishedAndPromisesModule(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Readable, Writable, finished } = require("stream");
			const promises = require("stream/promises");
			const nodePromises = require("node:stream/promises");
			const reader = Readable.from(["a", "b"]);
			reader.resume();
			await finished(reader, { cleanup: true });
			const writer = new Writable({ write(chunk, encoding, callback) { callback(); } });
			writer.write("x");
			writer.end();
			const cleanup = finished(writer, (error) => {
				if (error) throw error;
			});
			await new Promise((resolve) => writer.once("close", resolve));
			const callbackCleaned = typeof cleanup === "function";
			const failed = new Readable();
			failed.on("error", () => {});
			failed.destroy(new Error("failed"));
			const error = await promises.finished(failed).then(() => null, (err) => err.message);
			const premature = new Readable();
			premature.on("error", () => {});
			premature.destroy();
			const prematureError = await promises.finished(premature).then(() => null, (err) => String(err.message));
			return reader.readableEnded === true &&
				writer.writableFinished === true &&
				callbackCleaned === true &&
				error === "failed" &&
				prematureError.includes("premature close") &&
				promises.pipeline === require("stream").pipeline &&
				nodePromises.finished === promises.finished;
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream finished/promises failed: %v", err)
	}
}

func TestStreamDestroyAndErrorEvents(t *testing.T) {
	runtime := newStreamRuntime(t, `
		async function verify() {
			const { Readable, Writable } = require("stream");
			const events = [];
			const doomed = new Readable();
			doomed.on("error", (error) => events.push("error:" + error.message));
			doomed.on("close", () => events.push("close"));
			doomed.destroy(new Error("kaput"));
			const after = doomed.destroy();
			const writer = new Writable({ write(chunk, encoding, callback) { callback(); } });
			let writeCallbackError = null;
			writer.on("error", () => {});
			writer.destroy(new Error("writable-kaput"));
			writer.write("late", (error) => { writeCallbackError = error.message; });
			await new Promise((resolve) => setImmediate(resolve));
			return events.join(",") === "error:kaput,close" &&
				after === doomed &&
				doomed.errored.message === "kaput" &&
				doomed.readableAborted === true &&
				writer.destroyed === true &&
				writeCallbackError === "writable-kaput";
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("stream destroy/error failed: %v", err)
	}
}
