(function(fs) {
		const methods = ["readFile","writeFile","appendFile","access","stat","lstat","readdir","mkdir","rm","unlink","rmdir","rename","copyFile","realpath","readlink","symlink","truncate","chmod","utimes","mkdtemp","close","fstat","fsync"];
		const promises = {};
		for (const name of methods) promises[name] = (...args) => new Promise((resolve, reject) => fs[name](...args, (error, value) => error ? reject(error) : resolve(value)));
		promises.read = (...args) => new Promise((resolve, reject) => fs.read(...args, (error, bytesRead, buffer) => error ? reject(error) : resolve({ bytesRead, buffer })));
		promises.write = (...args) => new Promise((resolve, reject) => fs.write(...args, (error, bytesWritten, buffer) => error ? reject(error) : resolve({ bytesWritten, buffer })));
		const fileHandle = (fd) => ({
			fd,
			close: () => promises.close(fd),
			stat: () => promises.fstat(fd),
			sync: () => promises.fsync(fd),
			read: (...args) => promises.read(fd, ...args),
			write: (...args) => promises.write(fd, ...args)
		});
		promises.open = (...args) => new Promise((resolve, reject) => fs.open(...args, (error, fd) => error ? reject(error) : resolve(fileHandle(fd))));
		return promises;
	})