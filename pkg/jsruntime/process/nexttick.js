(function(nativeDrain) {
		let resolveCheckpoint;
		function prepare() {
			const checkpoint = new Promise((resolve) => { resolveCheckpoint = resolve; });
			checkpoint.then(nativeDrain);
		}
		function schedule() {
			const resolve = resolveCheckpoint;
			prepare();
			resolve();
		}
		prepare();
		return { schedule, run(job) { schedule(); return job(); } };
	})