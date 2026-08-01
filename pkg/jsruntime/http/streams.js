(function (stream) {
	"use strict";

	const { Readable, Writable } = stream;

	function createIncomingMessage() {
		return new Readable({
			read() {
				// The request body is fully buffered and pushed after the
				// request handler returns.
			},
		});
	}

	function createServerResponse(hooks) {
		return new Writable({
			write(chunk, encoding, callback) {
				hooks.write(this, chunk, callback);
			},
			final(callback) {
				hooks.finish(this, callback);
			},
			destroy(err, callback) {
				hooks.destroy(this);
				callback(err);
			},
		});
	}

	return { createIncomingMessage, createServerResponse };
})