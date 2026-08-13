# jsruntime 接口与兼容性说明

`jsruntime` 是基于 [goja](https://github.com/dop251/goja) 和
`goja_nodejs` 构建的服务端 JavaScript 运行时。它提供 CommonJS、事件循环、
常用 Web API，以及一组受权限控制的 Node.js 兼容模块。

这不是浏览器，也不是完整的 Node.js。本文只描述当前源码实际提供的接口，
不能把“名称相同”理解为所有边界行为均与浏览器或 Node.js 一致。

## 状态定义

| 状态 | 含义 |
| --- | --- |
| `可用` | 主要行为已实现，可按本文列出的签名使用。 |
| `部分实现` | 常见用法可用，但参数、返回值、流式处理或边界语义不完整。 |
| `固定值/调用抛错` | 名称存在，但始终返回占位值；没有真实语义的方法调用会明确抛错。 |
| `未实现` | 当前运行时没有注入该接口，调用或 `require()` 会失败。 |

## Go 侧接口

### 快速开始

```go
package main

import (
	"io"
	"log"
	"time"

	jsruntime "github.com/komari-monitor/komari/pkg/jsruntime"
)

func main() {
	runtime, err := jsruntime.New(`
		async function run(name) {
			await new Promise((resolve) => setTimeout(resolve, 10));
			console.log("hello %s", name);
			return name === "komari";
		}
	`, jsruntime.Options{
		Timeout: 2 * time.Second,
		Console: io.Discard,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer runtime.Close()

	if err := runtime.Call("run", "komari"); err != nil {
		log.Fatal(err)
	}
}
```

`New()` 会立即执行顶层脚本。脚本不必定义 `sendMessage`；那只是上层消息发送器
的约定，不是 `jsruntime` 的要求。

### `Options`

| 字段 | 默认值 | 行为 |
| --- | --- | --- |
| `HTTPClient` | 带 `Timeout` 的新 `http.Client` | 被 `fetch`、XHR 和 `http` 客户端使用。传入的 client 会被浅复制后按请求设置重定向策略。 |
| `Timeout` | `30s` | 限制顶层脚本初始化、`Runtime.Call()`、异步请求和运行时回调。同步死循环可被中断。 |
| `Console` | Komari 应用日志 | 非空时，所有 console 输出写入该 `io.Writer`。 |
| `RequireLoader` | `require.DefaultSourceLoader` | 自定义 CommonJS 源码加载器。设置 `BaseDir` 且未允许越界时，传给 loader 的路径仍会先经过目录和软链接校验。 |
| `ConfigureRequire` | `nil` | 在脚本执行前向当前 runtime 的私有 registry 注册或覆盖原生模块。 |
| `ConfigureHost` | `nil` | 在标准模块注册后、`ConfigureRequire` 前调用，提供 `Host` 宿主服务句柄与私有 registry，供宿主注入模块（如插件的 `require("server")`）并从其它 goroutine 调度事件循环。 |
| `BaseDir` | 空 | 顶层相对 `require`、`node_modules` 和 Node `fs` 的根目录。非空时必须已存在且是目录。 |
| `NodeJS` | `false` | 注入 Node 全局变量并注册 `events/path/os/process/fs/child_process/net/http`。 |
| `AllowExec` | `false` | 允许 `child_process` 和 `process.kill()`。仅在 `NodeJS` 模式下有意义。 |
| `AllowListen` | `false` | 允许 `net.Server`、`http.Server` 绑定本地端口。出站连接不受此项限制。 |
| `AllowAllFileAccess` | `false` | 允许 `require` 和 `fs` 访问 `BaseDir` 外部路径。 |
| `ExtraRoots` | `nil` | 额外的受限制 `fs` 根目录（与 `BaseDir` 同级约束，多用于挂载只读资源目录）。每个目录必须已存在。`require` 仍只限于 `BaseDir`。 |
| `StorageDir` | 空 | 长期存储根目录（必须已存在）：与 `BaseDir` 一样被沙箱约束，并额外以绝对路径注入为 `__storageDir__` 全局变量（`NodeJS` 模式下）。更新代码目录不会触碰它。 |
| `MaxHTTPBodyBytes` | `32 MiB` | 限制 fetch 响应体和 HTTP server 请求体的缓冲大小。 |
| `MaxChildOutputBytes` | `1 MiB` | 分别限制 `exec`/`execFile` 及同步版本的 stdout 和 stderr。 |

### `Runtime` 方法

| 方法 | 状态 | 说明 |
| --- | --- | --- |
| `New(script, options)` | `可用` | 创建独立 VM、注入接口并执行脚本。空脚本、非法 `BaseDir` 或脚本错误会直接返回 error。 |
| `Call(name, args...)` | `可用` | 调用全局函数；支持同步返回值和 Promise。只有 truthy 返回值或解析为 truthy 的 Promise 才算成功，不向 Go 返回 JS 值。 |
| `CallVoid(name, args...)` | `可用` | 调用副作用函数（如插件 `load()`/`unload()` 钩子），报告错误但不要求 truthy 返回值。 |
| `HasFunction(name)` | `可用` | 检查全局属性是否为函数；runtime 关闭后返回 `false`。 |
| `Close()` | `可用` | 幂等关闭；停止事件循环，并关闭 timer、fetch、文件、socket、listener 和子进程等已登记资源。 |

同一 runtime 的公开操作会串行执行，因为 goja VM 不是 goroutine-safe。多个独立
runtime 可以由调用方并行使用。

## `BaseDir`、`require` 与权限边界

### 相对模块从哪里查找

假设目录如下：

```text
plugin/
  a.js
  nested/b.js
  node_modules/example/index.js
```

```go
runtime, err := jsruntime.New(`
	function run() {
		return require("./a.js").ok;
	}
`, jsruntime.Options{BaseDir: `C:\plugins\plugin`})
```

- 顶层脚本的虚拟文件名是 `<BaseDir>/script.js`，所以顶层
  `require("./a.js")` 查找 `<BaseDir>/a.js`。
- `a.js` 内部的 `require("./nested/b.js")` 相对于 `a.js` 所在目录解析。
- `require("example")` 使用 CommonJS 原生模块和 `node_modules` 规则；配置
  `BaseDir` 时会包含 `<BaseDir>/node_modules`。
- 文件解析依次支持原路径、`.js`、`.json`；目录支持 `package.json` 的 `main`，
  然后回退到 `index.js`、`index.json`。
- `require` 会缓存已加载模块。
- `BaseDir` 可以在 `NodeJS: false` 时单独使用。
- `NodeJS: true` 且未传 `BaseDir` 时，进程当前工作目录会成为隐式根目录，并启用越界校验。
- `NodeJS: false` 且未传 `BaseDir` 时，仍有 `require`，但默认 loader 没有目录沙盒；
  对不可信脚本应始终显式设置 `BaseDir`。
- 全局 `require` 只实现模块加载函数本身；`require.resolve/cache/extensions/main` 未暴露。

默认情况下，路径穿越和指向目录外的软链接都会被拒绝。删除软链接时只删除链接
本身；`lstat`、`readlink`、`unlink`、`rm` 等不会为了删除而跟随最终链接。
`AllowAllFileAccess: true` 会关闭这层目录限制，但相对路径仍从 runtime 的当前目录解析。

`ExtraRoots` 与 `StorageDir` 会在 `BaseDir` 之外追加受约束的 `fs` 根目录：目录内的
读写被放行，越出任一目录仍被拒绝，且不同根目录之间不可通过 `..` 或软链接互访
（跨根目录的 `renameSync` 会被拒绝）。`StorageDir` 额外以绝对路径注入为
`__storageDir__` 全局变量（`NodeJS` 模式）。

### 自定义 registry 与 `BaseDir` 组合

`ConfigureRequire` 在内置模块注册之后执行，因此可以新增模块，也可以按名称覆盖模块。
它不会替换负责 `BaseDir` 的 registry，所以文件模块与自定义原生模块可以同时工作。

```go
import (
	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
	jsruntime "github.com/komari-monitor/komari/pkg/jsruntime"
)

runtime, err := jsruntime.New(`
	const local = require("./a.js");
	const meta = require("plugin:meta");
	function run() {
		return local.ok && meta.version === "1";
	}
`, jsruntime.Options{
	BaseDir: pluginDir,
	ConfigureRequire: func(registry *require.Registry) {
		loader := func(vm *goja.Runtime, module *goja.Object) {
			exports := module.Get("exports").ToObject(vm)
			_ = exports.Set("version", "1")
		}
		registry.RegisterNativeModule("plugin:meta", loader)
	},
})
```

如需覆盖同时支持两种写法的 Node 模块，应分别注册普通名称和 `node:` 名称：

```go
registry.RegisterNativeModule("path", loader)
registry.RegisterNativeModule("node:path", loader)
```

自定义 `RequireLoader` 也可以与 `BaseDir`、`ConfigureRequire` 同时设置。未开启
`AllowAllFileAccess` 时，loader 收到的是完成越界和软链接校验后的绝对路径。

## 总体可用性

### 始终注入

| 接口 | 状态 | 说明 |
| --- | --- | --- |
| ECMAScript 内建对象 | `可用` | 由 goja 提供。已覆盖 Promise、async/await、Array、Map/Set、typed array、JSON、`eval()` 等常见能力。 |
| `require()` | `可用` | CommonJS 文件加载和缓存始终存在，不依赖 `NodeJS`。不支持 ESM `import`/`export` loader。 |
| `console` | `部分实现` | 8 个常用输出方法和基础格式化。 |
| timers | `部分实现` | timeout、interval、immediate 及 clear。 |
| Fetch API | `部分实现` | `fetch` 及常用请求、响应、body、表单和取消对象。全部 body 都在内存中缓冲。 |
| XHR | `部分实现` | 异步与同步请求、状态、响应头、超时、取消和常用响应类型。 |
| `buffer`、`url`、`util` 模块 | `部分实现` | 来自固定版本的 `goja_nodejs`。 |

### `NodeJS: true` 时额外注入

全局变量：`Buffer`、`process`、`global`、`__dirname`、`__filename`。

模块：`events`、`path`、`os`、`process`、`fs`、`child_process`、`net`、`http`、`stream`（含 `stream/promises`）、`crypto`。
这些模块同时支持 `require("name")` 和 `require("node:name")`。

`__filename` 是合成的 `<runtime cwd>/script.js`，顶层脚本本身不是 CommonJS wrapper，
所以顶层不注入 `module` 和 `exports`。

## ECMAScript 与事件循环

`Array.prototype.map/filter/reduce`、`JSON.parse/stringify` 和 `eval()` 由 goja 原生提供，
不是 jsruntime 中的空壳。`eval()` 中的同步死循环仍受 `Timeout` 中断。

| 接口 | 状态 | 限制 |
| --- | --- | --- |
| `Promise`、async/await | `可用` | Promise job 在事件循环中执行。 |
| `queueMicrotask(callback)` | `可用` | 使用 `Promise.resolve().then(callback)` 实现；非函数会抛 `TypeError`。 |
| `setTimeout/clearTimeout` | `可用` | 支持附加 callback 参数。 |
| `setInterval/clearInterval` | `可用` | callback 因执行超时失败时会自动清理 interval。 |
| `setImmediate/clearImmediate` | `部分实现` | 以 0 延迟 event-loop timer 实现，没有完整 Node.js phase 语义。 |
| timer handle 的 `ref/unref/refresh/hasRef` | `未实现` | 返回的 handle 只保证可传给对应 clear 方法。 |
| `process.nextTick` | `可用` | 仅 Node 模式；在已测试的 turn 中先于 Promise microtask 执行。 |

```js
function verifyOrder() {
  const order = [];
  return new Promise((resolve) => {
    setTimeout(() => {
      order.push("timer");
      resolve(order.join(",") === "sync,microtask,timer");
    }, 0);
    queueMicrotask(() => order.push("microtask"));
    order.push("sync");
  });
}
```

## `console`

可用方法：

```text
assert  debug  error  exception  info  log  trace  warn
```

- `exception()` 是 `error()` 的别名。
- `assert(false, ...)` 和 `trace()` 会附加调用栈。
- 支持基础 `%s`、`%d`、`%i`、`%f`、`%o`、`%O`、`%c`、`%%`。
- `%c` 只消费样式参数，不应用样式；对象格式化不是浏览器的可交互 inspector。
- `table`、`dir`、`time/timeEnd`、`count`、`group`、`clear`、`profile` 等未实现。

```js
console.info("job=%s count=%d", "cleanup", 3);
console.assert(value > 0, "value must be positive");
```

## Fetch API

### 可用接口

| 对象 | 可用成员 | 状态 |
| --- | --- | --- |
| `Headers` | constructor、`append/delete/get/getSetCookie/has/set`、`entries/keys/values/forEach`、iterator | `可用` |
| `EventTarget` | `addEventListener/removeEventListener/dispatchEvent`，支持 listener object 和 `{ once: true }` | `部分实现` |
| `Event` | 常用属性、`preventDefault/stopPropagation/stopImmediatePropagation/composedPath`、phase 常量 | `部分实现` |
| `ProgressEvent` | `lengthComputable/loaded/total` | `可用` |
| `DOMException` | `name/message/code` | `部分实现` |
| `AbortController` | `signal`、`abort(reason)` | `可用` |
| `AbortSignal` | `aborted/reason/onabort`、`throwIfAborted()`、静态 `abort/timeout/any` | `可用` |
| `Blob` | constructor、`size/type`、`slice/arrayBuffer/bytes/text/stream` | `部分实现` |
| `File` | Blob 能力及 `name/lastModified/webkitRelativePath` | `部分实现` |
| `FormData` | `append/delete/get/getAll/has/set`、iterator、`entries/keys/values/forEach` | `可用` |
| Body mixin | `bodyUsed`、`arrayBuffer/blob/bytes/text/json/formData` | `部分实现` |
| `Request` | 常见 init 属性、headers/body、signal、redirect、`clone()` | `部分实现` |
| `Response` | status/headers/body、`ok`、`clone()`、静态 `error/redirect/json` | `部分实现` |
| `fetch(input, init)` | HTTP/HTTPS、header、buffered body、redirect、abort | `部分实现` |

### 与浏览器的主要差异

- 所有请求和响应 body 都完整缓冲，不提供真正的 `ReadableStream`。`Blob.stream()`
  只是一次性读取器；Body 的 `body` 属性固定为 `null`。
- body 默认上限为 32 MiB，由 `MaxHTTPBodyBytes` 调整。
- `Request.cache/credentials/integrity/keepalive/mode/referrer/referrerPolicy` 会保存为属性，
  但不执行浏览器缓存、Cookie、CORS、integrity 或 referrer policy。
- `Request.destination` 固定为空字符串，`duplex` 固定为 `"half"`。
- `File.webkitRelativePath` 固定为空字符串。
- 只实现 `follow`、`manual`、`error` 三种 redirect 行为；没有浏览器 opaque response。
- 使用 Go `http.Client` 和系统网络栈，不模拟浏览器同源策略或 Cookie jar。
- EventTarget 只有单目标分发，没有 DOM 树上的 capture/bubble/composed path。

```js
async function postJSON(url) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 2000);
  try {
    const response = await fetch(url, {
      method: "POST",
      headers: { "X-Plugin": "example" },
      body: JSON.stringify({ ok: true }),
      signal: controller.signal,
    });
    const data = await response.json();
    return response.ok && data.accepted === true;
  } finally {
    clearTimeout(timer);
  }
}
```

## `XMLHttpRequest`

### 可用成员

| 类别 | 接口 | 状态 |
| --- | --- | --- |
| 状态 | `UNSENT/OPENED/HEADERS_RECEIVED/LOADING/DONE`，实例和构造器均可访问 | `可用` |
| 属性 | `readyState/status/statusText/responseURL/response/responseText/responseType/timeout/withCredentials/upload` | `部分实现` |
| 方法 | `open/setRequestHeader/overrideMimeType/getResponseHeader/getAllResponseHeaders/send/abort` | `部分实现` |
| 事件 | `readystatechange/loadstart/progress/abort/error/load/timeout/loadend` 和对应 `on*` | `可用` |
| upload 事件 | `loadstart/progress/abort/error/load/timeout/loadend` | `部分实现` |
| responseType | `""`、`text`、`json`、`arraybuffer`、`blob`、`document` | `部分实现` |

主要差异：

- `responseXML` 固定为 `null`；`responseType = "document"` 的 `response` 也固定为 `null`。
- `withCredentials` 只是普通属性，不管理 Cookie 或认证状态。
- `open()` 的 `user`、`password` 参数未使用。
- upload 和 download 都是完整缓冲后报告，不提供分块进度。
- 同步模式 `open(..., false)` 可用，但会同步阻塞当前 JavaScript 事件循环；同步模式不允许
  非零 timeout 或非空 responseType。
- 没有浏览器 CORS、mixed-content、Cookie 和 DOM 集成。

```js
function loadText(url) {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    xhr.open("GET", url);
    xhr.timeout = 2000;
    xhr.onload = () => resolve(xhr.status === 200 && xhr.responseText.length > 0);
    xhr.onerror = () => resolve(false);
    xhr.ontimeout = () => resolve(false);
    xhr.send();
  });
}
```

## 始终可 require 的兼容模块

这些模块来自当前固定版本的 `goja_nodejs`，即使 `NodeJS: false` 也可 require。

### `buffer`

| 接口 | 状态 |
| --- | --- |
| `require("buffer").Buffer` | `部分实现` |
| `Buffer.from()`、`Buffer.alloc()`、`Buffer.poolSize` | `可用` |
| Uint8Array 继承能力、`equals/toString/write` | `可用` |
| BigInt、float、double、有符号和无符号整数的常见 BE/LE read/write 方法 | `可用` |
| `Buffer.concat/isBuffer/byteLength/compare/allocUnsafe/allocUnsafeSlow/transcode` | `未实现` |

全局 `Buffer` 只在 `NodeJS: true` 时注入；其他模式使用：

```js
const { Buffer } = require("buffer");
const value = Buffer.from("komari").toString("hex");
```

### `url`

| 接口 | 状态 |
| --- | --- |
| `URL` | `部分实现`：`host/hash/hostname/href/pathname/origin/password/username/port/protocol/search/searchParams`、`toString/toJSON` |
| `URLSearchParams` | `可用`：`append/delete/entries/forEach/get/getAll/has/keys/set/sort/size/toString/values` 和 iterator |
| `domainToASCII/domainToUnicode` | `可用` |
| 旧式 `parse/format/resolve/resolveObject/urlToHttpOptions` | `未实现` |

`URL` 和 `URLSearchParams` 不会自动成为全局变量，应从模块取得：

```js
const { URL, URLSearchParams } = require("node:url");
const url = new URL("/api", "https://example.com");
url.search = new URLSearchParams({ page: "1" }).toString();
```

### `util`

只有 `util.format()` 可用，支持 `%s`、`%d`、`%j` 和 `%%` 的基础格式化。
`inspect`、`promisify`、`callbackify`、`types`、`TextEncoder/TextDecoder` 等未实现。

## Node.js 兼容模块

以下章节仅适用于 `NodeJS: true`。

### `events`

`EventEmitter` 支持：

```text
addListener  on  once  prependListener  prependOnceListener
emit  removeListener  off  removeAllListeners
listeners  rawListeners  listenerCount  eventNames
getMaxListeners  setMaxListeners
```

静态方法：`EventEmitter.listenerCount/getEventListeners/once/on`。`once()` 返回 Promise，
`on()` 返回基础 async iterator。

`defaultMaxListeners` 存在，但 max listeners 只保存数值，不发 warning，也不阻止新增 listener。
`captureRejections`、`errorMonitor`、`addAbortListener`、`EventEmitterAsyncResource` 及完整
EventTarget 互操作未实现。

```js
const EventEmitter = require("events");
const events = new EventEmitter();
events.once("ready", (value) => console.log(value));
events.emit("ready", "ok");
```

### `stream`

提供 Node.js 风格的流抽象：`Readable`、`Writable`、`Duplex`、`Transform`、
`PassThrough`、`Stream`，以及 `pipeline`、`finished`、`addAbortSignal`、
`isErrored`、`isReadable`、`getDefaultHighWaterMark`。模块同时注册为
`stream` 与 `node:stream`；Promise 版在 `stream/promises` 与 `node:stream/promises`，
也通过 `require("stream").promises` 暴露。

#### Readable

```js
const { Readable } = require("stream");
const source = new Readable({
  read() {
    this.push("komari");
    this.push(null); // 结束
  },
});
source.on("data", (chunk) => console.log(chunk.toString()));
source.on("end", () => console.log("end"));
```

支持的成员：`push`、`unshift`、`read(size)`、`pause/resume/isPaused`、
`pipe/unpipe`、`setEncoding`、`destroy`、`on/once/removeListener` 的
`data/readable/end/close/error` 事件、`[Symbol.asyncIterator]`、
静态 `Readable.from(iterable, options)` 与 `Readable.isDisturbed(stream)`，
属性 `readableFlowing/readableEnded/readableLength/readableHighWaterMark/
readableObjectMode/readableEncoding/readableAborted/readableDidRead/errored/destroyed`。

- `Readable.from` 默认 `objectMode: true`，字符串作为单个 chunk 输出。
- 可消费同步或异步 iterable；异步生产者在 `_read` 返回后通过 `push` 继续。
- `read(size)` 在非 objectMode 下会拼接多个内部 chunk 凑足字节数，多余部分放回缓冲。
- async iterator 返回带 `next()/return()` 的标准对象；注意当前 goja 版本不支持
  `for await...of` 与 async generator 语法，脚本里需手动循环调用 `next()`。

#### Writable

```js
const { Writable } = require("stream");
const dest = new Writable({
  write(chunk, encoding, callback) {
    console.log(chunk.toString());
    callback(); // 完成后必须调用，否则背压不会继续
  },
});
dest.write("a");
dest.end("b");
dest.on("finish", () => console.log("done"));
```

支持的成员：`write`、`end`、`cork/uncork`、`setDefaultEncoding`、`destroy`、
`_write`、`_final`，事件 `drain/finish/close/error`，属性
`writableEnded/writableFinished/writableLength/writableHighWaterMark/
writableObjectMode/writableCorked/writableNeedDrain/errored/destroyed`。

- `write()` 在内部缓冲达到 `highWaterMark` 时返回 `false` 并置 `writableNeedDrain`，
  完成后发出 `drain`；`pipe` 会据此暂停/恢复源。
- `cork()` 期间写入暂存，`uncork()` 后统一下发。
- `end()` 会先写完剩余数据，再调用 `_final`（Transform 里即 `_flush`），最后发 `finish`。

#### Duplex / Transform / PassThrough

```js
const { Transform } = require("stream");
const upper = new Transform({
  transform(chunk, encoding, callback) {
    callback(null, chunk.toString().toUpperCase());
  },
});
```

- `Duplex` 同时具备 readable 与 writable 两侧；`allowHalfOpen` 控制一侧结束后是否
  自动结束另一侧（Transform 默认 `false`，与 Node 一致）。
- `Transform` 支持 `_transform(chunk, encoding, callback)` 与 `_flush(callback)`；
  `_flush` 结束前可 `push` 最后一批数据。
- `PassThrough` 原样转发。

#### pipeline 与 finished

```js
const { Readable, Writable, pipeline, finished } = require("stream");

// callback 形式
pipeline(Readable.from(["a", "b"]), new Writable({ write(c, e, cb) { cb(); } }), (error) => {
  if (error) console.error(error);
});
// Promise 形式（不传 callback）
await pipeline(Readable.from(["a", "b"]), new Writable({ write(c, e, cb) { cb(); } }));

await finished(readable, { cleanup: true }); // 等 readable 结束
```

- `pipeline(source, [transforms...], destination, callback?)` 支持多个参数或数组；
  第一个参数可以是 stream、iterable，或返回 iterable 的函数。不传 callback 时返回
  Promise。发生错误时会销毁链上所有 stream 并 reject/回调错误；目标提前关闭视为
  `premature close`。与 Node 一致，单个 stream 单独调用 `pipeline(stream)` 会报错。
- `finished(stream, options?, callback?)` 监听 `error/end/finish/close`；
  不传 callback 时返回 Promise（Node 的 callback 版本则返回清理函数）。
  `cleanup: true` 会在结束后移除监听器；`signal` 接受 AbortSignal。
- `addAbortSignal(signal, stream)` 在信号 abort 时销毁 stream。

#### 与 Node.js 的主要差异

- 事件循环由 goja 驱动，内部调度使用 `process.nextTick`；数据流是异步分发的。
- 没有 Web Streams：`stream/web`、`ReadableStream/WritableStream/TransformStream`、
  `Readable.toWeb/fromWeb`、`Writable.toWeb/fromWeb`、`Duplex.fromWeb/toWeb`、
  `compose`、`duplexPair` 未实现。
- `highWaterMark` 是阈值而非硬限制；objectMode 下按对象个数计算。
- 错误是普通 `Error/TypeError`，不带 Node 的 `ERR_STREAM_*` 错误码。
- `pipeline` 的 `{ signal }` 选项参数未实现；用 `addAbortSignal` 或自行监听。
- `fs.createReadStream/createWriteStream`、`process.stdout/stderr/stdin`、
  `child_process` 的 `stdin/stdout/stderr`、`http` 的 `IncomingMessage`（Readable）
  与 `ServerResponse`（Writable）都已接入真实 stream 实例，可配合 `pipe`、
  `pipeline`、`finished` 使用。`net.Socket` 仍是独立实现，未接入 stream 模块。

### `fs`

所有相对路径从 `process.cwd()` 解析。默认 cwd 是 `BaseDir`；`process.chdir()` 只修改当前
runtime 的 fs cwd，不调用宿主进程的 `os.Chdir()`。

#### 同步接口

```text
readFileSync  writeFileSync  appendFileSync  existsSync  accessSync
statSync  lstatSync  readdirSync  mkdirSync  rmSync  unlinkSync  rmdirSync
renameSync  copyFileSync  realpathSync  readlinkSync  symlinkSync
truncateSync  chmodSync  utimesSync  mkdtempSync
openSync  closeSync  fstatSync  fsyncSync  readSync  writeSync
```

这些方法会阻塞 JavaScript 事件循环。

#### callback 接口

```text
readFile  writeFile  appendFile  access  stat  lstat  readdir  mkdir  rm
unlink  rmdir  rename  copyFile  realpath  readlink  symlink  truncate
chmod  utimes  mkdtemp  open  close  fstat  fsync  read  write  exists
```

除同步版本外，文件系统工作在 goroutine 中执行，完成后回到事件循环调用 callback。
callback 风格采用 `(error, value...)`；`exists(path, callback)` 按 Node 旧接口只回调 boolean。

#### Promise 接口

`fs.promises` 提供 callback 列表中除 `exists` 外的同名方法。`open()` 返回简化 FileHandle：

```text
fd  close()  stat()  sync()  read()  write()
```

`read()` 解析为 `{ bytesRead, buffer }`，`write()` 解析为
`{ bytesWritten, buffer }`。

#### 流式接口

```text
createReadStream(path[, options])  createWriteStream(path[, options])
```

返回真实的 `stream.Readable` / `stream.Writable` 实例，支持 `data/end/finish/
close/error/open` 事件、`pipe()`、`pipeline()`、`finished()` 和背压。

- `createReadStream` 支持 `highWaterMark`、`encoding`、`start`、`end`（含端）和
  `autoClose`；每次读取都会按 `position` 定位，未消费时不会继续读入。
- `createWriteStream` 支持 `flags`（默认 `"w"`）、`mode`、`highWaterMark`、
  `autoClose`；写入按顺序串行落盘，`end()` 后先关闭 fd 再发 `finish`。
- 文件打开/读写基于 callback 版 `fs`，在 goroutine 中执行；错误会通过 `error`
  事件发出并销毁 stream。

#### 对象和常量

- `Stats` 提供 size/mode/time 字段以及 `isFile/isDirectory/isSymbolicLink/
  isBlockDevice/isCharacterDevice/isFIFO/isSocket`。
- `Dirent` 提供 name 和相同类型判断方法。
- `fs.constants` 只有 `F_OK/R_OK/W_OK/X_OK/COPYFILE_EXCL`。
- `access/accessSync` 会实际处理 `F_OK/R_OK/W_OK/X_OK`。
- 错误对象尽量提供 `name/code/errno/syscall/path/dest`。

#### 部分实现和缺失项

- `Stats.dev/ino/uid/gid/rdev` 固定为 0，`nlink` 固定为 1，`blksize` 固定为 4096；
  `atime/ctime/birthtime` 当前都使用 `ModTime`。
- `Dirent.parentPath` 和 `Dirent.path` 固定为空字符串。
- `readdir` 只处理 `withFileTypes`，不支持 recursive 等较新的选项。
- open flag 只明确支持 `r/r+/w/wx/w+/a/ax/a+`；未知 flag 会退化为只读。
- `readFile`/`readFileSync` 支持常用 encoding 返回字符串；`writeFile`/`appendFile`
  当前主要处理 `mode`，不完整支持 Node 的 `encoding/flag/flush` options。
- `copyFile` 暂不执行 `COPYFILE_EXCL` mode；`symlink` 不处理 Windows type 参数。
- `mkdir({recursive:true})` 不返回首个创建目录；部分 option/encoding 边界与 Node 不同。
- `watch/watchFile/unwatchFile/opendir/cp/link/statfs/lutimes`
  及完整 FileHandle API 未实现。

```js
async function updateState() {
  const fs = require("node:fs");
  await fs.promises.mkdir("data", { recursive: true });
  await fs.promises.writeFile("data/state.json", JSON.stringify({ ready: true }));
  const text = await fs.promises.readFile("data/state.json", "utf8");
  return JSON.parse(text).ready === true;
}
```

### `path`

可用接口：

```text
sep  delimiter  normalize  isAbsolute  join  resolve  relative
dirname  basename  extname  parse  format  toNamespacedPath
posix  win32
```

默认实现跟随宿主 OS，`path.posix` 和 `path.win32` 可显式选择。常用跨平台和边界行为已有
测试，但这是 Go 自实现兼容层：参数通常会被转成字符串，而 Node 在一些位置会抛
`TypeError`，罕见设备路径或边界输入也可能不同。

```js
const path = require("path");
const file = path.join("data", "result.json");
const portable = path.posix.join("plugins", "example", "index.js");
```

### `os`

| 接口 | 状态 |
| --- | --- |
| `arch/platform/type/release/version/machine` | `部分实现` |
| `hostname/homedir/tmpdir/endianness` | `可用` |
| `EOL/devNull` | `可用` |
| `uptime/loadavg/totalmem/freemem/availableParallelism/cpus` | `部分实现` |
| `userInfo/networkInterfaces` | `部分实现` |
| `constants` | `部分实现` |

系统指标读取宿主机，不是单个插件或 runtime 的资源指标。Windows 上 `loadavg()` 返回
零值；Linux 和 Windows 实现了系统指标，其他 GOOS 上依赖指标的方法会抛出“不支持”错误。

`userInfo().uid/gid` 来自 `os/user`，当前为字符串而不是 Node 常见的 number；`shell`
固定为空字符串。`constants.errno` 是空对象；`constants.signals` 只有
`SIGINT/SIGTERM/SIGKILL`。`networkInterfaces()` 没有完整 Node 字段和所有平台细节。

```js
const os = require("os");
console.log(os.platform(), os.arch(), os.totalmem());
```

### `process`

#### 可用属性和方法

```text
env  argv  execArgv  execPath  pid  ppid  platform  arch
version  versions  release  title  exitCode  connected  config
cwd()  chdir()  uptime()
memoryUsage()  memoryUsage.rss()  cpuUsage()  resourceUsage()
hrtime()  hrtime.bigint()  nextTick()  emitWarning()
kill()  exit()  abort()
stdout  stderr  stdin
```

重要差异：

- `process.env` 是构造 runtime 时的宿主环境快照；修改 JS 对象不会修改宿主环境。
- `argv` 是合成值；`execPath/pid/ppid` 指向当前 Komari/Go 宿主进程。
- `version` 来自 Go 版本，`versions.node` 固定为 `"0.0.0-goja"`；`release`、`title`、
  `connected` 和 `config` 也不是完整 Node 值。
- `process.uptime()` 是当前 JavaScript runtime 的存活时间；`os.uptime()` 才是宿主机 uptime。
- `memoryUsage()` 混合 Go runtime heap 和整个宿主进程 RSS；`cpuUsage()`、
  `resourceUsage()` 也是整个 Komari 进程，不是单插件指标。
- 这些 process 指标当前只实现于 Linux 和 Windows；其他 GOOS 上会抛出“不支持”错误。
- `process.kill()` 需要 `AllowExec`。只有 `SIGKILL` 映射到 Kill，其他 signal 统一按
  `os.Interrupt` 处理。
- `process.exit()` 和 `abort()` 只抛出 JavaScript/Go error，不会退出 Komari 进程。
- `stdout/stderr` 是 `stream.Writable` 实例，写入会同步写宿主流，可能阻塞当前事件循环；
  `stdin` 是 `stream.Readable` 实例但未连接，永不产生数据。三个 stream 的 `fd` 固定为
  `-1`、`isTTY` 固定为 `false`。

```js
function runtimeInfo() {
  const memory = process.memoryUsage();
  console.log("cwd=%s rss=%d", process.cwd(), memory.rss);
  return memory.rss > 0;
}
```

### `child_process`

整个模块要求 `AllowExec: true`，否则 `require("child_process")` 会抛错。

| 接口 | 状态 |
| --- | --- |
| `spawn/exec/execFile` | `部分实现` |
| `spawnSync/execSync/execFileSync` | `部分实现`，同步阻塞事件循环 |
| `fork` | `未实现`，明确抛错 |
| ChildProcess `pid/exitCode/signalCode/killed/stdin/stdout/stderr/stdio` | `部分实现` |
| ChildProcess `kill/ref/unref/disconnect/send` | `部分实现` 或调用抛错，见下文 |

支持的 options：`cwd`、`env`、`shell`、`timeout`、`encoding`、`maxBuffer`。
`cwd` 受 `BaseDir` 限制；子进程默认不继承 runtime 的 `Timeout`，只有显式传入 `timeout` 时才会自动结束。
请求的 `maxBuffer` 只能收紧 Go 侧全局上限，不能放大。
`stdio/input/detached/uid/gid/windowsHide/windowsVerbatimArguments/serialization` 等未实现。

- `spawn()` 的 `stdin` 是 `stream.Writable`（写入经后台写队列串行执行，`end()` 后关闭管道），
  `stdout/stderr` 是 `stream.Readable`，支持 `data/end/close` 事件、`setEncoding`、
  `pipe()`/`pipeline()`/`finished()`；`encoding` option 会预置解码。
- `exec()`/`execFile()` 返回的是简化 child，只保证 pid、kill 和 exit/close/error 等事件，
  不像当前 `spawn()` 结果那样暴露完整的 stdin/stdout/stderr 属性。
- child 输出当前没有跨 goroutine 的 backpressure：如果消费者长时间不读，输出会在
  readable 缓冲中累积。
- ChildProcess `ref/unref` 调用会明确抛错（事件循环由宿主驱动，没有引用计数语义）。
- `connected` 固定为 `false`，`send()` 固定返回 `false` 并向 callback 报告 IPC 未启用；
  `disconnect()` 只发事件，没有 IPC channel。
- `kill("SIGKILL")` 使用强制结束，其他 signal 统一按 interrupt 处理；`signalCode` 当前不更新。
- `exec()` 和 `execFile()` 当前要求 callback。

```js
function runCommand() {
  const childProcess = require("child_process");
  const command = process.platform === "win32" ? "echo ready" : "printf ready";
  return new Promise((resolve) => {
    childProcess.exec(command, { encoding: "utf8" }, (error, stdout) => {
      resolve(!error && stdout.trim() === "ready");
    });
  });
}
```

### `net`

只实现 TCP；不支持 Unix socket、Windows named pipe、UDP 或 TLS。

| 对象 | 可用成员 | 状态 |
| --- | --- | --- |
| 模块 | `createServer/connect/createConnection/isIP/isIPv4/isIPv6` | `部分实现` |
| 默认 family | `getDefaultAutoSelectFamily` 固定 `true`；`setDefaultAutoSelectFamily` 调用抛错 | `固定值/调用抛错` |
| Server | `listen/close/address/getConnections`、`listening/maxConnections`、EventEmitter | `部分实现` |
| Socket | `write/end/destroy/setEncoding/setTimeout/setNoDelay/setKeepAlive/address`、地址属性、EventEmitter | `部分实现` |

Server `listen()` 需要 `AllowListen`，默认 host 是 `127.0.0.1`。`connect()` 是出站连接，
不需要此权限，并使用 Go `net.Dialer`/系统网络栈。

主要差异：

- `getDefaultAutoSelectFamily()` 固定返回 `true`；`setDefaultAutoSelectFamily()` 调用会明确抛错，
  不改变 Go resolver/dialer。实际地址选择仍由 Go 和系统网络配置决定。
- `Server.closeAllConnections/closeIdleConnections` 调用会明确抛错（连接未逐个跟踪）；
  `maxConnections` 不执行限制。
- Server/Socket 的 `ref/unref` 调用会明确抛错（事件循环由宿主驱动，没有引用计数语义）。
- Socket 的 `pause/resume` 调用会明确抛错；没有 backpressure、pipe 和完整 stream 状态。
- `setTimeout()` 使用连接 deadline，不完全等同于 Node 的 idle timeout 重置语义。
- 连接 options 只处理常见 `port/host`；`family/lookup/localAddress/happyEyeballs` 等未实现。
- 异步 dial 完成前的占位 Socket 没有 `write/end/destroy`，因此不能像 Node 一样预先排队写入；
  应等待 `connect` 事件。
- `Socket`、`Server`、`BlockList`、`SocketAddress` 构造器未导出。

```js
function tcpEcho() {
  const net = require("net");
  return new Promise((resolve) => {
    const server = net.createServer((socket) => {
      socket.on("data", (data) => socket.end(data));
    });
    server.listen(0, "127.0.0.1", () => {
      const client = net.connect(server.address().port, "127.0.0.1");
      let output = "";
      client.setEncoding("utf8");
      client.on("connect", () => client.write("hello"));
      client.on("data", (chunk) => output += chunk);
      client.on("end", () => server.close(() => resolve(output === "hello")));
      client.on("error", () => resolve(false));
    });
  });
}
```

### `http`

#### 模块级接口

```text
createServer  request  get
METHODS  STATUS_CODES  maxHeaderSize
validateHeaderName  validateHeaderValue
Agent  ClientRequest  globalAgent  IncomingMessage  ServerResponse
```

没有单独的 `https` 模块，但 `fetch()` 和当前基于 fetch 的 `http.request()` 可以请求
HTTPS URL。

#### Server

| 接口 | 状态 |
| --- | --- |
| `listen/close/address/setTimeout` | `部分实现` |
| `closeAllConnections` | `部分实现`：调用 Go server `Close()`，行为比 Node 更强，会停止 server |
| `closeIdleConnections` | 调用抛错（Go `net/http` 不暴露单个空闲连接） |
| `ref/unref` | 调用抛错（事件循环由宿主驱动） |
| `listening` | `可用` |
| `timeout/keepAliveTimeout/headersTimeout/requestTimeout` | `部分实现/占位` |

端口监听要求 `AllowListen`，默认绑定 `127.0.0.1`。请求体会一次性缓冲并受
`MaxHTTPBodyBytes` 限制；handler 必须在 runtime `Timeout` 内调用 response `end()`，
否则返回 504。

`keepAliveTimeout/headersTimeout/requestTimeout` 属性目前不驱动对应的 Go server 配置；
`setTimeout()` 只在监听前保存 read timeout，timeout 事件也不具备完整 Node 语义。

#### IncomingMessage 与 ServerResponse

IncomingMessage 是 `stream.Readable` 实例，可用属性和方法：

```text
method  url  headers  headersDistinct  rawHeaders
httpVersion  httpVersionMajor  httpVersionMinor
complete  aborted  readable  socket  connection
setEncoding()  pause()  resume()  destroy()
push()/read()/pipe() 等 Readable 成员
```

body 已完整读入内存，在 handler 返回后以缓冲形式推入 stream，再发出 `end/close`，
因此 `data` 可能不止一个 chunk（按 `highWaterMark` 分割）。`complete` 固定为 `true`；
`setEncoding/pause/resume` 与 `read()` 拉取模式均可用。

ServerResponse 是 `stream.Writable` 实例，可用成员：

```text
statusCode  statusMessage  headersSent  writableEnded  writableFinished
destroyed  sendDate
setHeader  appendHeader  getHeader  getHeaders  getHeaderNames
hasHeader  removeHeader  writeHead  flushHeaders  writeContinue
write  end  destroy  setTimeout
cork()/uncork() 等 Writable 成员
```

`write()`/`end()` 走 Writable 语义（`write()` 返回 boolean，`end()` 触发
`finish`，随后 `close`）；response body 会缓冲到 `end()`。`flushHeaders()` 只更新状态，
`writeContinue()` 调用会明确抛错，
`sendDate` 和自定义 `statusMessage` 不控制 Go `net/http` 的实际 wire 行为。

#### ClientRequest

`request/get` 返回简化 ClientRequest，支持 header 方法、`write/end/abort/destroy/setTimeout`。
请求体缓冲到 `end()` 后通过 `fetch()` 发送；响应也一次性缓冲后发出一个 `data`。

- `Agent` 只保存 `options/keepAlive/maxSockets`，`destroy()` 调用会明确抛错，不提供连接池控制。
- ClientRequest `flushHeaders/setNoDelay/setSocketKeepAlive` 调用会明确抛错。
- client timeout 只定时发 `timeout`，不会自动终止请求，也不会在完成后自动取消 timer。
- upgrade、CONNECT、trailers、stream backpressure、socket 复用和完整 Agent 行为未实现。

```js
function serveOnce() {
  const http = require("http");
  return new Promise((resolve) => {
    const server = http.createServer((request, response) => {
      response.statusCode = 200;
      response.setHeader("Content-Type", "application/json");
      response.end(JSON.stringify({ ok: true }));
    });
    server.listen(0, "127.0.0.1", async () => {
      const url = "http://127.0.0.1:" + server.address().port + "/health";
      const response = await fetch(url);
      const body = await response.json();
      server.close(() => resolve(response.ok && body.ok));
    });
  });
}
```

### `crypto`

模块同时注册为 `crypto` 与 `node:crypto`，仅 `NodeJS: true` 时可用。实现基于 Go 标准库和
`golang.org/x/crypto`；常见用法与 Node.js 一致，边界行为差异见下文。

| 类别 | 接口 | 状态 |
| --- | --- | --- |
| 摘要 | `createHash`、`createHmac`、`hash()`、`Hash`/`Hmac`、`update/digest/copy`、`getHashes()` | `可用` |
| 随机数 | `randomBytes`、`randomFillSync`、`randomFill`、`randomInt`、`randomUUID`、`getRandomValues` | `可用` |
| 密钥派生 | `pbkdf2Sync`/`pbkdf2`、`scryptSync`/`scrypt` | `可用` |
| 对称加密 | `createCipheriv`/`createDecipheriv`、`Cipher`/`Decipher`（AES-CBC/CTR/ECB/GCM、ChaCha20-Poly1305） | `部分实现` |
| 签名验签 | `createSign`/`createVerify`、`sign`/`verify`（RSA PKCS1v15、ECDSA、Ed25519，PEM 密钥） | `部分实现` |
| 其他 | `timingSafeEqual`、`getCiphers()`、`constants`（RSA padding 常量） | `可用` |

#### Hash 与 HMAC

支持的摘要算法（`getHashes()` 返回）：`md5`、`sha1`、`sha224`、`sha256`、`sha384`、
`sha512`、`sha512-224`、`sha512-256`、`sha3-224`、`sha3-256`、`sha3-384`、`sha3-512`、
`ripemd160`（及 `ripemd`/`rmd160` 别名）、`blake2b512`、`blake2s256`。未知算法抛
`TypeError: Digest method not supported`。

```js
const crypto = require("node:crypto");
const digest = crypto.createHash("sha256").update("komari").digest("hex");
const mac = crypto.createHmac("sha256", "secret").update("body").digest("base64");
const oneShot = crypto.hash("sha256", "komari", "hex");
```

- `update(data[, inputEncoding])` 的 inputEncoding 支持 `utf8`/`hex`/`base64`/`base64url`/
  `latin1`/`binary`/`ascii`/`utf16le`；缺省按 utf8 处理字符串。
- `digest([outputEncoding])` 缺省返回 Buffer；`copy()` 返回可继续 `update` 的副本。
- digest 之后继续 `update`/`digest` 抛 `ERR_CRYPTO_HASH_FINALIZED`。

#### 随机数、UUID 与恒时比较

- `randomBytes(size[, callback])`：`0 <= size <= 2^31-1`。
- `randomFillSync(buffer[, offset[, size]])` 与 `randomFill(buffer[, offset[, size]][, callback])`：
  TypedArray 的 offset/size 以元素为单位，DataView 以字节为单位（与 Node 一致）。
- `randomInt([min,] max[, callback])`：`max` 必须是不超过 2^48 的安全整数。
- `randomUUID()`：生成 v4 UUID。
- `getRandomValues(typedArray)`：仅接受整数 TypedArray（含 BigInt64/BigUint64），
  超过 64 KiB 抛 `QuotaExceededError`。
- `timingSafeEqual(a, b)`：长度不一致抛 `ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH`。

#### PBKDF2 与 scrypt

```js
const key = crypto.pbkdf2Sync("password", salt, 100000, 32, "sha256");
const key2 = crypto.scryptSync("password", salt, 32, { N: 16384, r: 8, p: 1 });
```

- `pbkdf2`/`pbkdf2Sync` 的 digest 参数必填；迭代次数 1..2^31-1，keylen 0..2^31-1。
- `scrypt`/`scryptSync` 默认 `N=16384, r=8, p=1, maxmem=32 MiB`，选项同时接受
  `N/r/p` 与 `cost/blockSize/parallelization` 别名；参数非法或内存超限抛
  `ERR_CRYPTO_INVALID_SCRYPT_PARAMS`。
- 异步版本把 CPU 计算放在事件循环外执行，不阻塞该 runtime 的其他任务。

#### 对称加密

支持算法：`aes-128/192/256-cbc`、`aes-128/192/256-ctr`、`aes-128/192/256-ecb`、
`aes-128/192/256-gcm`、`chacha20-poly1305`，以及 `aes128`/`aes192`/`aes256`
（= CBC 别名）。`getCiphers()` 返回当前支持的列表。

```js
const cipher = crypto.createCipheriv("aes-256-gcm", key, iv);
cipher.setAAD(associatedData);
const encrypted = Buffer.concat([cipher.update(plain), cipher.final()]);
const tag = cipher.getAuthTag();

const decipher = crypto.createDecipheriv("aes-256-gcm", key, iv);
decipher.setAAD(associatedData);
decipher.setAuthTag(tag);
const plain = Buffer.concat([decipher.update(encrypted), decipher.final()]);
```

- CBC/ECB 默认 PKCS#7 填充，可用 `setAutoPadding(false)` 关闭；无填充时块长度不整会在
  `final()` 抛错。
- GCM/ChaCha20-Poly1305 支持 `setAAD`、`setAuthTag`（解密）、`getAuthTag`（加密）。
- 与 Node 的主要差异：
  - GCM/ChaCha20-Poly1305 的 `update()` 返回空 Buffer，所有数据在 `final()` 一次性返回；
    Node 中 `update()` 会逐步返回密文。使用 `Buffer.concat([update(), final()])` 的常见
    写法不受影响，认证标签仍只从 `getAuthTag()` 获取。
  - GCM 的 `authTagLength` 仅支持 12-16（Node 支持 4-16）；`chacha20-poly1305` 只支持
    默认 16。GCM 使用非 12 字节 IV 时 `authTagLength` 必须为 16。
  - 密钥长度与 IV 长度校验同 Node（`ERR_CRYPTO_INVALID_KEYLEN`、`ERR_CRYPTO_INVALID_IV`），
    未知算法抛 `ERR_CRYPTO_UNKNOWN_CIPHER`。

#### 签名与验签

```js
const signature = crypto.sign("RSA-SHA256", data, privateKeyPem);
const ok = crypto.verify("RSA-SHA256", data, publicKeyPem, signature);

const signer = crypto.createSign("sha256");
signer.update(data);
const sig = signer.sign(privateKeyPem, "base64");
const valid = crypto.createVerify("sha256").update(data).verify(publicKeyPem, sig, "base64");
```

- 算法名：摘要名（如 `sha256`，按密钥类型自动选择 RSA PKCS1v15 或 ECDSA）、
  `RSA-<digest>`、`ecdsa-with-<digest>`、`ed25519`；`sign(null, ...)`/`verify(null, ...)`
  也可用于 Ed25519。未知算法抛 `ERR_CRYPTO_INVALID_DIGEST`。
- 密钥为 PEM 字符串或 Buffer：私钥支持 PKCS#1/PKCS#8/SEC1，公钥支持 PKIX/PKCS#1，
  也接受私钥 PEM 并自动取其公钥部分。
- `sign`/`verify` 最后一个参数为函数时走异步回调。
- Ed25519 密钥配摘要算法抛 `ERR_OSSL_INVALID_DIGEST`；验签失败返回 `false` 而不是抛错。
- 未实现：`generateKeyPairSync`、`KeyObject`/`createPrivateKey`/`createPublicKey`、
  `generateKeyPair`、RSA-OAEP 加解密、RSA-PSS、`DiffieHellman`/`ECDH`、`webcrypto`/
  `SubtleCrypto`，以及签名 `padding`/`saltLength` 选项。
## 固定值与调用抛错汇总

下表列出容易被误认为“完整可用”的占位接口；没有真实语义的方法调用会明确抛错。

| 接口 | 当前行为 |
| --- | --- |
| Body `body` | 固定 `null`；实际数据保存在内部 buffer，通过消费方法读取。 |
| `Request.destination` | 固定 `""`。 |
| `Request.duplex` | 固定 `"half"`。 |
| `File.webkitRelativePath` | 固定 `""`。 |
| `Event.isTrusted` | 固定 `false`。 |
| XHR `responseXML`、document response | 固定 `null`。 |
| `fs.Stats` 多个身份/时间字段 | 多个字段为固定值，四类时间都来自 ModTime。 |
| `fs.Dirent.parentPath/path` | 固定 `""`。 |
| `os.userInfo().shell` | 固定 `""`。 |
| `os.constants.errno` | 固定空对象；signals 只有 3 项。 |
| `process.versions.node` | 固定 `"0.0.0-goja"`。 |
| `process.connected`、`process.config` | 固定 `false`、空对象。 |
| process stream `fd/isTTY` | `fd=-1`、`isTTY=false`；`stdin` 未连接，作为 Readable 存在但永不产生数据。 |
| ChildProcess `connected/send/ref/unref/disconnect` | 无 IPC；`connected` 固定 `false`，`send()` 向 callback 报告 IPC 未启用，`ref/unref` 调用抛错，`disconnect()` 只发事件。 |

| `net.getDefaultAutoSelectFamily()` | 固定 `true`。 |
| `net.setDefaultAutoSelectFamily()` | 调用抛错。 |
| `net.Server.closeAllConnections/closeIdleConnections` | 调用抛错（连接未逐个跟踪）。 |
| net Server/Socket `ref/unref`、Socket `pause/resume` | 调用抛错。 |
| `http.Server.closeIdleConnections` | 调用抛错。 |
| `http.Server/ClientRequest` 若干 timeout/socket 属性 | 仅部分状态或定时事件，不是完整连接控制。 |
| HTTP `Agent.destroy`、ClientRequest `flushHeaders/setNoDelay/setSocketKeepAlive` | 调用抛错。 |
| ServerResponse `writeContinue` | 调用抛错；`flushHeaders` 只更新状态，不实际 flush。 |

| GCM/ChaCha20-Poly1305 update() | 返回空 Buffer，数据在 final() 一次性返回；Node 中 update() 逐步返回密文。Buffer.concat([update(), final()]) 写法不受影响。 |
| GCM authTagLength | 仅支持 12-16（Node 支持 4-16）；chacha20-poly1305 仅支持默认 16，其他值抛 ERR_CRYPTO_INVALID_AUTH_TAG。 |
| GCM 非 12 字节 IV | 可用，但此时 authTagLength 必须为 16，否则构造抛错。 |
| sign/verify 密钥 | 仅接受 PEM 字符串/Buffer；KeyObject、generateKeyPairSync、RSA-PSS 与 padding/saltLength 选项未实现。 |

## 常见但未实现的接口

| 类别 | 未实现示例 |
| --- | --- |
| 浏览器 DOM | `window`、`document`、DOM node、`navigator`、`location`、storage、canvas。 |
| 浏览器网络 | `WebSocket`、`EventSource`、Service Worker、完整 Streams、WebRTC、WebCrypto。 |
| 模块系统 | ESM `import/export`、动态 `import()`、顶层 CommonJS `module/exports`，以及 `require.resolve/cache/extensions/main`。 |
| Node 核心模块 | `https`、`tls`、`dns`、`dgram`、`zlib`、`worker_threads`、`cluster`、`readline`、`assert` 等。 |
| 完整 fs | watcher、opendir、cp、link、statfs 和完整 FileHandle。 |
| 完整 net/http | Unix/pipe/TLS、构造器族、真实 backpressure、Agent 池、upgrade/trailer。 |
| timer handle | `ref/unref/refresh/hasRef`。 |
| 插件资源隔离 | 没有单 runtime CPU、内存、goroutine、网络或文件 IO 统计，也没有 Docker/cgroup 式额度限制。 |

`process.memoryUsage/cpuUsage/resourceUsage` 不能用于衡量单个插件：它们读取整个 Komari
Go 进程。当前可用于约束单次执行的机制只有 `Timeout`、HTTP body 上限、child output
上限、`BaseDir`、`AllowExec` 和 `AllowListen`；它们不是完整的资源配额系统。

## 使用建议

1. 对不可信脚本始终设置独立且已存在的 `BaseDir`，保持 `AllowAllFileAccess`、
   `AllowExec`、`AllowListen` 为 `false`，只按需开启。
2. 优先使用 callback/Promise 版 fs、异步 fetch 和异步 child process；同步 XHR、同步 fs、
   `execSync` 等会阻塞该 runtime 的事件循环。
3. 所有 runtime 都应 `defer runtime.Close()`，否则 listener、socket、文件和 timer 可能继续存活。
4. 引入 npm/CommonJS 包前先核对它依赖的 Node 核心模块。纯 JavaScript 不代表能在当前兼容子集运行。
5. 调用会抛错的方法或依赖固定值前，应把它视为“不支持”，不要把存在的方法名当成能力检测。

