(function () {
	"use strict";
	const bridge = globalThis.__komariXHRBridge;
	const nativeFetchSync = bridge.nativeFetchSync;
	const bodyText = bridge.bodyText;
	const nativeRequest = bridge.nativeRequest;
	const requestBodyLength = bridge.requestBodyLength;
	const defineEventHandler = bridge.defineEventHandler;
	const slots = new WeakMap();
	const slot = (value) => slots.get(value);

	class XMLHttpRequestUpload extends EventTarget {
		constructor() {
			super();
			for (const name of ["loadstart", "progress", "abort", "error", "load", "timeout", "loadend"]) defineEventHandler(this, name);
		}
	}

	class XMLHttpRequest extends EventTarget {
		constructor() {
			super();
			slots.set(this, {
				readyState: XMLHttpRequest.UNSENT, method: "GET", url: "", async: true,
				requestHeaders: new Headers(), responseHeaders: null, responseBuffer: new ArrayBuffer(0),
				responseText: "", response: null, responseType: "", responseURL: "", status: 0,
				statusText: "", timeout: 0, overrideMimeType: "", sendFlag: false, controller: null,
				timeoutTimer: null, generation: 0
			});
			this.withCredentials = false;
			this.upload = new XMLHttpRequestUpload();
			for (const name of ["readystatechange", "loadstart", "progress", "abort", "error", "load", "timeout", "loadend"]) defineEventHandler(this, name);
		}

		get readyState() { return slot(this).readyState; }
		get status() { return slot(this).status; }
		get statusText() { return slot(this).statusText; }
		get responseURL() { return slot(this).responseURL; }
		get response() { return slot(this).response; }
		get responseXML() { return null; }
		get responseText() {
			if (slot(this).responseType !== "" && slot(this).responseType !== "text") {
				throw new DOMException("responseText is only available for text responses", "InvalidStateError");
			}
			return slot(this).responseText;
		}
		get responseType() { return slot(this).responseType; }
		set responseType(value) {
			value = String(value);
			if (!["", "text", "json", "arraybuffer", "blob", "document"].includes(value)) {
				throw new DOMException("Unsupported responseType", "SyntaxError");
			}
			if (slot(this).readyState === XMLHttpRequest.LOADING || slot(this).readyState === XMLHttpRequest.DONE) {
				throw new DOMException("Cannot change responseType while loading or done", "InvalidStateError");
			}
			if (!slot(this).async && value !== "") throw new DOMException("Synchronous responseType is not supported", "InvalidAccessError");
			slot(this).responseType = value;
		}
		get timeout() { return slot(this).timeout; }
		set timeout(value) {
			value = Number(value);
			if (!Number.isFinite(value) || value < 0) value = 0;
			if (!slot(this).async && value !== 0) throw new DOMException("Synchronous timeout is not supported", "InvalidAccessError");
			slot(this).timeout = Math.floor(value);
		}

		open(method, url, async, user, password) {
			if (arguments.length < 2) throw new TypeError("open requires method and url");
			method = String(method);
			const upper = method.toUpperCase();
			if (["CONNECT", "TRACE", "TRACK"].includes(upper)) throw new DOMException("Forbidden HTTP method", "SecurityError");
			if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(method)) throw new DOMException("Invalid HTTP method", "SyntaxError");
			slot(this).generation++;
			if (slot(this).controller) slot(this).controller.abort();
			slot(this).method = ["DELETE", "GET", "HEAD", "OPTIONS", "POST", "PUT"].includes(upper) ? upper : method;
			slot(this).url = String(url);
			slot(this).async = async === undefined ? true : Boolean(async);
			slot(this).requestHeaders = new Headers();
			slot(this).sendFlag = false;
			slot(this).controller = null;
			clearXHRTimeout(this);
			resetResponse(this);
			slot(this).readyState = XMLHttpRequest.OPENED;
			this.dispatchEvent(new Event("readystatechange"));
		}

		setRequestHeader(name, value) {
			if (slot(this).readyState !== XMLHttpRequest.OPENED || slot(this).sendFlag) {
				throw new DOMException("Request is not open for headers", "InvalidStateError");
			}
			slot(this).requestHeaders.append(name, value);
		}

		overrideMimeType(mime) {
			if (slot(this).readyState === XMLHttpRequest.LOADING || slot(this).readyState === XMLHttpRequest.DONE) {
				throw new DOMException("Response loading has started", "InvalidStateError");
			}
			slot(this).overrideMimeType = String(mime);
		}

		getResponseHeader(name) {
			if (slot(this).readyState < XMLHttpRequest.HEADERS_RECEIVED || !slot(this).responseHeaders) return null;
			return slot(this).responseHeaders.get(name);
		}

		getAllResponseHeaders() {
			if (slot(this).readyState < XMLHttpRequest.HEADERS_RECEIVED || !slot(this).responseHeaders) return "";
			let result = "";
			for (const [name, value] of slot(this).responseHeaders) result += name + ": " + value + "\r\n";
			return result;
		}

		send(body) {
			if (slot(this).readyState !== XMLHttpRequest.OPENED || slot(this).sendFlag) {
				throw new DOMException("Request is not open", "InvalidStateError");
			}
			if ((slot(this).method === "GET" || slot(this).method === "HEAD")) body = null;
			slot(this).sendFlag = true;
			resetResponse(this);
			slot(this).controller = new AbortController();
			const generation = ++slot(this).generation;
			const request = new Request(slot(this).url, { method: slot(this).method, headers: slot(this).requestHeaders, body, signal: slot(this).controller.signal });
			this.dispatchEvent(new ProgressEvent("loadstart"));
			this.upload.dispatchEvent(new ProgressEvent("loadstart"));
			const uploadSize = requestBodyLength(request);
			this.upload.dispatchEvent(new ProgressEvent("progress", { lengthComputable: true, loaded: uploadSize, total: uploadSize }));
			this.upload.dispatchEvent(new ProgressEvent("load", { lengthComputable: true, loaded: uploadSize, total: uploadSize }));
			this.upload.dispatchEvent(new ProgressEvent("loadend", { lengthComputable: true, loaded: uploadSize, total: uploadSize }));

			if (!slot(this).async) {
				try {
					const raw = nativeFetchSync(nativeRequest(request));
					acceptHeaders(this, raw);
					acceptBody(this, raw.body);
				} catch (error) {
					networkError(this, error);
				}
				return;
			}

			if (slot(this).timeout > 0) {
				slot(this).timeoutTimer = setTimeout(() => handleTimeout(this, generation), slot(this).timeout);
			}
			fetch(request).then((response) => {
				if (!isCurrent(this, generation)) return null;
				acceptHeaders(this, { status: response.status, statusText: response.statusText, url: response.url, headers: Array.from(response.headers) });
				return response.arrayBuffer();
			}).then((buffer) => {
				if (buffer == null || !isCurrent(this, generation)) return;
				acceptBody(this, buffer);
			}, (error) => {
				if (!isCurrent(this, generation)) return;
				networkError(this, error);
			});
		}

		abort() {
			const active = slot(this).sendFlag;
			slot(this).generation++;
			slot(this).sendFlag = false;
			if (slot(this).controller) slot(this).controller.abort();
			clearXHRTimeout(this);
			resetResponse(this);
			slot(this).readyState = XMLHttpRequest.UNSENT;
			if (active) {
				this.dispatchEvent(new Event("readystatechange"));
				this.dispatchEvent(new ProgressEvent("abort"));
				this.dispatchEvent(new ProgressEvent("loadend"));
			}
		}

	}

	function changeState(xhr, state) {
		slot(xhr).readyState = state;
		xhr.dispatchEvent(new Event("readystatechange"));
	}
	function resetResponse(xhr) {
		Object.assign(slot(xhr), { status: 0, statusText: "", responseURL: "", responseHeaders: null,
			responseBuffer: new ArrayBuffer(0), responseText: "", response: null });
	}
	function isCurrent(xhr, generation) {
		return slot(xhr).sendFlag && generation === slot(xhr).generation;
	}
	function acceptHeaders(xhr, raw) {
		slot(xhr).status = raw.status;
		slot(xhr).statusText = raw.statusText;
		slot(xhr).responseURL = raw.url || "";
		slot(xhr).responseHeaders = new Headers(raw.headers);
		changeState(xhr, XMLHttpRequest.HEADERS_RECEIVED);
	}
	function responseMimeType(xhr) {
		if (slot(xhr).overrideMimeType) return slot(xhr).overrideMimeType.split(";", 1)[0].trim().toLowerCase();
		return slot(xhr).responseHeaders ? slot(xhr).responseHeaders.get("content-type") || "" : "";
	}
	function acceptBody(xhr, buffer) {
		slot(xhr).responseBuffer = buffer.slice ? buffer.slice(0) : buffer;
		changeState(xhr, XMLHttpRequest.LOADING);
		const totalHeader = slot(xhr).responseHeaders && slot(xhr).responseHeaders.get("content-length");
		const total = totalHeader == null ? slot(xhr).responseBuffer.byteLength : Number(totalHeader);
		xhr.dispatchEvent(new ProgressEvent("progress", { lengthComputable: totalHeader != null, loaded: slot(xhr).responseBuffer.byteLength, total }));
		slot(xhr).responseText = bodyText(slot(xhr).responseBuffer);
		switch (slot(xhr).responseType) {
		case "arraybuffer": slot(xhr).response = slot(xhr).responseBuffer.slice(0); break;
		case "blob": slot(xhr).response = new Blob([slot(xhr).responseBuffer], { type: responseMimeType(xhr) }); break;
		case "json":
			try { slot(xhr).response = JSON.parse(slot(xhr).responseText); } catch (_) { slot(xhr).response = null; }
			break;
		case "document": slot(xhr).response = null; break;
		default: slot(xhr).response = slot(xhr).responseText;
		}
		slot(xhr).sendFlag = false;
		clearXHRTimeout(xhr);
		changeState(xhr, XMLHttpRequest.DONE);
		xhr.dispatchEvent(new ProgressEvent("load", { lengthComputable: totalHeader != null, loaded: slot(xhr).responseBuffer.byteLength, total }));
		xhr.dispatchEvent(new ProgressEvent("loadend", { lengthComputable: totalHeader != null, loaded: slot(xhr).responseBuffer.byteLength, total }));
	}
	function networkError(xhr) {
		slot(xhr).sendFlag = false;
		clearXHRTimeout(xhr);
		resetResponse(xhr);
		changeState(xhr, XMLHttpRequest.DONE);
		xhr.dispatchEvent(new ProgressEvent("error"));
		xhr.dispatchEvent(new ProgressEvent("loadend"));
	}
	function handleTimeout(xhr, generation) {
		if (!isCurrent(xhr, generation)) return;
		slot(xhr).generation++;
		slot(xhr).sendFlag = false;
		slot(xhr).controller.abort(new DOMException("The operation timed out", "TimeoutError"));
		clearXHRTimeout(xhr);
		resetResponse(xhr);
		changeState(xhr, XMLHttpRequest.DONE);
		xhr.dispatchEvent(new ProgressEvent("timeout"));
		xhr.dispatchEvent(new ProgressEvent("loadend"));
	}
	function clearXHRTimeout(xhr) {
		if (slot(xhr).timeoutTimer != null) clearTimeout(slot(xhr).timeoutTimer);
		slot(xhr).timeoutTimer = null;
	}

	for (const [name, value] of Object.entries({ UNSENT: 0, OPENED: 1, HEADERS_RECEIVED: 2, LOADING: 3, DONE: 4 })) {
		Object.defineProperty(XMLHttpRequest, name, { value, enumerable: true });
		Object.defineProperty(XMLHttpRequest.prototype, name, { value, enumerable: true });
	}
	Object.defineProperty(XMLHttpRequest.prototype, Symbol.toStringTag, { value: "XMLHttpRequest" });
	globalThis.XMLHttpRequest = XMLHttpRequest;
	globalThis.XMLHttpRequestEventTarget = EventTarget;
	globalThis.XMLHttpRequestUpload = XMLHttpRequestUpload;
	delete globalThis.__komariXHRBridge;
})();
