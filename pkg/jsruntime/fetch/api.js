(function () {
	"use strict";
	const nativeBodyBuffer = globalThis.__komariBodyBuffer;
	const nativeBodyText = globalThis.__komariBodyText;
	const nativeEncodeFormData = globalThis.__komariEncodeFormData;
	const nativeParseFormData = globalThis.__komariParseFormData;
	const nativeNewAbortSignal = globalThis.__komariNewAbortSignal;
	const nativeAbortSignal = globalThis.__komariAbortSignal;
	const nativeFetch = globalThis.__komariFetch;
	const nativeFetchSync = globalThis.__komariFetchSync;
	const slots = new WeakMap();
	const slot = (value) => slots.get(value);

	function copyBuffer(buffer) {
		const source = new Uint8Array(buffer);
		const copy = new Uint8Array(source.length);
		copy.set(source);
		return copy.buffer;
	}

	function toBuffer(value) {
		if (value == null) return new ArrayBuffer(0);
		if (value instanceof Blob) return copyBuffer(slot(value).buffer);
		if (value instanceof ArrayBuffer) return copyBuffer(value);
		if (ArrayBuffer.isView(value)) {
			const source = new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
			const copy = new Uint8Array(source.length);
			copy.set(source);
			return copy.buffer;
		}
		return nativeBodyBuffer(String(value));
	}

	function normalizeHeaderName(name) {
		name = String(name).toLowerCase();
		if (!name || !/^[!#$%&'*+.^_`|~0-9a-z-]+$/.test(name)) {
			throw new TypeError("Invalid HTTP header name");
		}
		return name;
	}

	function normalizeHeaderValue(value) {
		value = String(value).replace(/^[\t\x20]+|[\t\x20]+$/g, "");
		if (/[\0\r\n]/.test(value)) throw new TypeError("Invalid HTTP header value");
		return value;
	}
	function sortedHeaderEntries(headers) {
		return Array.from(slot(headers).values.keys()).sort().map((name) => [name, headers.get(name)]);
	}

	class Headers {
		constructor(init) {
			slots.set(this, { values: new Map() });
			if (init == null) return;
			if (init instanceof Headers) {
				for (const [name, values] of slot(init).values) slot(this).values.set(name, values.slice());
				return;
			}
			if (typeof init[Symbol.iterator] === "function") {
				for (const pair of init) {
					if (pair == null || typeof pair[Symbol.iterator] !== "function") {
						throw new TypeError("Each header pair must be iterable");
					}
					const values = Array.from(pair);
					if (values.length !== 2) throw new TypeError("Each header pair must contain two values");
					this.append(values[0], values[1]);
				}
				return;
			}
			for (const name of Object.keys(Object(init))) this.append(name, init[name]);
		}

		append(name, value) {
			name = normalizeHeaderName(name);
			value = normalizeHeaderValue(value);
			const values = slot(this).values.get(name);
			if (values) values.push(value); else slot(this).values.set(name, [value]);
		}

		delete(name) { slot(this).values.delete(normalizeHeaderName(name)); }

		get(name) {
			const values = slot(this).values.get(normalizeHeaderName(name));
			return values ? values.join(", ") : null;
		}

		getSetCookie() {
			const values = slot(this).values.get("set-cookie");
			return values ? values.slice() : [];
		}

		has(name) { return slot(this).values.has(normalizeHeaderName(name)); }

		set(name, value) {
			slot(this).values.set(normalizeHeaderName(name), [normalizeHeaderValue(value)]);
		}

		entries() { return sortedHeaderEntries(this)[Symbol.iterator](); }
		keys() { return sortedHeaderEntries(this).map((entry) => entry[0])[Symbol.iterator](); }
		values() { return sortedHeaderEntries(this).map((entry) => entry[1])[Symbol.iterator](); }
		forEach(callback, thisArg) {
			if (typeof callback !== "function") throw new TypeError("callback must be a function");
			for (const [name, value] of sortedHeaderEntries(this)) callback.call(thisArg, value, name, this);
		}
		[Symbol.iterator]() { return this.entries(); }
	}
	Object.defineProperty(Headers.prototype, Symbol.toStringTag, { value: "Headers" });

	class Event {
		constructor(type, init) {
			if (arguments.length === 0) throw new TypeError("Event type is required");
			init = init || {};
			this.type = String(type);
			this.bubbles = Boolean(init.bubbles);
			this.cancelable = Boolean(init.cancelable);
			this.composed = Boolean(init.composed);
			this.target = null;
			this.currentTarget = null;
			this.eventPhase = 0;
			this.defaultPrevented = false;
			this.timeStamp = Date.now();
			this.isTrusted = false;
			slots.set(this, { stopped: false, immediateStopped: false });
		}
		preventDefault() { if (this.cancelable) this.defaultPrevented = true; }
		stopPropagation() { slot(this).stopped = true; }
		stopImmediatePropagation() { slot(this).stopped = slot(this).immediateStopped = true; }
		composedPath() { return this.target == null ? [] : [this.target]; }
	}
	Event.NONE = 0; Event.CAPTURING_PHASE = 1; Event.AT_TARGET = 2; Event.BUBBLING_PHASE = 3;
	Object.assign(Event.prototype, { NONE: 0, CAPTURING_PHASE: 1, AT_TARGET: 2, BUBBLING_PHASE: 3 });

	class ProgressEvent extends Event {
		constructor(type, init) {
			init = init || {};
			super(type, init);
			this.lengthComputable = Boolean(init.lengthComputable);
			this.loaded = Number(init.loaded || 0);
			this.total = Number(init.total || 0);
		}
	}

	class EventTarget {
		constructor() { slots.set(this, { listeners: new Map() }); }
		addEventListener(type, callback, options) {
			if (callback == null) return;
			type = String(type);
			const listeners = slot(this).listeners.get(type) || [];
			if (!listeners.some((listener) => listener.callback === callback)) {
				listeners.push({ callback, once: Boolean(options && typeof options === "object" && options.once) });
				slot(this).listeners.set(type, listeners);
			}
		}
		removeEventListener(type, callback) {
			const listeners = slot(this).listeners.get(String(type));
			if (!listeners) return;
			slot(this).listeners.set(String(type), listeners.filter((listener) => listener.callback !== callback));
		}
		dispatchEvent(event) {
			if (!(event instanceof Event)) throw new TypeError("event must be an Event");
			event.target = this;
			event.currentTarget = this;
			event.eventPhase = Event.AT_TARGET;
			const listeners = (slot(this).listeners.get(event.type) || []).slice();
			for (const listener of listeners) {
				if (slot(event).immediateStopped) break;
				if (listener.once) this.removeEventListener(event.type, listener.callback);
				if (typeof listener.callback === "function") listener.callback.call(this, event);
				else if (listener.callback && typeof listener.callback.handleEvent === "function") listener.callback.handleEvent(event);
			}
			event.currentTarget = null;
			event.eventPhase = Event.NONE;
			return !event.defaultPrevented;
		}
	}
	function defineEventHandler(target, type) {
		let value = null;
		const listener = (event) => { if (typeof value === "function") value.call(target, event); };
		Object.defineProperty(target, "on" + type, {
			configurable: true,
			enumerable: true,
			get() { return value; },
			set(next) {
				if (value != null) target.removeEventListener(type, listener);
				value = next;
				if (value != null) target.addEventListener(type, listener);
			}
		});
	}

	class DOMException extends Error {
		constructor(message, name) {
			super(message === undefined ? "" : String(message));
			this.name = name === undefined ? "Error" : String(name);
		}
		get code() {
			return { IndexSizeError: 1, HierarchyRequestError: 3, WrongDocumentError: 4,
				InvalidCharacterError: 5, NoModificationAllowedError: 7, NotFoundError: 8,
				NotSupportedError: 9, InUseAttributeError: 10, InvalidStateError: 11,
				SyntaxError: 12, InvalidModificationError: 13, NamespaceError: 14,
				InvalidAccessError: 15, TypeMismatchError: 17, SecurityError: 18,
				NetworkError: 19, AbortError: 20, URLMismatchError: 21,
				QuotaExceededError: 22, TimeoutError: 23, InvalidNodeTypeError: 24,
				DataCloneError: 25 }[this.name] || 0;
		}
	}

	class AbortSignal extends EventTarget {
		constructor() {
			super();
			this.aborted = false;
			this.reason = undefined;
			defineEventHandler(this, "abort");
			Object.assign(slot(this), { id: nativeNewAbortSignal() });
		}
		throwIfAborted() { if (this.aborted) throw this.reason; }
		static abort(reason) { const controller = new AbortController(); controller.abort(reason); return controller.signal; }
		static timeout(milliseconds) {
			milliseconds = Number(milliseconds);
			if (!Number.isFinite(milliseconds) || milliseconds < 0) throw new TypeError("Invalid timeout");
			const controller = new AbortController();
			setTimeout(() => controller.abort(new DOMException("The operation timed out", "TimeoutError")), milliseconds);
			return controller.signal;
		}
		static any(signals) {
			const controller = new AbortController();
			for (const signal of signals) {
				if (!(signal instanceof AbortSignal)) throw new TypeError("Expected AbortSignal");
				if (signal.aborted) { controller.abort(signal.reason); break; }
				signal.addEventListener("abort", () => controller.abort(signal.reason), { once: true });
			}
			return controller.signal;
		}
	}
	function abortSignal(signal, reason) {
		if (signal.aborted) return;
		signal.aborted = true;
		signal.reason = reason === undefined ? new DOMException("This operation was aborted", "AbortError") : reason;
		nativeAbortSignal(slot(signal).id);
		signal.dispatchEvent(new Event("abort"));
	}

	class AbortController {
		constructor() { this.signal = new AbortSignal(); }
		abort(reason) { abortSignal(this.signal, reason); }
	}

	class Blob {
		constructor(parts, options) {
			parts = parts === undefined ? [] : Array.from(parts);
			const buffers = parts.map(toBuffer);
			const total = buffers.reduce((size, buffer) => size + buffer.byteLength, 0);
			const bytes = new Uint8Array(total);
			let offset = 0;
			for (const buffer of buffers) { bytes.set(new Uint8Array(buffer), offset); offset += buffer.byteLength; }
			slots.set(this, { buffer: bytes.buffer });
			const type = options && options.type !== undefined ? String(options.type).toLowerCase() : "";
			this.type = /^[\x20-\x7e]*$/.test(type) ? type : "";
			this.size = total;
		}
		slice(start, end, type) {
			const size = this.size;
			start = start === undefined ? 0 : Number(start);
			end = end === undefined ? size : Number(end);
			start = start < 0 ? Math.max(size + start, 0) : Math.min(start, size);
			end = end < 0 ? Math.max(size + end, 0) : Math.min(end, size);
			return new Blob([new Uint8Array(slot(this).buffer, Math.min(start, end), Math.max(end - start, 0))], { type });
		}
		arrayBuffer() { return Promise.resolve(copyBuffer(slot(this).buffer)); }
		bytes() { return Promise.resolve(new Uint8Array(copyBuffer(slot(this).buffer))); }
		text() { return Promise.resolve(nativeBodyText(slot(this).buffer)); }
		stream() {
			const value = new Uint8Array(copyBuffer(slot(this).buffer));
			let consumed = false;
			return {
				getReader() { return { read() { if (consumed) return Promise.resolve({ done: true, value: undefined }); consumed = true; return Promise.resolve({ done: false, value }); }, cancel() { consumed = true; return Promise.resolve(); }, releaseLock() { throw new Error("Blob.stream().getReader().releaseLock is not supported by jsruntime"); } }; },
				[Symbol.asyncIterator]() { return { next() { if (consumed) return Promise.resolve({ done: true }); consumed = true; return Promise.resolve({ done: false, value }); } }; }
			};
		}
	}
	Object.defineProperty(Blob.prototype, Symbol.toStringTag, { value: "Blob" });

	class File extends Blob {
		constructor(parts, name, options) {
			super(parts, options);
			this.name = String(name);
			this.lastModified = options && options.lastModified !== undefined ? Number(options.lastModified) : Date.now();
			this.webkitRelativePath = "";
		}
	}
	Object.defineProperty(File.prototype, Symbol.toStringTag, { value: "File" });

	class FormData {
		constructor() { slots.set(this, { entries: [] }); }
		append(name, value, filename) { slot(this).entries.push([String(name), normalizeFormValue(value, filename)]); }
		delete(name) { name = String(name); slot(this).entries = slot(this).entries.filter((entry) => entry[0] !== name); }
		get(name) { name = String(name); const entry = slot(this).entries.find((item) => item[0] === name); return entry ? entry[1] : null; }
		getAll(name) { name = String(name); return slot(this).entries.filter((entry) => entry[0] === name).map((entry) => entry[1]); }
		has(name) { name = String(name); return slot(this).entries.some((entry) => entry[0] === name); }
		set(name, value, filename) {
			name = String(name);
			const normalized = normalizeFormValue(value, filename);
			const index = slot(this).entries.findIndex((entry) => entry[0] === name);
			this.delete(name);
			if (index < 0) slot(this).entries.push([name, normalized]); else slot(this).entries.splice(index, 0, [name, normalized]);
		}
		entries() { return slot(this).entries.slice()[Symbol.iterator](); }
		keys() { return slot(this).entries.map((entry) => entry[0])[Symbol.iterator](); }
		values() { return slot(this).entries.map((entry) => entry[1])[Symbol.iterator](); }
		forEach(callback, thisArg) { for (const [name, value] of slot(this).entries.slice()) callback.call(thisArg, value, name, this); }
		[Symbol.iterator]() { return this.entries(); }
	}
	Object.defineProperty(FormData.prototype, Symbol.toStringTag, { value: "FormData" });
	function normalizeFormValue(value, filename) {
		if (value instanceof Blob) {
			if (value instanceof File && filename === undefined) return value;
			return new File([value], filename === undefined ? "blob" : filename, { type: value.type });
		}
		return String(value);
	}
	function encodeFormData(form) {
		return nativeEncodeFormData(slot(form).entries.map(([name, value]) => value instanceof Blob ?
			{ name, value: slot(value).buffer, file: true, filename: value.name || "blob", type: value.type } :
			{ name, value, file: false }));
	}
	function formDataFrom(entries) {
		const form = new FormData();
		for (const entry of entries) {
			if (entry.file) form.append(entry.name, new File([entry.value], entry.filename, { type: entry.type }));
			else form.append(entry.name, entry.value);
		}
		return form;
	}

	class Body {
		constructor() { slots.set(this, {}); }
		get body() { return null; }
		get bodyUsed() { return slot(this).bodyUsed; }
		arrayBuffer() { return consumeBody(this, (buffer) => buffer); }
		blob() { return consumeBody(this, (buffer) => new Blob([buffer], { type: slot(this).contentType })); }
		bytes() { return consumeBody(this, (buffer) => new Uint8Array(buffer)); }
		text() { return consumeBody(this, (buffer) => nativeBodyText(buffer)); }
		json() { return consumeBody(this, (buffer) => JSON.parse(nativeBodyText(buffer))); }
		formData() { return consumeBody(this, (buffer) => formDataFrom(nativeParseFormData(buffer, slot(this).contentType))); }
	}
	function initBody(target, body, contentType) {
		Object.assign(slot(target), { bodyBuffer: body == null ? new ArrayBuffer(0) : toBuffer(body), bodyUsed: false, contentType: contentType || "" });
	}
	function consumeBody(target, transform) {
		if (slot(target).bodyUsed) return Promise.reject(new TypeError("Body has already been consumed"));
		slot(target).bodyUsed = true;
		try { return Promise.resolve(transform(copyBuffer(slot(target).bodyBuffer))); }
		catch (error) { return Promise.reject(error); }
	}

	const knownMethods = new Set(["DELETE", "GET", "HEAD", "OPTIONS", "POST", "PUT"]);
	function normalizeMethod(method) {
		method = String(method);
		const upper = method.toUpperCase();
		if (["CONNECT", "TRACE", "TRACK"].includes(upper)) throw new TypeError("Forbidden HTTP method");
		if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(method)) throw new TypeError("Invalid HTTP method");
		return knownMethods.has(upper) ? upper : method;
	}

	class Request extends Body {
		constructor(input, init) {
			super();
			init = init == null ? {} : Object(init);
			const source = input instanceof Request ? input : null;
			Object.assign(slot(this), {
				url: source ? source.url : String(input),
				method: normalizeMethod(init.method !== undefined ? init.method : source ? source.method : "GET"),
				headers: new Headers(init.headers !== undefined ? init.headers : source ? source.headers : undefined),
				signal: init.signal !== undefined ? init.signal : source ? source.signal : new AbortController().signal,
				cache: init.cache !== undefined ? String(init.cache) : source ? source.cache : "default",
				credentials: init.credentials !== undefined ? String(init.credentials) : source ? source.credentials : "same-origin",
				integrity: init.integrity !== undefined ? String(init.integrity) : source ? source.integrity : "",
				keepalive: init.keepalive !== undefined ? Boolean(init.keepalive) : source ? source.keepalive : false,
				mode: init.mode !== undefined ? String(init.mode) : source ? source.mode : "cors",
				redirect: init.redirect !== undefined ? String(init.redirect) : source ? source.redirect : "follow",
				referrer: init.referrer !== undefined ? String(init.referrer) : source ? source.referrer : "about:client",
				referrerPolicy: init.referrerPolicy !== undefined ? String(init.referrerPolicy) : source ? source.referrerPolicy : ""
			});
			if (!(slot(this).signal instanceof AbortSignal)) throw new TypeError("signal must be an AbortSignal");
			if (!["follow", "error", "manual"].includes(slot(this).redirect)) throw new TypeError("Invalid redirect mode");
			let body = init.body !== undefined ? init.body : source ? slot(source).bodyBuffer : null;
			if ((slot(this).method === "GET" || slot(this).method === "HEAD") && body != null && toBuffer(body).byteLength !== 0) {
				throw new TypeError("GET and HEAD requests cannot have a body");
			}
			if (body instanceof FormData) {
				const encoded = encodeFormData(body);
				body = encoded.body;
				if (!slot(this).headers.has("content-type")) slot(this).headers.set("content-type", encoded.contentType);
			} else if (body instanceof Blob && body.type && !slot(this).headers.has("content-type")) {
				slot(this).headers.set("content-type", body.type);
			} else if (typeof URLSearchParams !== "undefined" && body instanceof URLSearchParams && !slot(this).headers.has("content-type")) {
				slot(this).headers.set("content-type", "application/x-www-form-urlencoded;charset=UTF-8");
			} else if (typeof body === "string" && !slot(this).headers.has("content-type")) {
				slot(this).headers.set("content-type", "text/plain;charset=UTF-8");
			}
			initBody(this, body, slot(this).headers.get("content-type") || "");
		}
		get cache() { return slot(this).cache; }
		get credentials() { return slot(this).credentials; }
		get destination() { return ""; }
		get headers() { return slot(this).headers; }
		get integrity() { return slot(this).integrity; }
		get keepalive() { return slot(this).keepalive; }
		get method() { return slot(this).method; }
		get mode() { return slot(this).mode; }
		get redirect() { return slot(this).redirect; }
		get referrer() { return slot(this).referrer; }
		get referrerPolicy() { return slot(this).referrerPolicy; }
		get signal() { return slot(this).signal; }
		get url() { return slot(this).url; }
		get duplex() { return "half"; }
		clone() { if (this.bodyUsed) throw new TypeError("Body has already been consumed"); return new Request(this); }
	}
	Object.defineProperty(Request.prototype, Symbol.toStringTag, { value: "Request" });
	function nativeRequest(request) {
		return { method: request.method, url: request.url, headers: Array.from(request.headers), body: slot(request).bodyBuffer,
			signalId: slot(request.signal).id, redirect: request.redirect };
	}

	const responseInternal = Symbol("responseInternal");
	class Response extends Body {
		constructor(body, init) {
			super();
			init = init == null ? {} : Object(init);
			const internal = init[responseInternal] === true;
			const status = init.status === undefined ? 200 : Number(init.status);
			if (!internal && (!Number.isInteger(status) || status < 200 || status > 599)) throw new RangeError("Invalid response status");
			if (!internal && [101, 204, 205, 304].includes(status) && body != null) throw new TypeError("Response status cannot have a body");
			Object.assign(slot(this), {
				status,
				statusText: init.statusText === undefined ? "" : String(init.statusText),
				headers: new Headers(init.headers),
				url: init.url === undefined ? "" : String(init.url),
				redirected: Boolean(init.redirected),
				type: init.type === undefined ? "default" : String(init.type)
			});
			if (/[\r\n]/.test(slot(this).statusText)) throw new TypeError("Invalid response statusText");
			initBody(this, body, slot(this).headers.get("content-type") || "");
		}
		get headers() { return slot(this).headers; }
		get ok() { return this.status >= 200 && this.status <= 299; }
		get redirected() { return slot(this).redirected; }
		get status() { return slot(this).status; }
		get statusText() { return slot(this).statusText; }
		get type() { return slot(this).type; }
		get url() { return slot(this).url; }
		clone() {
			if (this.bodyUsed) throw new TypeError("Body has already been consumed");
			return new Response(slot(this).bodyBuffer, { [responseInternal]: true, status: this.status, statusText: this.statusText, headers: this.headers, url: this.url, redirected: this.redirected, type: this.type });
		}
		static error() { return new Response(null, { [responseInternal]: true, status: 0, type: "error" }); }
		static redirect(url, status) {
			status = status === undefined ? 302 : Number(status);
			if (![301, 302, 303, 307, 308].includes(status)) throw new RangeError("Invalid redirect status");
			return new Response(null, { status, headers: { location: String(url) } });
		}
		static json(data, init) {
			init = init == null ? {} : Object(init);
			const serialized = JSON.stringify(data);
			if (serialized === undefined) throw new TypeError("Value is not JSON serializable");
			const headers = new Headers(init.headers);
			if (!headers.has("content-type")) headers.set("content-type", "application/json");
			return new Response(serialized, Object.assign({}, init, { headers }));
		}
	}
	Object.defineProperty(Response.prototype, Symbol.toStringTag, { value: "Response" });

	function rawToResponse(raw) {
		return new Response(raw.body, { [responseInternal]: true, status: raw.status, statusText: raw.statusText,
			headers: raw.headers, url: raw.url, redirected: raw.redirected, type: "basic" });
	}

	function fetch(input, init) {
		let request;
		const source = input instanceof Request ? input : null;
		if (source && source.bodyUsed) return Promise.reject(new TypeError("Body has already been consumed"));
		try { request = new Request(input, init); }
		catch (error) { return Promise.reject(error); }
		if (request.signal.aborted) return Promise.reject(request.signal.reason);
		if (source && slot(source).bodyBuffer.byteLength !== 0) slot(source).bodyUsed = true;
		return nativeFetch(nativeRequest(request)).then(rawToResponse, (error) => {
			if (request.signal.aborted) throw request.signal.reason;
			throw error;
		});
	}

	Object.assign(globalThis, { Headers, Event, ProgressEvent, EventTarget, DOMException, AbortSignal, AbortController,
		Blob, File, FormData, Request, Response, fetch });
	Object.defineProperty(globalThis, "__komariXHRBridge", { configurable: true, value: {
		nativeFetchSync, rawToResponse, bodyText: nativeBodyText, nativeRequest, defineEventHandler,
		requestBodyLength: (request) => slot(request).bodyBuffer.byteLength
	} });
	delete globalThis.__komariBodyBuffer;
	delete globalThis.__komariBodyText;
	delete globalThis.__komariEncodeFormData;
	delete globalThis.__komariParseFormData;
	delete globalThis.__komariNewAbortSignal;
	delete globalThis.__komariAbortSignal;
	delete globalThis.__komariFetch;
	delete globalThis.__komariFetchSync;
})();
