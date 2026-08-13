package child_process

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/fs"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/writequeue"
)

type childCommandOptions struct {
	cwd       string
	env       []string
	shell     bool
	timeout   time.Duration
	encoding  string
	maxBuffer int
}

type childOutputLimitError struct {
	stream string
}

func (e *childOutputLimitError) Error() string {
	return fmt.Sprintf("%s maxBuffer length exceeded", e.stream)
}

type childOutputBuffer struct {
	buffer bytes.Buffer
	limit  int
	stream string
	cancel context.CancelFunc
	err    error
	once   sync.Once
}

func (b *childOutputBuffer) Write(data []byte) (int, error) {
	if b.err != nil {
		return len(data), nil
	}
	remaining := b.limit - b.Len()
	if remaining >= len(data) {
		_, _ = b.buffer.Write(data)
		return len(data), nil
	}
	if remaining > 0 {
		_, _ = b.buffer.Write(data[:remaining])
	}
	b.err = &childOutputLimitError{stream: b.stream}
	b.once.Do(b.cancel)
	return len(data), nil
}

func (b *childOutputBuffer) Len() int      { return b.buffer.Len() }
func (b *childOutputBuffer) Bytes() []byte { return b.buffer.Bytes() }

type Module struct {
	runtime    *bridge.Runtime
	filesystem *fs.Module
	allowExec  bool
	maxOutput  int
}

func New(runtime *bridge.Runtime, filesystem *fs.Module, allowExec bool, maxOutput int) *Module {
	return &Module{runtime: runtime, filesystem: filesystem, allowExec: allowExec, maxOutput: maxOutput}
}

func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	if !m.allowExec {
		panic(vm.NewGoError(fmt.Errorf("child_process requires AllowExec")))
	}
	exports := vm.NewObject()
	_ = exports.Set("spawn", func(call goja.FunctionCall) goja.Value {
		command, arguments, options := m.childCommandArguments(vm, call, false)
		return m.spawnChild(vm, command, arguments, options)
	})
	_ = exports.Set("exec", func(call goja.FunctionCall) goja.Value {
		command := call.Argument(0).String()
		optionsValue, callbackValue := call.Argument(1), call.Argument(2)
		if _, ok := goja.AssertFunction(optionsValue); ok {
			callbackValue, optionsValue = optionsValue, goja.Undefined()
		}
		callback, ok := goja.AssertFunction(callbackValue)
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		options := m.childOptions(vm, optionsValue)
		options.shell = true
		return m.execChild(vm, command, nil, options, callback)
	})
	_ = exports.Set("execFile", func(call goja.FunctionCall) goja.Value {
		command, arguments, options := m.childCommandArguments(vm, call, true)
		callbackIndex := 1
		if _, ok := call.Argument(1).Export().([]any); ok {
			callbackIndex = 2
		}
		callbackValue := call.Argument(callbackIndex)
		if _, ok := goja.AssertFunction(callbackValue); !ok {
			callbackValue = call.Argument(callbackIndex + 1)
		}
		callback, ok := goja.AssertFunction(callbackValue)
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		return m.execChild(vm, command, arguments, options, callback)
	})
	_ = exports.Set("spawnSync", func(call goja.FunctionCall) goja.Value {
		command, arguments, options := m.childCommandArguments(vm, call, false)
		return m.spawnChildSync(vm, command, arguments, options)
	})
	_ = exports.Set("execFileSync", func(call goja.FunctionCall) goja.Value {
		command, arguments, options := m.childCommandArguments(vm, call, false)
		result := m.runChildSync(command, arguments, options)
		if result.err != nil {
			panic(childProcessError(vm, result.err, result.exitCode, result.stdout, result.stderr))
		}
		return encodeChildOutput(vm, result.stdout, options.encoding)
	})
	_ = exports.Set("execSync", func(call goja.FunctionCall) goja.Value {
		options := m.childOptions(vm, call.Argument(1))
		options.shell = true
		result := m.runChildSync(call.Argument(0).String(), nil, options)
		if result.err != nil {
			panic(childProcessError(vm, result.err, result.exitCode, result.stdout, result.stderr))
		}
		return encodeChildOutput(vm, result.stdout, options.encoding)
	})
	_ = exports.Set("fork", func() {
		panic(vm.NewGoError(fmt.Errorf("child_process.fork is unavailable because this runtime is not a Node executable")))
	})
	_ = module.Set("exports", exports)
}

