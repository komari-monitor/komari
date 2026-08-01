(function (EventEmitter, fetch, AbortController, Blob) {
	"use strict";
	class Agent { constructor(options) { this.options = options || {}; this.keepAlive = Boolean(this.options.keepAlive); this.maxSockets = this.options.maxSockets || Infinity; } destroy() { throw new Error("http.Agent.destroy is not supported by jsruntime; no connection pool"); } }
	class ClientRequest extends EventEmitter {
		constructor(input, options, callback) {
			super(); this._chunks = []; this._controller = new AbortController(); this.destroyed = false; this.finished = false;
			if (typeof input === "object" && input !== null) { options = input; input = undefined; }
			options = options || {}; this.method = String(options.method || "GET").toUpperCase(); this._headers = new Headers(options.headers);
			this.url = input === undefined ? String(options.protocol || "http:") + "//" + String(options.hostname || options.host || "localhost") + (options.port ? ":" + options.port : "") + String(options.path || "/") : String(input);
			if (typeof callback === "function") this.once("response", callback);
		}
		setHeader(name, value) { this._headers.set(name, value); }
		getHeader(name) { return this._headers.get(name); }
		getHeaders() { return Object.fromEntries(this._headers); }
		getHeaderNames() { return Array.from(this._headers.keys()); }
		hasHeader(name) { return this._headers.has(name); }
		removeHeader(name) { this._headers.delete(name); }
		flushHeaders() { throw new Error("http.ClientRequest.flushHeaders is not supported by jsruntime; headers are buffered until end()"); }
		write(chunk, encoding, callback) { this._chunks.push(chunk); if (typeof encoding === "function") callback = encoding; if (callback) callback(); return true; }
		end(chunk, encoding, callback) {
			if (chunk !== undefined && chunk !== null && typeof chunk !== "function") this._chunks.push(chunk);
			if (typeof chunk === "function") callback = chunk; else if (typeof encoding === "function") callback = encoding;
			this.finished = true;
			const body = this._chunks.length ? new Blob(this._chunks) : undefined;
			fetch(this.url, { method: this.method, headers: this._headers, body, signal: this._controller.signal }).then(async (response) => {
				const incoming = new EventEmitter(); incoming.statusCode = response.status; incoming.statusMessage = response.statusText;
				incoming.headers = Object.fromEntries(response.headers); incoming.rawHeaders = Array.from(response.headers).flat(); incoming.httpVersion = "1.1";
				incoming.complete = true; incoming.aborted = false; incoming.setEncoding = () => { throw new Error("http.IncomingMessage.setEncoding is not supported by jsruntime; the body is fully buffered"); }; incoming.pause = () => { throw new Error("http.IncomingMessage.pause is not supported by jsruntime; the body is fully buffered"); }; incoming.resume = () => { throw new Error("http.IncomingMessage.resume is not supported by jsruntime; the body is fully buffered"); };
				this.emit("response", incoming); const bytes = new Uint8Array(await response.arrayBuffer());
				setTimeout(() => { if (bytes.length) incoming.emit("data", bytes); incoming.emit("end"); incoming.emit("close"); this.emit("close"); }, 0);
			}, (error) => { emitRequestError(this, error); this.emit("close"); });
			if (callback) callback(); return this;
		}
		abort() { this.destroyed = true; this._controller.abort(); this.emit("abort"); }
		destroy(error) { this.destroyed = true; this._controller.abort(error); if (error) emitRequestError(this, error); this.emit("close"); return this; }
		setTimeout(ms, callback) { if (callback) this.once("timeout", callback); setTimeout(() => this.emit("timeout"), ms); return this; }
		setNoDelay() { throw new Error("http.ClientRequest.setNoDelay is not supported by jsruntime"); } setSocketKeepAlive() { throw new Error("http.ClientRequest.setSocketKeepAlive is not supported by jsruntime"); }
	}
	// Unhandled "error" events are rethrown on the next turn so the host
	// error reporter logs them instead of losing them in the Promise machinery.
	function emitRequestError(request, error) {
		try { request.emit("error", error); }
		catch (unhandled) { setTimeout(() => { throw unhandled; }, 0); }
	}
	function request(input, options, callback) { if (typeof options === "function") { callback = options; options = {}; } return new ClientRequest(input, options, callback); }
	function get(input, options, callback) { const req = request(input, options, callback); req.end(); return req; }
	return { request, get, Agent, ClientRequest, globalAgent: new Agent(), IncomingMessage: EventEmitter, ServerResponse: EventEmitter };
})
