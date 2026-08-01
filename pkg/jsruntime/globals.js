		globalThis.queueMicrotask = function queueMicrotask(callback) {
			if (typeof callback !== "function") {
				throw new TypeError("queueMicrotask callback must be a function");
			}
			Promise.resolve().then(callback);
		};
	