func (m *Module) childCommandArguments(vm *goja.Runtime, call goja.FunctionCall, callbackExpected bool) (string, []string, childCommandOptions) {
	command := call.Argument(0).String()
	arguments := []string{}
	optionsIndex := 1
	if value := call.Argument(1); !goja.IsUndefined(value) && !goja.IsNull(value) {
		var exported []string
		if err := vm.ExportTo(value, &exported); err == nil {
			arguments = exported
			optionsIndex = 2
		}
	}
	options := m.childOptions(vm, call.Argument(optionsIndex))
	_ = callbackExpected
	return command, arguments, options
}

func (m *Module) childOptions(vm *goja.Runtime, value goja.Value) childCommandOptions {
	options := childCommandOptions{cwd: m.filesystem.Cwd(), env: os.Environ(), maxBuffer: m.maxOutput}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return options
	}
	object := value.ToObject(vm)
	if cwd := object.Get("cwd"); cwd != nil && !goja.IsUndefined(cwd) && !goja.IsNull(cwd) {
		resolved, err := m.filesystem.Resolve(cwd.String(), false)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		options.cwd = resolved
	}
	if environment := object.Get("env"); environment != nil && !goja.IsUndefined(environment) && !goja.IsNull(environment) {
		var values map[string]string
		if err := vm.ExportTo(environment, &values); err != nil {
			panic(vm.NewTypeError("env must be an object"))
		}
		options.env = make([]string, 0, len(values))
		for name, value := range values {
			options.env = append(options.env, name+"="+value)
		}
	}
	if shell := object.Get("shell"); shell != nil {
		options.shell = shell.ToBoolean()
	}
	if timeoutValue := object.Get("timeout"); timeoutValue != nil && timeoutValue.ToInteger() > 0 {
		timeout := timeoutValue.ToInteger()
		options.timeout = time.Duration(timeout) * time.Millisecond
	}
	if encoding := object.Get("encoding"); encoding != nil && !goja.IsUndefined(encoding) && !goja.IsNull(encoding) {
		options.encoding = encoding.String()
	}
	if maxBuffer := object.Get("maxBuffer"); maxBuffer != nil {
		requested := maxBuffer.ToInteger()
		if requested > 0 && requested < int64(options.maxBuffer) {
			options.maxBuffer = int(requested)
		}
	}
	return options
}

func childExecCommand(command string, arguments []string, options childCommandOptions) (*exec.Cmd, context.CancelFunc) {
	ctx := context.Background()
	cancel := func() {}
	if options.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, options.timeout)
	}
	if options.shell {
		line := command
		if len(arguments) > 0 {
			line += " " + strings.Join(arguments, " ")
		}
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", line), cancel
		}
		return exec.CommandContext(ctx, "/bin/sh", "-c", line), cancel
	}
	return exec.CommandContext(ctx, command, arguments...), cancel
}

func (m *Module) childOutputLimit(options childCommandOptions) int {
	if options.maxBuffer > 0 && options.maxBuffer < m.maxOutput {
		return options.maxBuffer
	}
	return m.maxOutput
}

