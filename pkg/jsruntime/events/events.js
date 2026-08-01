(function () {
	"use strict";
	class EventEmitter {
		constructor() { Object.defineProperty(this, "_events", { value: new Map(), writable: true }); this._maxListeners = 10; }
		addListener(name, listener) { return this.on(name, listener); }
		on(name, listener) {
			if (typeof listener !== "function") throw new TypeError("listener must be a function");
			const list = this._events.get(name) || [];
			list.push(listener); this._events.set(name, list); return this;
		}
		once(name, listener) {
			const wrapper = (...args) => { this.removeListener(name, wrapper); return listener.apply(this, args); };
			wrapper.listener = listener; return this.on(name, wrapper);
		}
		prependListener(name, listener) {
			if (typeof listener !== "function") throw new TypeError("listener must be a function");
			const list = this._events.get(name) || []; list.unshift(listener); this._events.set(name, list); return this;
		}
		prependOnceListener(name, listener) {
			const wrapper = (...args) => { this.removeListener(name, wrapper); return listener.apply(this, args); };
			wrapper.listener = listener;
			const list = this._events.get(name) || []; list.unshift(wrapper); this._events.set(name, list); return this;
		}
		emit(name, ...args) {
			const list = (this._events.get(name) || []).slice();
			if (name === "error" && list.length === 0) throw (args[0] instanceof Error ? args[0] : new Error(String(args[0])));
			for (const listener of list) listener.apply(this, args);
			return list.length > 0;
		}
		removeListener(name, listener) {
			const list = this._events.get(name); if (!list) return this;
			for (let i = list.length - 1; i >= 0; i--) {
				if (list[i] === listener || list[i].listener === listener) { list.splice(i, 1); break; }
			}
			if (list.length === 0) this._events.delete(name); return this;
		}
		off(name, listener) { return this.removeListener(name, listener); }
		removeAllListeners(name) { if (arguments.length === 0) this._events.clear(); else this._events.delete(name); return this; }
		listeners(name) { return (this._events.get(name) || []).map((listener) => listener.listener || listener); }
		rawListeners(name) { return (this._events.get(name) || []).slice(); }
		listenerCount(name, listener) { const list = this.listeners(name); return listener === undefined ? list.length : list.filter((item) => item === listener).length; }
		eventNames() { return Array.from(this._events.keys()); }
		getMaxListeners() { return this._maxListeners; }
		setMaxListeners(value) { value = Number(value); if (!Number.isFinite(value) || value < 0) throw new RangeError("Invalid max listeners"); this._maxListeners = value; return this; }
		static listenerCount(emitter, name) { return emitter.listenerCount(name); }
		static getEventListeners(emitter, name) { return emitter.listeners(name); }
		static once(emitter, name) { return new Promise((resolve, reject) => { emitter.once(name, (...args) => resolve(args)); if (name !== "error") emitter.once("error", reject); }); }
		static on(emitter, name) {
			const queue = [], waiters = []; let done = false;
			const listener = (...args) => { const waiter = waiters.shift(); if (waiter) waiter({ value: args, done: false }); else queue.push(args); };
			emitter.on(name, listener);
			return { next() { if (queue.length) return Promise.resolve({ value: queue.shift(), done: false }); if (done) return Promise.resolve({ done: true }); return new Promise((resolve) => waiters.push(resolve)); }, return() { done = true; emitter.off(name, listener); for (const resolve of waiters.splice(0)) resolve({ done: true }); return Promise.resolve({ done: true }); }, [Symbol.asyncIterator]() { return this; } };
		}
	}
	return EventEmitter;
})()
