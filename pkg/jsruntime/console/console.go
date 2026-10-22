package console

import (
	"encoding/json"
	"fmt"
	moe "github.com/Calcium-Ion/moejs"
	e "github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	logger "github.com/komari-monitor/komari/utils/log"
	"io"
	"strings"
)

type level uint8

const (
	debug level = iota
	info
	warn
	errorLevel
)

type Module struct{ output io.Writer }

func New(w io.Writer) *Module { return &Module{output: w} }
func (m *Module) Inject(vm *moe.Runtime) error {
	o := vm.Realm().NewObject()
	for _, entry := range []struct {
		name  string
		level level
		stack bool
	}{{"debug", debug, false}, {"info", info, false}, {"log", info, false}, {"warn", warn, false}, {"error", errorLevel, false}, {"exception", errorLevel, false}, {"trace", debug, true}} {
		entry := entry
		if err := native.Function(vm, o, entry.name, func(_ *moe.Realm, _ moe.Value, args []moe.Value) (moe.Value, error) {
			m.write(vm, entry.level, args, entry.stack)
			return moe.Undefined(), nil
		}); err != nil {
			return err
		}
	}
	if err := native.Function(vm, o, "assert", func(_ *moe.Realm, _ moe.Value, args []moe.Value) (moe.Value, error) {
		if len(args) > 0 && native.Bool(args[0]) {
			return moe.Undefined(), nil
		}
		values := []moe.Value{moe.String("Assertion failed:")}
		if len(args) > 1 {
			values = append(values, args[1:]...)
		}
		m.write(vm, errorLevel, values, true)
		return moe.Undefined(), nil
	}); err != nil {
		return err
	}
	return vm.SetGlobal("console", e.ObjectValue(o))
}
func (m *Module) WriteError(vm *moe.Runtime, values []moe.Value, stack bool) {
	m.write(vm, errorLevel, values, stack)
}
func (m *Module) Report(vm *moe.Runtime, name string, err error) {
	m.WriteError(vm, []moe.Value{moe.String(fmt.Sprintf("%s callback failed: %v", name, err))}, false)
}
func (m *Module) write(vm *moe.Runtime, l level, values []moe.Value, withStack bool) {
	args := make([]any, len(values))
	for i, v := range values {
		x, err := vm.ToGo(v)
		if err != nil {
			x = native.String(vm, v)
		}
		args[i] = x
	}
	message := ""
	if len(args) > 0 {
		if format, ok := args[0].(string); ok && strings.Contains(format, "%") {
			message = formatConsoleString(format, args[1:])
		} else {
			parts := make([]string, len(args))
			for i, x := range args {
				parts[i] = formatConsoleValue(x)
			}
			message = strings.Join(parts, " ")
		}
	}
	if withStack {
		errObj := vm.Realm().NewError(e.KindError, "")
		stack := vm.Realm().StackTrace(e.ObjectValue(errObj))
		if stack != "" {
			message += "\n" + stack
		}
	}
	if m.output != nil {
		_, _ = fmt.Fprintln(m.output, message)
		return
	}
	switch l {
	case debug:
		logger.Debug("JavaScript", message)
	case warn:
		logger.Warn("JavaScript", message)
	case errorLevel:
		logger.Error("JavaScript", message)
	default:
		logger.Info("JavaScript", message)
	}
}
func formatConsoleValue(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case string:
		return value
	case error:
		return value.Error()
	}

	if data, err := json.Marshal(value); err == nil {
		return string(data)
	}
	return fmt.Sprint(value)
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
			builder.WriteString(formatConsoleValue(arg))
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
			remaining = append(remaining, formatConsoleValue(arg))
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