func (m *Module) spawnChild(vm *goja.Runtime, command string, arguments []string, options childCommandOptions) *goja.Object {
	child := events.NewEmitter(vm)
	cmd, cancel := childExecCommand(command, arguments, options)
	cmd.Dir = options.cwd
	cmd.Env = options.env
	stdin, stdinErr := cmd.StdinPipe()
	stdoutReader, stdoutErr := cmd.StdoutPipe()
	stderrReader, stderrErr := cmd.StderrPipe()
	if err := errors.Join(stdinErr, stdoutErr, stderrErr); err != nil {
		panic(vm.NewGoError(err))
	}
	stdinStream, stdout, stderr := m.childStreams(vm, stdin)
	_ = child.Set("stdout", stdout)
	_ = child.Set("stderr", stderr)
	_ = child.Set("stdin", stdinStream)
	_ = child.Set("stdio", []any{stdinStream, stdout, stderr})
	_ = child.Set("pid", goja.Undefined())
	_ = child.Set("exitCode", goja.Null())
	_ = child.Set("signalCode", goja.Null())
	_ = child.Set("killed", false)
	_ = child.Set("connected", false)

	if err := cmd.Start(); err != nil {
		cancel()
		m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
			_ = m.runtime.RunJob(vm, "child_process.spawn", func() error {
				return events.Emit(vm, child, "error", vm.NewGoError(err))
			})
		}, 0)
		return child
	}
	_ = child.Set("pid", cmd.Process.Pid)
	resourceID := m.runtime.AddResource(func() { _ = cmd.Process.Kill(); cancel() })
	if resourceID == 0 {
		_ = stdin.Close()
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
		go func() { _ = cmd.Wait() }()
		return child
	}
	_ = child.Set("kill", func(signal string) bool {
		if cmd.Process == nil {
			return false
		}
		var err error
		if signal == "SIGKILL" {
			err = cmd.Process.Kill()
		} else {
			err = cmd.Process.Signal(os.Interrupt)
		}
		if err == nil {
			_ = child.Set("killed", true)
		}
		return err == nil
	})
	_ = child.Set("ref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("child_process.ChildProcess.ref is not supported by jsruntime; the event loop is host-driven")))
	})
	_ = child.Set("unref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("child_process.ChildProcess.unref is not supported by jsruntime; the event loop is host-driven")))
	})
	_ = child.Set("disconnect", func() { _ = events.Emit(vm, child, "disconnect") })
	_ = child.Set("send", func(call goja.FunctionCall) goja.Value {
		if callback, ok := goja.AssertFunction(call.Argument(len(call.Arguments) - 1)); ok {
			_, _ = callback(goja.Undefined(), vm.NewGoError(fmt.Errorf("IPC is not enabled")))
		}
		return vm.ToValue(false)
	})

	m.pipeChildOutput(vm, stdoutReader, stdout, options.encoding)
	m.pipeChildOutput(vm, stderrReader, stderr, options.encoding)
	go func() {
		err := cmd.Wait()
		cancel()
		m.runtime.RemoveResource(resourceID)
		exitCode := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				exitCode = -1
			}
		}
		m.runtime.RunOnLoop(func(vm *goja.Runtime) {
			_ = m.runtime.RunJob(vm, "child_process", func() error {
				_ = child.Set("exitCode", exitCode)
				if emitErr := events.Emit(vm, child, "exit", exitCode, goja.Null()); emitErr != nil {
					return emitErr
				}
				return events.Emit(vm, child, "close", exitCode, goja.Null())
			})
		})
	}()
	return child
}

//go:embed stdio.js
var childStdioSource string

// childStreams creates the Writable stdin and Readable stdout/stderr streams
// for one spawned child process. The Go side owns the underlying pipes and
// serializes writes through a per-child queue.
func (m *Module) childStreams(vm *goja.Runtime, stdin io.WriteCloser) (*goja.Object, *goja.Object, *goja.Object) {
	factoryValue, err := vm.RunString(childStdioSource)
	if err != nil {
		panic(vm.NewGoError(fmt.Errorf("load child_process stdio: %w", err)))
	}
	factory, _ := goja.AssertFunction(factoryValue)
	hooks := vm.NewObject()
	var writes writequeue.Queue
	_ = hooks.Set("stdinWrite", func(chunk goja.Value, callback goja.Callable) {
		data := append([]byte(nil), buffer.Bytes(vm, chunk)...)
		writes.Submit(func() {
			_, err := stdin.Write(data)
			if callback == nil {
				return
			}
			m.runtime.RunOnLoop(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "child_process.stdin.write", func() error {
					if err != nil {
						_, callbackErr := callback(goja.Undefined(), vm.NewGoError(err))
						return callbackErr
					}
					_, callbackErr := callback(goja.Undefined())
					return callbackErr
				})
			})
		})
	})
	_ = hooks.Set("stdinEnd", func(callback goja.Callable) {
		writes.Submit(func() {
			err := stdin.Close()
			m.runtime.RunOnLoop(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "child_process.stdin.end", func() error {
					if callback != nil {
						if err != nil {
							_, callbackErr := callback(goja.Undefined(), vm.NewGoError(err))
							return callbackErr
						}
						_, callbackErr := callback(goja.Undefined())
						return callbackErr
					}
					return nil
				})
			})
		})
	})
	_ = hooks.Set("stdinDestroy", func() {
		go func() { _ = stdin.Close() }()
	})
	streams, err := factory(goja.Undefined(), require.Require(vm, "stream"), hooks)
	if err != nil {
		panic(err)
	}
	streamsObject := streams.ToObject(vm)
	createStdin, _ := goja.AssertFunction(streamsObject.Get("createStdin"))
	createOutput, _ := goja.AssertFunction(streamsObject.Get("createOutput"))
	if createStdin == nil || createOutput == nil {
		panic(vm.NewGoError(fmt.Errorf("child_process stdio factory is incomplete")))
	}
	stdinValue, err := createStdin(goja.Undefined())
	if err != nil {
		panic(err)
	}
	stdoutValue, err := createOutput(goja.Undefined())
	if err != nil {
		panic(err)
	}
	stderrValue, err := createOutput(goja.Undefined())
	if err != nil {
		panic(err)
	}
	return stdinValue.ToObject(vm), stdoutValue.ToObject(vm), stderrValue.ToObject(vm)
}

