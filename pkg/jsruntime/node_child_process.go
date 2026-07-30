package jsruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
)

type childCommandOptions struct {
	cwd      string
	env      []string
	shell    bool
	timeout  time.Duration
	encoding string
}

func (r *Runtime) loadChildProcessModule(vm *goja.Runtime, module *goja.Object) {
	if !r.allowExec {
		panic(vm.NewGoError(fmt.Errorf("child_process requires AllowExec")))
	}
	exports := vm.NewObject()
	_ = exports.Set("spawn", func(call goja.FunctionCall) goja.Value {
		command, arguments, options := r.childCommandArguments(vm, call, false)
		return r.spawnChild(vm, command, arguments, options)
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
		options := r.childOptions(vm, optionsValue)
		options.shell = true
		return r.execChild(vm, command, nil, options, callback)
	})
	_ = exports.Set("execFile", func(call goja.FunctionCall) goja.Value {
		command, arguments, options := r.childCommandArguments(vm, call, true)
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
		return r.execChild(vm, command, arguments, options, callback)
	})
	_ = exports.Set("spawnSync", func(call goja.FunctionCall) goja.Value {
		command, arguments, options := r.childCommandArguments(vm, call, false)
		return r.spawnChildSync(vm, command, arguments, options)
	})
	_ = exports.Set("execFileSync", func(call goja.FunctionCall) goja.Value {
		command, arguments, options := r.childCommandArguments(vm, call, false)
		result := r.runChildSync(command, arguments, options)
		if result.err != nil {
			panic(childProcessError(vm, result.err, result.exitCode, result.stdout, result.stderr))
		}
		return encodeChildOutput(vm, result.stdout, options.encoding)
	})
	_ = exports.Set("execSync", func(call goja.FunctionCall) goja.Value {
		options := r.childOptions(vm, call.Argument(1))
		options.shell = true
		result := r.runChildSync(call.Argument(0).String(), nil, options)
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

func (r *Runtime) childCommandArguments(vm *goja.Runtime, call goja.FunctionCall, callbackExpected bool) (string, []string, childCommandOptions) {
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
	options := r.childOptions(vm, call.Argument(optionsIndex))
	_ = callbackExpected
	return command, arguments, options
}

func (r *Runtime) childOptions(vm *goja.Runtime, value goja.Value) childCommandOptions {
	options := childCommandOptions{cwd: r.nodeCwd, env: os.Environ()}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return options
	}
	object := value.ToObject(vm)
	if cwd := object.Get("cwd"); cwd != nil && !goja.IsUndefined(cwd) && !goja.IsNull(cwd) {
		resolved, err := r.resolveNodePath(cwd.String(), false)
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

func (r *Runtime) spawnChild(vm *goja.Runtime, command string, arguments []string, options childCommandOptions) *goja.Object {
	child := newEventEmitter(vm)
	stdout := newEventEmitter(vm)
	stderr := newEventEmitter(vm)
	_ = child.Set("stdout", stdout)
	_ = child.Set("stderr", stderr)
	_ = child.Set("stdio", []any{goja.Null(), stdout, stderr})
	_ = child.Set("pid", goja.Undefined())
	_ = child.Set("exitCode", goja.Null())
	_ = child.Set("signalCode", goja.Null())
	_ = child.Set("killed", false)
	_ = child.Set("connected", false)

	cmd, cancel := childExecCommand(command, arguments, options)
	cmd.Dir = options.cwd
	cmd.Env = options.env
	stdin, stdinErr := cmd.StdinPipe()
	stdoutReader, stdoutErr := cmd.StdoutPipe()
	stderrReader, stderrErr := cmd.StderrPipe()
	if err := errors.Join(stdinErr, stdoutErr, stderrErr); err != nil {
		panic(vm.NewGoError(err))
	}
	_ = child.Set("stdin", childStdin(vm, stdin))
	if err := cmd.Start(); err != nil {
		cancel()
		r.loop.SetTimeout(func(vm *goja.Runtime) { _ = emitEvent(vm, child, "error", vm.NewGoError(err)) }, 0)
		return child
	}
	_ = child.Set("pid", cmd.Process.Pid)
	resourceID := r.addNodeResource(func() { _ = cmd.Process.Kill(); cancel() })
	if resourceID == 0 {
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
	_ = child.Set("ref", func() *goja.Object { return child })
	_ = child.Set("unref", func() *goja.Object { return child })
	_ = child.Set("disconnect", func() { _ = emitEvent(vm, child, "disconnect") })
	_ = child.Set("send", func(call goja.FunctionCall) goja.Value {
		if callback, ok := goja.AssertFunction(call.Argument(len(call.Arguments) - 1)); ok {
			_, _ = callback(goja.Undefined(), vm.NewGoError(fmt.Errorf("IPC is not enabled")))
		}
		return vm.ToValue(false)
	})

	r.pipeChildOutput(stdoutReader, stdout, options.encoding)
	r.pipeChildOutput(stderrReader, stderr, options.encoding)
	go func() {
		err := cmd.Wait()
		cancel()
		r.removeNodeResource(resourceID)
		exitCode := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				exitCode = -1
			}
		}
		r.loop.RunOnLoop(func(vm *goja.Runtime) {
			r.runAsyncJob(vm, "child_process", func() error {
				_ = child.Set("exitCode", exitCode)
				if emitErr := emitEvent(vm, child, "exit", exitCode, goja.Null()); emitErr != nil {
					return emitErr
				}
				return emitEvent(vm, child, "close", exitCode, goja.Null())
			})
		})
	}()
	return child
}

func childStdin(vm *goja.Runtime, stdin io.WriteCloser) *goja.Object {
	stream := newEventEmitter(vm)
	_ = stream.Set("writable", true)
	_ = stream.Set("destroyed", false)
	_ = stream.Set("write", func(call goja.FunctionCall) goja.Value {
		_, err := stdin.Write(buffer.Bytes(vm, call.Argument(0)))
		if callback, ok := goja.AssertFunction(call.Argument(2)); ok {
			if err != nil {
				_, _ = callback(goja.Undefined(), vm.NewGoError(err))
			} else {
				_, _ = callback(goja.Undefined())
			}
		}
		return vm.ToValue(err == nil)
	})
	_ = stream.Set("end", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			_, _ = stdin.Write(buffer.Bytes(vm, call.Argument(0)))
		}
		_ = stdin.Close()
		_ = stream.Set("writable", false)
		return goja.Undefined()
	})
	_ = stream.Set("destroy", func() { _ = stdin.Close(); _ = stream.Set("destroyed", true) })
	return stream
}

func (r *Runtime) pipeChildOutput(reader io.Reader, stream *goja.Object, encoding string) {
	go func() {
		data := make([]byte, 32*1024)
		for {
			count, err := reader.Read(data)
			if count > 0 {
				chunk := append([]byte(nil), data[:count]...)
				r.loop.RunOnLoop(func(vm *goja.Runtime) {
					value := encodeChildOutput(vm, chunk, encoding)
					r.runAsyncJob(vm, "child_process stream", func() error { return emitEvent(vm, stream, "data", value) })
				})
			}
			if err != nil {
				r.loop.RunOnLoop(func(vm *goja.Runtime) {
					_ = stream.Set("readable", false)
					_ = emitEvent(vm, stream, "end")
					_ = emitEvent(vm, stream, "close")
				})
				return
			}
		}
	}()
	_ = stream.Set("readable", true)
	_ = stream.Set("destroyed", false)
	_ = stream.Set("setEncoding", func(value string) *goja.Object { encoding = value; return stream })
	_ = stream.Set("pause", func() *goja.Object { return stream })
	_ = stream.Set("resume", func() *goja.Object { return stream })
}

func (r *Runtime) execChild(vm *goja.Runtime, command string, arguments []string, options childCommandOptions, callback goja.Callable) *goja.Object {
	child := newEventEmitter(vm)
	_ = child.Set("pid", goja.Undefined())
	_ = child.Set("killed", false)
	cmd, cancel := childExecCommand(command, arguments, options)
	cmd.Dir, cmd.Env = options.cwd, options.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		r.loop.SetTimeout(func(vm *goja.Runtime) {
			_, _ = callback(goja.Undefined(), childProcessError(vm, err, -1, nil, nil), goja.Undefined(), goja.Undefined())
			_ = emitEvent(vm, child, "error", vm.NewGoError(err))
		}, 0)
		return child
	}
	_ = child.Set("pid", cmd.Process.Pid)
	resourceID := r.addNodeResource(func() { _ = cmd.Process.Kill(); cancel() })
	if resourceID == 0 {
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
		cancel()
		r.removeNodeResource(resourceID)
		exitCode := 0
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
		r.loop.RunOnLoop(func(vm *goja.Runtime) {
			r.runAsyncJob(vm, "child_process.exec", func() error {
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
				_ = emitEvent(vm, child, "exit", exitCode, goja.Null())
				return emitEvent(vm, child, "close", exitCode, goja.Null())
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

func (r *Runtime) runChildSync(command string, arguments []string, options childCommandOptions) childSyncResult {
	cmd, cancel := childExecCommand(command, arguments, options)
	defer cancel()
	cmd.Dir, cmd.Env = options.cwd, options.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := childSyncResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), err: err, exitCode: 0}
	if cmd.Process != nil {
		result.pid = cmd.Process.Pid
	}
	if exitError, ok := err.(*exec.ExitError); ok {
		result.exitCode = exitError.ExitCode()
	} else if err != nil {
		result.exitCode = -1
	}
	return result
}

func (r *Runtime) spawnChildSync(vm *goja.Runtime, command string, arguments []string, options childCommandOptions) *goja.Object {
	result := r.runChildSync(command, arguments, options)
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
	_ = object.Set("code", exitCode)
	_ = object.Set("killed", false)
	_ = object.Set("signal", goja.Null())
	_ = object.Set("stdout", encodeChildOutput(vm, stdout, ""))
	_ = object.Set("stderr", encodeChildOutput(vm, stderr, ""))
	return object
}
