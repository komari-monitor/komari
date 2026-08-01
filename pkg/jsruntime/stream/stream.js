(function () {
	"use strict";

	const EventEmitter = require("events");
	const { Buffer } = require("buffer");

	const kReadableState = Symbol("readableState");
	const kWritableState = Symbol("writableState");

	const defaultHighWaterMark = (objectMode) => (objectMode ? 16 : 65536);

	const chunkLength = (chunk, objectMode) => {
		if (objectMode) return 1;
		if (chunk == null) return 0;
		return (chunk.length || chunk.byteLength || 0) >>> 0;
	};

	const toBuffer = (chunk, encoding, objectMode) => {
		if (objectMode) return chunk;
		if (chunk instanceof Buffer || chunk instanceof Uint8Array) return chunk;
		if (chunk instanceof DataView || ArrayBuffer.isView(chunk)) {
			return Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
		}
		if (typeof chunk === "string") return Buffer.from(chunk, encoding || "utf8");
		return chunk;
	};

	const schedule = (callback) => {
		if (typeof process !== "undefined" && typeof process.nextTick === "function") {
			process.nextTick(callback);
		} else {
			setImmediate(callback);
		}
	};

	function initReadableState(stream, options) {
		options = options || {};
		const objectMode = !!options.objectMode;
		const highWaterMark = options.highWaterMark != null
			? Number(options.highWaterMark)
			: (options.readableHighWaterMark != null ? Number(options.readableHighWaterMark) : defaultHighWaterMark(objectMode));
		stream[kReadableState] = {
			objectMode,
			highWaterMark,
			buffer: [],
			length: 0,
			ended: false,
			endEmitted: false,
			flowing: null,
			reading: false,
			needReadable: false,
			destroyed: false,
			errored: null,
			encoding: null,
			pipes: [],
			readable: options.readable !== false,
			disturbed: false,
			emitReadableScheduled: false,
			flowScheduled: false,
			autoDestroy: options.autoDestroy !== false,
		};
		stream._readableState = stream[kReadableState];
		if (typeof options.read === "function") stream._read = options.read;
	}

	function initWritableState(stream, options) {
		options = options || {};
		const objectMode = !!options.objectMode;
		const highWaterMark = options.highWaterMark != null
			? Number(options.highWaterMark)
			: (options.writableHighWaterMark != null ? Number(options.writableHighWaterMark) : defaultHighWaterMark(objectMode));
		stream[kWritableState] = {
			objectMode,
			highWaterMark,
			pending: [],
			corkedBuffer: [],
			length: 0,
			writing: false,
			ended: false,
			ending: false,
			finished: false,
			corked: 0,
			needDrain: false,
			destroyed: false,
			errored: null,
			defaultEncoding: "utf8",
			writable: options.writable !== false,
			autoDestroy: options.autoDestroy !== false,
		};
		stream._writableState = stream[kWritableState];
		if (typeof options.write === "function") stream._write = options.write;
		if (typeof options.final === "function") stream._final = options.final;
		if (typeof options.destroy === "function") stream._destroy = options.destroy;
	}
	class Stream extends EventEmitter {
		_destroy(err, callback) {
			callback(err);
		}

		destroy(err) {
			const rstate = this[kReadableState];
			const wstate = this[kWritableState];
			const already = (rstate && rstate.destroyed) || (wstate && wstate.destroyed);
			if (already) return this;
			if (rstate) {
				rstate.destroyed = true;
				rstate.ended = true;
				if (err) rstate.errored = err;
			}
			if (wstate) {
				wstate.destroyed = true;
				wstate.ended = true;
				wstate.ending = true;
				if (err) wstate.errored = err;
				const fail = (list) => {
					for (const item of list) {
						if (typeof item[2] === "function") item[2](err || new Error("stream destroyed"));
					}
				};
				fail(wstate.pending);
				fail(wstate.corkedBuffer);
				wstate.pending.length = 0;
				wstate.corkedBuffer.length = 0;
				wstate.length = 0;
			}
			let called = false;
			const complete = (callbackError) => {
				if (called) return;
				called = true;
				const error = callbackError || err || null;
				if (error) {
					if (rstate && !rstate.errored) rstate.errored = error;
					if (wstate && !wstate.errored) wstate.errored = error;
					this.emit("error", error);
				}
				this.emit("close");
			};
			if (this._destroy !== Stream.prototype._destroy) {
				try {
					this._destroy(err || null, complete);
				} catch (error) {
					complete(error);
				}
			} else {
				complete(err || null);
			}
			return this;
		}

		pipe(dest, options) {
			const state = this[kReadableState];
			if (!state) throw new TypeError("pipe() is only valid on readable streams");
			if (!dest || typeof dest.write !== "function") throw new TypeError("pipe() destination must be a writable stream");
			const end = !options || options.end !== false;
			const handlers = {
				ondata: (chunk) => {
					if (dest.destroyed) {
						this.unpipe(dest);
						return;
					}
					const ok = dest.write(chunk);
					if (ok === false) this.pause();
				},
				onend: () => {
					if (end && !dest.destroyed && typeof dest.end === "function" && dest.writable !== false) dest.end();
				},
				onerror: () => {
					this.unpipe(dest);
				},
				ondrain: () => {
					if (this.isPaused() && !state.ended && !state.destroyed) this.resume();
				},
				ondesterror: () => {
					this.unpipe(dest);
				},
				ondestclose: () => {
					this.unpipe(dest);
				},
			};
			this.on("data", handlers.ondata);
			this.on("end", handlers.onend);
			this.on("error", handlers.onerror);
			if (typeof dest.on === "function") {
				dest.on("drain", handlers.ondrain);
				dest.on("error", handlers.ondesterror);
				dest.on("close", handlers.ondestclose);
			}
			state.pipes.push({ dest, handlers, end });
			if (typeof dest.emit === "function") dest.emit("pipe", this);
			this.resume();
			return dest;
		}

		unpipe(dest) {
			const state = this[kReadableState];
			if (!state) return this;
			if (!dest) {
				const pipes = state.pipes.slice();
				for (const pipe of pipes) this._unpipeOne(pipe);
				return this;
			}
			for (let i = state.pipes.length - 1; i >= 0; i--) {
				if (state.pipes[i].dest === dest) this._unpipeOne(state.pipes[i]);
			}
			return this;
		}

		_unpipeOne(pipe) {
			const state = this[kReadableState];
			if (!state) return;
			const { dest, handlers } = pipe;
			this.removeListener("data", handlers.ondata);
			this.removeListener("end", handlers.onend);
			this.removeListener("error", handlers.onerror);
			if (typeof dest.removeListener === "function") {
				dest.removeListener("drain", handlers.ondrain);
				dest.removeListener("error", handlers.ondesterror);
				dest.removeListener("close", handlers.ondestclose);
			}
			const index = state.pipes.indexOf(pipe);
			if (index >= 0) state.pipes.splice(index, 1);
			if (typeof dest.emit === "function") dest.emit("unpipe", this);
		}

		pause() {
			return this;
		}

		resume() {
			return this;
		}

		isPaused() {
			return false;
		}

		_autoDestroy() {
			const rstate = this[kReadableState];
			const wstate = this[kWritableState];
			if (rstate && !rstate.endEmitted) return;
			if (wstate && !wstate.finished) return;
			schedule(() => {
				if (this.destroyed) return;
				try {
					this.destroy();
				} catch (error) {
					schedule(() => {
						throw error;
					});
				}
			});
		}
	}

	class Readable extends Stream {
		constructor(options) {
			super();
			initReadableState(this, options);
			if (options && typeof options.destroy === "function") this._destroy = options.destroy;
		}

		get readable() {
			return this[kReadableState].readable;
		}

		get readableFlowing() {
			return this[kReadableState].flowing;
		}

		get readableEnded() {
			return this[kReadableState].endEmitted;
		}

		get readableLength() {
			return this[kReadableState].length;
		}

		get readableHighWaterMark() {
			return this[kReadableState].highWaterMark;
		}

		get readableObjectMode() {
			return this[kReadableState].objectMode;
		}

		get readableEncoding() {
			return this[kReadableState].encoding;
		}

		get readableAborted() {
			const state = this[kReadableState];
			return state.destroyed && !state.endEmitted;
		}

		get readableDidRead() {
			return this[kReadableState].disturbed;
		}

		get destroyed() {
			return this[kReadableState].destroyed;
		}

		get errored() {
			return this[kReadableState].errored;
		}

		_read(size) {
			// Implemented by stream subclasses.
		}

		push(chunk, encoding) {
			const state = this[kReadableState];
			if (!state.readable) throw new TypeError("Cannot push to a stream that is not readable");
			if (state.destroyed) return false;
			if (chunk === null) {
				state.ended = true;
				state.reading = false;
				if (!state.endEmitted && state.length === 0) this._emitEnd();
				return false;
			}
			if (!state.objectMode) {
				chunk = toBuffer(chunk, encoding, false);
				if (typeof chunk === "number" || (typeof chunk === "object" && !(chunk instanceof Buffer) && !(chunk instanceof Uint8Array))) {
					throw new TypeError("Invalid non-string/buffer chunk");
				}
			}
			if (chunk === undefined || (typeof chunk.length === "number" && chunk.length === 0)) {
				state.reading = false;
				return true;
			}
			state.reading = false;
			state.disturbed = true;
			state.buffer.push(chunk);
			state.length += chunkLength(chunk, state.objectMode);
			if (state.flowing) {
				this._scheduleFlow();
			} else if (!state.emitReadableScheduled && !state.ended) {
				this._emitReadable();
			}
			return state.length < state.highWaterMark;
		}

		unshift(chunk, encoding) {
			const state = this[kReadableState];
			if (state.flowing) throw new Error("stream.unshift() is only valid in paused mode");
			if (chunk === null) {
				this.push(null);
				return;
			}
			if (!state.objectMode) chunk = toBuffer(chunk, encoding, false);
			state.disturbed = true;
			state.buffer.unshift(chunk);
			state.length += chunkLength(chunk, state.objectMode);
		}
		read(size) {
			const state = this[kReadableState];
			if (!state.readable) throw new TypeError("Cannot read from a stream that is not readable");
			if (size != null && size !== 0 && (!Number.isFinite(size) || size < 0)) {
				throw new RangeError("read() size must be a non-negative integer");
			}
			if (size === 0) return null;
			state.disturbed = true;
			if (state.buffer.length === 0) {
				if (state.ended) {
					this._emitEnd();
					return null;
				}
				state.needReadable = true;
				this._maybeRead();
				return null;
			}
			let chunk;
			if (size == null) {
				chunk = state.buffer.shift();
				state.length -= chunkLength(chunk, state.objectMode);
			} else if (state.objectMode) {
				chunk = state.buffer.shift();
				state.length -= 1;
			} else {
				const wanted = Math.floor(size);
				let total = 0;
				const parts = [];
				while (state.buffer.length > 0 && total < wanted) {
					const next = state.buffer.shift();
					parts.push(next);
					total += next.length;
				}
				if (total > wanted) {
					const last = parts[parts.length - 1];
					const excess = total - wanted;
					const keep = last.subarray(last.length - excess);
					const take = last.subarray(0, last.length - excess);
					parts[parts.length - 1] = take;
					state.buffer.unshift(keep);
					total = wanted;
				}
				state.length -= total;
				chunk = parts.length === 1 ? parts[0] : concatBuffers(parts);
			}
			if (state.encoding && !state.objectMode) chunk = chunk.toString(state.encoding);
			if (state.buffer.length === 0 && !state.ended) {
				state.needReadable = true;
				this._maybeRead();
			}
			if (state.ended && state.buffer.length === 0 && !state.endEmitted) this._emitEnd();
			return chunk;
		}

		setEncoding(encoding) {
			this[kReadableState].encoding = encoding;
			return this;
		}

		pause() {
			this[kReadableState].flowing = false;
			return this;
		}

		resume() {
			const state = this[kReadableState];
			if (!state.readable) return this;
			if (state.flowing === true) return this;
			state.flowing = true;
			state.disturbed = true;
			this._scheduleFlow();
			return this;
		}

		isPaused() {
			return this[kReadableState].flowing !== true;
		}

		on(name, listener) {
			const result = super.on(name, listener);
			if (name === "data") {
				this.resume();
			} else if (name === "readable") {
				this.pause();
				schedule(() => this._maybeRead());
			}
			return result;
		}

		addListener(name, listener) {
			return this.on(name, listener);
		}

		once(name, listener) {
			const result = super.once(name, listener);
			if (name === "data") {
				this.resume();
			} else if (name === "readable") {
				this.pause();
				schedule(() => this._maybeRead());
			}
			return result;
		}

		removeListener(name, listener) {
			const result = super.removeListener(name, listener);
			if (name === "data" && this.listenerCount("data") === 0 && this[kReadableState].flowing) {
				this.pause();
			}
			return result;
		}

		off(name, listener) {
			return this.removeListener(name, listener);
		}

		[Symbol.asyncIterator]() {
			const stream = this;
			const state = stream[kReadableState];
			state.disturbed = true;
			const queue = [];
			const waiters = [];
			let done = false;
			let failed = null;
			const ondata = (chunk) => {
				const waiter = waiters.shift();
				if (waiter) waiter.resolve({ value: chunk, done: false });
				else queue.push(chunk);
			};
			const onend = () => {
				if (done) return;
				done = true;
				for (const waiter of waiters.splice(0)) waiter.resolve({ value: undefined, done: true });
			};
			const onerror = (error) => {
				if (done) return;
				done = true;
				failed = error;
				for (const waiter of waiters.splice(0)) waiter.reject(error);
			};
			const cleanup = () => {
				stream.removeListener("data", ondata);
				stream.removeListener("end", onend);
				stream.removeListener("error", onerror);
				stream.removeListener("close", onend);
			};
			stream.on("data", ondata);
			stream.on("end", onend);
			stream.on("error", onerror);
			stream.on("close", onend);
			stream.resume();
			return {
				next() {
					if (queue.length > 0) return Promise.resolve({ value: queue.shift(), done: false });
					if (done) {
						if (failed) return Promise.reject(failed);
						return Promise.resolve({ value: undefined, done: true });
					}
					return new Promise((resolve, reject) => waiters.push({ resolve, reject }));
				},
				return() {
					cleanup();
					if (!done) done = true;
					return Promise.resolve({ value: undefined, done: true });
				},
				[Symbol.asyncIterator]() {
					return this;
				},
			};
		}

		_maybeRead() {
			const state = this[kReadableState];
			if (state.reading || state.ended || state.destroyed) return;
			if (state.length >= state.highWaterMark) return;
			state.reading = true;
			try {
				this._read(state.highWaterMark - state.length);
			} catch (error) {
				state.reading = false;
				this.destroy(error);
			}
		}

		_scheduleFlow() {
			const state = this[kReadableState];
			if (state.flowScheduled || state.destroyed) return;
			state.flowScheduled = true;
			schedule(() => {
				state.flowScheduled = false;
				this._flow();
			});
		}

		_flow() {
			const state = this[kReadableState];
			while (state.flowing && !state.destroyed) {
				if (state.buffer.length > 0) {
					const chunk = state.buffer.shift();
					state.length -= chunkLength(chunk, state.objectMode);
					state.needReadable = false;
					this.emit("data", state.encoding && !state.objectMode ? chunk.toString(state.encoding) : chunk);
				} else if (state.ended) {
					this._emitEnd();
					break;
				} else {
					this._maybeRead();
					if (state.buffer.length === 0 && !state.ended) break;
				}
			}
		}

		_emitReadable() {
			const state = this[kReadableState];
			if (state.emitReadableScheduled || state.destroyed || state.ended) return;
			state.emitReadableScheduled = true;
			schedule(() => {
				state.emitReadableScheduled = false;
				if (state.destroyed || state.endEmitted) return;
				if (state.buffer.length > 0) this.emit("readable");
			});
		}

		_emitEnd() {
			const state = this[kReadableState];
			if (state.endEmitted || state.destroyed) return;
			state.endEmitted = true;
			this.emit("end");
			this._autoDestroy();
		}

		static from(iterable, options) {
			if (typeof iterable === "string") {
				const stream = new Readable(Object.assign({}, options, { objectMode: true }));
				schedule(() => {
					if (!stream.destroyed) {
						stream.push(iterable);
						stream.push(null);
					}
				});
				return stream;
			}
			if (iterable == null ||
				(typeof iterable[Symbol.iterator] !== "function" && typeof iterable[Symbol.asyncIterator] !== "function")) {
				throw new TypeError("Readable.from() requires an iterable");
			}
			const stream = new Readable(Object.assign({}, options, { objectMode: options ? options.objectMode !== false : true }));
			if (typeof iterable[Symbol.asyncIterator] === "function") {
				const iterator = iterable[Symbol.asyncIterator]();
				stream._read = () => {
					Promise.resolve(iterator.next()).then((result) => {
						if (stream.destroyed) return;
						if (result.done) stream.push(null);
						else stream.push(result.value);
					}).catch((error) => stream.destroy(error));
				};
			} else {
				const iterator = iterable[Symbol.iterator]();
				let done = false;
				stream._read = () => {
					if (done) return;
					try {
						const result = iterator.next();
						if (result.done) {
							done = true;
							stream.push(null);
						} else {
							stream.push(result.value);
						}
					} catch (error) {
						done = true;
						stream.destroy(error);
					}
				};
			}
			return stream;
		}

		static isDisturbed(stream) {
			return !!(stream && stream.readableDidRead);
		}
	}
	class Writable extends Stream {
		constructor(options) {
			super();
			initWritableState(this, options);
		}

		get writable() {
			return this[kWritableState].writable;
		}

		get writableEnded() {
			return this[kWritableState].ended;
		}

		get writableFinished() {
			return this[kWritableState].finished;
		}

		get writableLength() {
			return this[kWritableState].length;
		}

		get writableHighWaterMark() {
			return this[kWritableState].highWaterMark;
		}

		get writableObjectMode() {
			return this[kWritableState].objectMode;
		}

		get writableCorked() {
			return this[kWritableState].corked;
		}

		get writableNeedDrain() {
			return this[kWritableState].needDrain;
		}

		get destroyed() {
			return this[kWritableState].destroyed;
		}

		get errored() {
			return this[kWritableState].errored;
		}

		_write(chunk, encoding, callback) {
			callback(new Error("Writable._write() is not implemented"));
		}

		_final(callback) {
			callback();
		}

		write(chunk, encoding, callback) {
			const state = this[kWritableState];
			if (!state.writable) throw new TypeError("stream is not writable");
			if (typeof encoding === "function") {
				callback = encoding;
				encoding = null;
			}
			if (state.destroyed) {
				if (typeof callback === "function") schedule(() => callback(state.errored || new Error("stream destroyed")));
				return false;
			}
			if (state.ended) {
				const error = new Error("write after end");
				if (typeof callback === "function") schedule(() => callback(error));
				this.destroy(error);
				return false;
			}
			if (!state.objectMode) {
				chunk = toBuffer(chunk, encoding || state.defaultEncoding, false);
				if (typeof chunk === "number" || (typeof chunk === "object" && chunk !== null && !(chunk instanceof Buffer) && !(chunk instanceof Uint8Array))) {
					throw new TypeError("Invalid non-string/buffer chunk");
				}
			}
			if (chunk == null) chunk = Buffer.alloc(0);
			state.length += chunkLength(chunk, state.objectMode);
			if (state.corked > 0) {
				state.corkedBuffer.push([chunk, encoding, callback]);
				return true;
			}
			state.pending.push([chunk, encoding, callback]);
			this._doWrite();
			const ok = state.length < state.highWaterMark;
			if (!ok) state.needDrain = true;
			return ok;
		}

		end(chunk, encoding, callback) {
			const state = this[kWritableState];
			if (!state.writable) throw new TypeError("stream is not writable");
			if (typeof chunk === "function") {
				callback = chunk;
				chunk = null;
				encoding = null;
			} else if (typeof encoding === "function") {
				callback = encoding;
				encoding = null;
			}
			if (state.destroyed) {
				if (typeof callback === "function") schedule(() => callback(state.errored || new Error("stream destroyed")));
				return this;
			}
			if (chunk != null && chunk !== "") {
				this.write(chunk, encoding, callback);
			} else if (typeof callback === "function") {
				this.once("finish", callback);
			}
			if (state.ended) return this;
			state.ended = true;
			state.ending = true;
			this._maybeFinish();
			return this;
		}

		_doWrite() {
			const state = this[kWritableState];
			if (state.destroyed || state.writing) return;
			const item = state.pending.shift();
			if (!item) return;
			state.writing = true;
			const chunk = item[0];
			const encoding = item[1];
			const callback = item[2];
			let finished = false;
			const onwrite = (error) => {
				if (finished) return;
				finished = true;
				state.writing = false;
				state.length = Math.max(0, state.length - chunkLength(chunk, state.objectMode));
				if (error) {
					if (callback) callback(error);
					this.destroy(error);
					return;
				}
				if (callback) callback();
				if (state.destroyed) return;
				if (state.pending.length > 0) {
					this._doWrite();
					return;
				}
				if (state.needDrain && state.length < state.highWaterMark) {
					state.needDrain = false;
					this.emit("drain");
				}
				this._maybeFinish();
			};
			try {
				this._write(chunk, encoding, onwrite);
			} catch (error) {
				onwrite(error);
			}
		}

		_maybeFinish() {
			const state = this[kWritableState];
			if (!state.ending || state.finished || state.destroyed) return;
			if (state.writing || state.pending.length > 0 || state.corkedBuffer.length > 0) return;
			const complete = () => {
				if (state.finished || state.destroyed) return;
				state.finished = true;
				this.emit("finish");
				if (state.autoDestroy) this._autoDestroy();
			};
			if (this._final !== Writable.prototype._final) {
				let called = false;
				try {
					this._final((error) => {
						if (called) return;
						called = true;
						if (error) {
							this.destroy(error);
							return;
						}
						complete();
					});
				} catch (error) {
					this.destroy(error);
				}
			} else {
				complete();
			}
		}

		cork() {
			this[kWritableState].corked++;
		}

		uncork() {
			const state = this[kWritableState];
			if (state.corked <= 0) return;
			state.corked--;
			if (state.corked > 0) return;
			if (state.corkedBuffer.length === 0) return;
			const buffered = state.corkedBuffer;
			state.corkedBuffer = [];
			for (const item of buffered) state.pending.push(item);
			this._doWrite();
		}

		setDefaultEncoding(encoding) {
			this[kWritableState].defaultEncoding = encoding;
			return this;
		}
	}

	class Duplex extends Readable {
		constructor(options) {
			super(options);
			initWritableState(this, options);
			const allowHalfOpen = !options || options.allowHalfOpen !== false;
			if (allowHalfOpen === false) {
				this.on("end", () => {
					const wstate = this[kWritableState];
					if (wstate && !wstate.ended && !wstate.destroyed) this.end();
				});
				this.on("finish", () => {
					const rstate = this[kReadableState];
					if (rstate && !rstate.ended && !rstate.destroyed) this.push(null);
				});
			}
		}
	}

	for (const name of Object.getOwnPropertyNames(Writable.prototype)) {
		if (name === "constructor" || name === "destroy") continue;
		const descriptor = Object.getOwnPropertyDescriptor(Writable.prototype, name);
		Object.defineProperty(Duplex.prototype, name, descriptor);
	}

	class Transform extends Duplex {
		constructor(options) {
			options = options || {};
			super(Object.assign({}, options, { allowHalfOpen: options.allowHalfOpen === true }));
			this._transformState = {
				transforming: false,
				writechunk: null,
				writeencoding: null,
				writecb: null,
				pending: [],
			};
			if (typeof options.transform === "function") this._transform = options.transform;
			if (typeof options.flush === "function") this._flush = options.flush;
		}

		_transform(chunk, encoding, callback) {
			callback(new Error("Transform._transform() is not implemented"));
		}

		_flush(callback) {
			callback();
		}

		_write(chunk, encoding, callback) {
			const state = this._transformState;
			if (state.writechunk !== null && state.writechunk !== undefined) {
				state.pending.push([chunk, encoding, callback]);
				return;
			}
			state.writechunk = chunk;
			state.writeencoding = encoding;
			state.writecb = callback;
			this._doTransform();
		}

		_doTransform() {
			const state = this._transformState;
			if (state.transforming || state.writechunk === null || state.writechunk === undefined) return;
			state.transforming = true;
			const chunk = state.writechunk;
			const encoding = state.writeencoding;
			let called = false;
			const done = (error, data) => {
				if (called) return;
				called = true;
				state.transforming = false;
				state.writechunk = null;
				state.writeencoding = null;
				const callback = state.writecb;
				state.writecb = null;
				if (data !== null && data !== undefined && data !== "") {
					try {
						this.push(data);
					} catch (pushError) {
						if (!error) error = pushError;
					}
				}
				if (error) {
					if (callback) callback(error);
					this.destroy(error);
					return;
				}
				if (callback) callback();
			};
			try {
				this._transform(chunk, encoding, done);
			} catch (error) {
				done(error);
			}
		}

		_final(callback) {
			this._flush((error) => {
				if (error) {
					callback(error);
					return;
				}
				this.push(null);
				callback();
			});
		}
	}

	class PassThrough extends Transform {
		_transform(chunk, encoding, callback) {
			callback(null, chunk);
		}
	}

	function concatBuffers(parts) {
		let total = 0;
		for (const part of parts) total += part.length;
		const result = Buffer.alloc(total);
		let offset = 0;
		for (const part of parts) {
			result.set(part, offset);
			offset += part.length;
		}
		return result;
	}
	function pipeline(...streams) {
		let callback = streams[streams.length - 1];
		if (typeof callback === "function") {
			streams.pop();
		} else {
			callback = null;
		}

		const promise = new Promise((resolve, reject) => {
			const flattened = [];
			for (const stream of streams) {
				if (Array.isArray(stream)) flattened.push(...stream);
				else flattened.push(stream);
			}
			streams = flattened;

			let completed = false;
			const fail = (error) => {
				if (completed) return;
				completed = true;
				for (const stream of streams) {
					if (stream && typeof stream.destroy === "function") {
						try {
							stream.destroy(error);
						} catch (destroyError) {
							// destroy() may throw when 'error' has no listener.
						}
					}
				}
				reject(error);
			};
			const succeed = () => {
				if (completed) return;
				completed = true;
				resolve();
			};

			if (streams.length === 0) {
				fail(new TypeError("pipeline() requires at least one stream"));
				return;
			}

			const attachError = (stream) => {
				if (stream && typeof stream.on === "function") stream.on("error", fail);
			};

			if (streams.length < 2) {
				fail(new TypeError("pipeline() requires at least two streams"));
				return;
			}
			let source = streams[0];
			if (!source || typeof source.pipe !== "function") {
				try {
					if (typeof source === "function") source = Readable.from(source({ signal: undefined }));
					else source = Readable.from(source);
				} catch (sourceError) {
					fail(new TypeError("pipeline() source must be a stream or iterable"));
					return;
				}
				attachError(source);
			}
			const dest = streams[streams.length - 1];
			if (!dest || typeof dest.write !== "function") {
				fail(new TypeError("pipeline() destination must be a writable stream"));
				return;
			}
			for (const stream of streams) attachError(stream);

			let current = source;
			for (let i = 1; i < streams.length; i++) {
				const next = streams[i];
				if (!next || typeof next.pipe !== "function") {
					fail(new TypeError("pipeline() transform streams must be duplex"));
					return;
				}
				current.pipe(next);
				current = next;
			}

			let destDone = false;
			const markDestDone = () => {
				destDone = true;
				succeed();
			};
			if (dest.writable === true || typeof dest.write === "function") {
				if (dest.writableFinished) markDestDone();
				else dest.on("finish", markDestDone);
			}
			if (dest.readable === true) {
				if (dest.readableEnded) markDestDone();
				else dest.on("end", markDestDone);
			}
			if (dest.writable !== true && dest.readable !== true) markDestDone();
			dest.on("close", () => {
				if (!destDone && !dest.writableFinished && !dest.readableEnded) {
					fail(dest.errored || new Error("premature close"));
				}
			});
		});

		if (callback) {
			promise.then(() => callback(null), (error) => callback(error));
			return undefined;
		}
		return promise;
	}

	function finished(stream, options, callback) {
		if (typeof options === "function") {
			callback = options;
			options = {};
		}
		options = options || {};
		if (!stream || typeof stream.on !== "function") {
			const error = new TypeError("finished() requires a stream");
			if (typeof callback === "function") {
				schedule(() => callback(error));
				return () => {};
			}
			return Promise.reject(error);
		}

		const wantCleanup = options.cleanup === true;
		const readable = options.readable !== false &&
			(stream.readable === true || typeof stream.pipe === "function");
		const writable = options.writable !== false &&
			(stream.writable === true || typeof stream.write === "function");

		let done = false;
		const listeners = [];
		const removeListeners = () => {
			for (const item of listeners) {
				const target = item[0];
				const name = item[1];
				const listener = item[2];
				if (target && typeof target.removeListener === "function") target.removeListener(name, listener);
			}
			listeners.length = 0;
		};
		let settle = null;
		const promise = new Promise((resolve, reject) => {
			settle = (error) => {
				if (done) return;
				done = true;
				if (wantCleanup) removeListeners();
				if (error) reject(error);
				else resolve();
			};
		});

		const onerror = (error) => settle(error);
		const onend = () => settle();
		const onfinish = () => settle();
		const onclose = () => {
			const ended = readable && stream.readableEnded;
			const finishedFlag = writable && stream.writableFinished;
			if (ended || finishedFlag) settle();
			else settle(stream.errored || new Error("premature close"));
		};
		const listen = (target, name, listener) => {
			target.on(name, listener);
			listeners.push([target, name, listener]);
		};

		listen(stream, "error", onerror);
		if (readable) listen(stream, "end", onend);
		if (writable) listen(stream, "finish", onfinish);
		listen(stream, "close", onclose);

		if (options.signal) {
			if (options.signal.aborted) {
				settle(new Error("The operation was aborted"));
			} else {
				listen(options.signal, "abort", () => settle(new Error("The operation was aborted")));
			}
		}

		if (stream.destroyed && !stream.readableEnded && !stream.writableFinished) {
			settle(stream.errored || new Error("premature close"));
		} else if ((readable && stream.readableEnded) || (writable && stream.writableFinished)) {
			settle();
		}

		if (typeof callback === "function") {
			promise.then(() => callback(null), (error) => callback(error));
			return removeListeners;
		}
		return promise;
	}

	function addAbortSignal(signal, stream) {
		if (!signal || typeof signal.addEventListener !== "function") {
			throw new TypeError("addAbortSignal() requires an AbortSignal");
		}
		if (!stream || typeof stream.destroy !== "function") {
			throw new TypeError("addAbortSignal() requires a stream");
		}
		if (signal.aborted) {
			stream.destroy(signal.reason || new Error("The operation was aborted"));
			return stream;
		}
		signal.addEventListener("abort", () => {
			stream.destroy(signal.reason || new Error("The operation was aborted"));
		}, { once: true });
		return stream;
	}

	function isErrored(stream) {
		if (!stream || typeof stream !== "object") return false;
		return stream.errored || false;
	}

	function isReadable(stream) {
		if (!stream || typeof stream !== "object") return false;
		return stream.readable === true && !stream.destroyed;
	}

	function getDefaultHighWaterMark(objectMode) {
		return defaultHighWaterMark(!!objectMode);
	}

	return {
		Stream,
		Readable,
		Writable,
		Duplex,
		Transform,
		PassThrough,
		pipeline,
		finished,
		addAbortSignal,
		isErrored,
		isReadable,
		getDefaultHighWaterMark,
		promises: {
			pipeline: (...args) => pipeline(...args),
			finished: (...args) => finished(...args),
		},
	};
})()