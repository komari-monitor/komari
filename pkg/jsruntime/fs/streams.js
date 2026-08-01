(function (fs, stream, bufferModule) {
	"use strict";

	const { Readable, Writable } = stream;
	const { Buffer } = bufferModule;

	function createReadStream(path, options) {
		options = options || {};
		const highWaterMark = options.highWaterMark != null ? options.highWaterMark : 65536;
		const encoding = options.encoding;
		const autoClose = options.autoClose !== false;
		const start = options.start != null ? options.start : 0;
		const end = options.end; // inclusive byte position; undefined means EOF
		let fd = null;
		let position = start;
		let finished = false;
		let openEmitted = false;

		const source = new Readable({
			highWaterMark,
			encoding,
			read() {
				readChunk();
			},
			destroy(err, callback) {
				if (fd !== null && !finished) {
					const current = fd;
					fd = null;
					fs.close(current, () => callback(err));
				} else {
					callback(err);
				}
			},
		});

		function fail(err) {
			if (finished) return;
			finished = true;
			source.destroy(err);
		}

		function readChunk() {
			if (fd === null || finished) return;
			if (end != null && position > end) {
				finish();
				return;
			}
			const remaining = end == null ? highWaterMark : Math.min(highWaterMark, end - position + 1);
			const buffer = Buffer.alloc(remaining);
			fs.read(fd, buffer, 0, remaining, position, (err, bytesRead) => {
				if (err) {
					fail(err);
					return;
				}
				position += bytesRead;
				if (bytesRead === 0) {
					finish();
					return;
				}
				const chunk = bytesRead === buffer.length ? buffer : buffer.subarray(0, bytesRead);
				source.push(chunk);
			});
		}

		function finish() {
			if (finished) return;
			finished = true;
			source.push(null);
			if (autoClose && fd !== null) {
				const current = fd;
				fd = null;
				fs.close(current, () => {});
			}
		}

		fs.open(path, "r", (err, fdValue) => {
			if (err) {
				fail(err);
				return;
			}
			fd = fdValue;
			if (!openEmitted) {
				openEmitted = true;
				source.emit("open", fdValue);
			}
			readChunk();
		});
		return source;
	}

	function createWriteStream(path, options) {
		options = options || {};
		const flags = options.flags || "w";
		const mode = options.mode != null ? options.mode : 0o666;
		const highWaterMark = options.highWaterMark;
		const autoClose = options.autoClose !== false;
		let fd = null;
		let pending = [];
		let openEmitted = false;

		const dest = new Writable({
			highWaterMark,
			write(chunk, encoding, callback) {
				if (fd === null) {
					pending.push([chunk, callback]);
					return;
				}
				writeChunk(chunk, callback);
			},
			final(callback) {
				if (fd !== null && autoClose) {
					const current = fd;
					fd = null;
					fs.close(current, () => callback());
				} else {
					callback();
				}
			},
			destroy(err, callback) {
				if (fd !== null) {
					const current = fd;
					fd = null;
					fs.close(current, () => callback(err));
				} else {
					callback(err);
				}
			},
		});

		function writeChunk(chunk, callback) {
			fs.write(fd, chunk, 0, chunk.length, null, (err) => {
				callback(err);
			});
		}

		function failPending(error) {
			const queued = pending;
			pending = [];
			for (const item of queued) item[1](error);
		}

		fs.open(path, flags, mode, (err, fdValue) => {
			if (err) {
				failPending(err);
				dest.destroy(err);
				return;
			}
			fd = fdValue;
			if (!openEmitted) {
				openEmitted = true;
				dest.emit("open", fdValue);
			}
			if (pending.length === 0) return;
			const queued = pending;
			pending = [];
			let index = 0;
			const drain = () => {
				if (index >= queued.length) return;
				const item = queued[index++];
				writeChunk(item[0], (writeErr) => {
					if (writeErr) {
						for (; index < queued.length; index++) queued[index][1](writeErr);
						item[1](writeErr);
						dest.destroy(writeErr);
						return;
					}
					item[1]();
					drain();
				});
			};
			drain();
		});
		return dest;
	}

	return { createReadStream, createWriteStream };
})