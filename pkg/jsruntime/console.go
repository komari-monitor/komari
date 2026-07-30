package jsruntime

// JavaScript compatibility: console.assert(), debug(), error(), exception(),
// info(), log(), trace(), and warn(). Supports basic %s/%d/%i/%f/%o/%O/%c/%%
// substitutions. assert(false, ...) and trace() include a stack trace.

import (
	"fmt"
	"strings"

	"github.com/dop251/goja"
	logger "github.com/komari-monitor/komari/utils/log"
)

type consoleLevel uint8

const (
	consoleDebug consoleLevel = iota
	consoleInfo
	consoleWarn
	consoleError
)

func (r *Runtime) injectConsole() {
	console := r.vm.NewObject()
	console.Set("assert", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 && call.Argument(0).ToBoolean() {
			return goja.Undefined()
		}
		values := call.Arguments
		if len(values) <= 1 {
			values = []goja.Value{r.vm.ToValue("Assertion failed")}
		} else {
			values = append([]goja.Value{r.vm.ToValue("Assertion failed:")}, values[1:]...)
		}
		r.writeConsole(consoleError, values, true)
		return goja.Undefined()
	})
	console.Set("debug", func(call goja.FunctionCall) goja.Value {
		r.writeConsole(consoleDebug, call.Arguments, false)
		return goja.Undefined()
	})
	error := func(call goja.FunctionCall) goja.Value {
		r.writeConsole(consoleError, call.Arguments, false)
		return goja.Undefined()
	}
	console.Set("error", error)
	console.Set("exception", error)
	console.Set("info", func(call goja.FunctionCall) goja.Value {
		r.writeConsole(consoleInfo, call.Arguments, false)
		return goja.Undefined()
	})
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		r.writeConsole(consoleInfo, call.Arguments, false)
		return goja.Undefined()
	})
	console.Set("trace", func(call goja.FunctionCall) goja.Value {
		r.writeConsole(consoleDebug, call.Arguments, true)
		return goja.Undefined()
	})
	console.Set("warn", func(call goja.FunctionCall) goja.Value {
		r.writeConsole(consoleWarn, call.Arguments, false)
		return goja.Undefined()
	})
	r.vm.Set("console", console)
}

func (r *Runtime) writeConsole(level consoleLevel, values []goja.Value, withStack bool) {
	message := formatConsoleValues(values)
	if withStack {
		if stack := r.stackTrace(); stack != "" {
			if message != "" {
				message += "\n"
			}
			message += stack
		}
	}
	if r.console != nil {
		_, _ = fmt.Fprintln(r.console, message)
		return
	}
	switch level {
	case consoleDebug:
		logger.Debug("JavaScript", message)
	case consoleWarn:
		logger.Warn("JavaScript", message)
	case consoleError:
		logger.Error("JavaScript", message)
	default:
		logger.Info("JavaScript", message)
	}
}

func formatConsoleValues(values []goja.Value) string {
	if len(values) == 0 {
		return ""
	}
	args := make([]any, 0, len(values))
	for _, value := range values {
		args = append(args, value.Export())
	}
	if format, ok := args[0].(string); ok && strings.Contains(format, "%") {
		return formatConsoleString(format, args[1:])
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, fmt.Sprint(arg))
	}
	return strings.Join(parts, " ")
}

func formatConsoleString(format string, args []any) string {
	var builder strings.Builder
	argIndex := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			builder.WriteByte(format[i])
			continue
		}
		i++
		if format[i] == '%' {
			builder.WriteByte('%')
			continue
		}
		if format[i] == 'c' {
			if argIndex < len(args) {
				argIndex++
			}
			continue
		}
		if argIndex >= len(args) {
			builder.WriteByte('%')
			builder.WriteByte(format[i])
			continue
		}
		arg := args[argIndex]
		argIndex++
		switch format[i] {
		case 's', 'o', 'O':
			builder.WriteString(fmt.Sprint(arg))
		case 'd', 'i':
			builder.WriteString(fmt.Sprintf("%d", toConsoleInteger(arg)))
		case 'f':
			builder.WriteString(fmt.Sprintf("%f", toConsoleFloat(arg)))
		default:
			builder.WriteByte('%')
			builder.WriteByte(format[i])
			builder.WriteString(fmt.Sprint(arg))
		}
	}
	if argIndex < len(args) {
		remaining := make([]string, 0, len(args)-argIndex)
		for _, arg := range args[argIndex:] {
			remaining = append(remaining, fmt.Sprint(arg))
		}
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(strings.Join(remaining, " "))
	}
	return builder.String()
}

func toConsoleInteger(value any) int64 {
	switch value := value.(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func toConsoleFloat(value any) float64 {
	switch value := value.(type) {
	case float32:
		return float64(value)
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func (r *Runtime) stackTrace() string {
	if r.vm == nil {
		return ""
	}
	value, err := r.vm.RunString("new Error().stack")
	if err != nil {
		return ""
	}
	return value.String()
}
