(function (stream, hooks) {
	"use strict";

	const { Writable, Readable } = stream;

	const stdout = new Writable({
		write(chunk, encoding, callback) {
			hooks.write("stdout", chunk, callback);
		},
	});
	const stderr = new Writable({
		write(chunk, encoding, callback) {
			hooks.write("stderr", chunk, callback);
		},
	});
	const stdin = new Readable({
		read() {
			// Standard input is not connected in jsruntime; the stream stays
			// open and never produces data.
		},
	});

	return { stdout, stderr, stdin };
})