func childCallback(call goja.FunctionCall) goja.Callable {
	for index := len(call.Arguments) - 1; index >= 0; index-- {
		if callback, ok := goja.AssertFunction(call.Arguments[index]); ok {
			return callback
		}
	}
	return nil
}

func (m *Module) pipeChildOutput(vm *goja.Runtime, reader io.Reader, stream *goja.Object, encoding string) {
	push, _ := goja.AssertFunction(stream.Get("push"))
	setEncoding, _ := goja.AssertFunction(stream.Get("setEncoding"))
	if encoding != "" && setEncoding != nil {
		_, _ = setEncoding(stream, vm.ToValue(encoding))
	}
	go func() {
		data := make([]byte, 32*1024)
		for {
			count, err := reader.Read(data)
			if count > 0 {
				chunk := append([]byte(nil), data[:count]...)
				delivered := make(chan struct{})
				if !m.runtime.RunOnLoop(func(vm *goja.Runtime) {
					defer close(delivered)
					_ = m.runtime.RunJob(vm, "child_process stream", func() error {
						_, pushErr := push(stream, buffer.WrapBytes(vm, chunk))
						return pushErr
					})
				}) {
					return
				}
				<-delivered
			}
			if err != nil {
				m.runtime.RunOnLoop(func(vm *goja.Runtime) {
					_ = m.runtime.RunJob(vm, "child_process stream close", func() error {
						_, pushErr := push(stream, goja.Null())
						return pushErr
					})
				})
				return
			}
		}
	}()
}

