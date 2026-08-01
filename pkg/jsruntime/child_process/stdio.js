(function (stream, hooks) {
	"use strict";

	const { Writable, Readable } = stream;

	function createStdin() {
		return new Writable({
			write(chunk, encoding, callback) {
				hooks.stdinWrite(chunk, callback);
			},
			final(callback) {
				hooks.stdinEnd(callback);
			},
			destroy(err, callback) {
				hooks.stdinDestroy();
				callback(err);
			},
		});
	}

	function createOutput() {
		return new Readable({
			read() {
				// Output is pushed from the Go side as it arrives.
			},
		});
	}

	return { createStdin, createOutput };
})