func (m *Module) execChild(vm *goja.Runtime, command string, arguments []string, options childCommandOptions, callback goja.Callable) *goja.Object {
	child := events.NewEmitter(vm)
	_ = child.Set("pid", goja.Undefined())
	_ = child.Set("killed", false)
	cmd, cancel := childExecCommand(command, arguments, options)
	cmd.Dir, cmd.Env = options.cwd, options.env
	outputLimit := m.childOutputLimit(options)
	stdout := &childOutputBuffer{limit: outputLimit, stream: "stdout", cancel: cancel}
	stderr := &childOutputBuffer{limit: outputLimit, stream: "stderr", cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		cancel()
		m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
			_ = m.runtime.RunJob(vm, "child_process.exec", func() error {
				if _, callbackErr := callback(goja.Undefined(), childProcessError(vm, err, -1, nil, nil), goja.Undefined(), goja.Undefined()); callbackErr != nil {
					return callbackErr
				}
				return events.Emit(vm, child, "error", vm.NewGoError(err))
			})
		}, 0)
		return child
	}
	_ = child.Set("pid", cmd.Process.Pid)
	resourceID := m.runtime.AddResource(func() { _ = cmd.Process.Kill(); cancel() })
	if resourceID == 0 {
		go func() { _ = cmd.Wait() }()
		return child
	}
	_ = child.Set("kill", func() bool {
		err := cmd.Process.Kill()
		if err == nil {
			_ = child.Set("killed", true)
		}
		return err == nil
	})
	go func() {
		err := cmd.Wait()
		err = errors.Join(err, stdout.err, stderr.err)
		cancel()
		m.runtime.RemoveResource(resourceID)
		exitCode := 0
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else if err != nil {
			exitCode = -1
		}
		m.runtime.RunOnLoop(func(vm *goja.Runtime) {
			_ = m.runtime.RunJob(vm, "child_process.exec", func() error {
				stdoutValue := encodeChildOutput(vm, stdout.Bytes(), options.encoding)
				stderrValue := encodeChildOutput(vm, stderr.Bytes(), options.encoding)
				var errorValue goja.Value = goja.Null()
				if err != nil {
					errorValue = childProcessError(vm, err, exitCode, stdout.Bytes(), stderr.Bytes())
				}
				if _, callbackErr := callback(goja.Undefined(), errorValue, stdoutValue, stderrValue); callbackErr != nil {
					return callbackErr
				}
				_ = child.Set("exitCode", exitCode)
				_ = events.Emit(vm, child, "exit", exitCode, goja.Null())
				return events.Emit(vm, child, "close", exitCode, goja.Null())
			})
		})
	}()
	return child
}

type childSyncResult struct {
	stdout   []byte
	stderr   []byte
	err      error
	exitCode int
	pid      int
}

func (m *Module) runChildSync(command string, arguments []string, options childCommandOptions) childSyncResult {
	cmd, cancel := childExecCommand(command, arguments, options)
	defer cancel()
	cmd.Dir, cmd.Env = options.cwd, options.env
	outputLimit := m.childOutputLimit(options)
	stdout := &childOutputBuffer{limit: outputLimit, stream: "stdout", cancel: cancel}
	stderr := &childOutputBuffer{limit: outputLimit, stream: "stderr", cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	err = errors.Join(err, stdout.err, stderr.err)
	result := childSyncResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), err: err, exitCode: 0}
	if cmd.Process != nil {
		result.pid = cmd.Process.Pid
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.exitCode = exitError.ExitCode()
	} else if err != nil {
		result.exitCode = -1
	}
	return result
}

func (m *Module) spawnChildSync(vm *goja.Runtime, command string, arguments []string, options childCommandOptions) *goja.Object {
	result := m.runChildSync(command, arguments, options)
	object := vm.NewObject()
	_ = object.Set("pid", result.pid)
	_ = object.Set("status", result.exitCode)
	_ = object.Set("signal", goja.Null())
	_ = object.Set("stdout", encodeChildOutput(vm, result.stdout, options.encoding))
	_ = object.Set("stderr", encodeChildOutput(vm, result.stderr, options.encoding))
	_ = object.Set("output", []any{goja.Null(), encodeChildOutput(vm, result.stdout, options.encoding), encodeChildOutput(vm, result.stderr, options.encoding)})
	if result.err != nil {
		_ = object.Set("error", childProcessError(vm, result.err, result.exitCode, result.stdout, result.stderr))
	}
	return object
}

func encodeChildOutput(vm *goja.Runtime, data []byte, encoding string) goja.Value {
	if encoding == "" || encoding == "buffer" {
		return buffer.WrapBytes(vm, append([]byte(nil), data...))
	}
	return buffer.EncodeBytes(vm, data, vm.ToValue(encoding))
}

func childProcessError(vm *goja.Runtime, err error, exitCode int, stdout, stderr []byte) *goja.Object {
	object := vm.NewGoError(err)
	var limitError *childOutputLimitError
	if errors.As(err, &limitError) {
		_ = object.Set("code", "ERR_CHILD_PROCESS_STDIO_MAXBUFFER")
	} else {
		_ = object.Set("code", exitCode)
	}
	_ = object.Set("killed", false)
	_ = object.Set("signal", goja.Null())
	_ = object.Set("stdout", encodeChildOutput(vm, stdout, ""))
	_ = object.Set("stderr", encodeChildOutput(vm, stderr, ""))
	return object
}
