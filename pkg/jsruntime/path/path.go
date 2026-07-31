package path

import (
	"path"
	"runtime"
	"strings"

	"github.com/dop251/goja"
)

type Module struct {
	cwd func() string
}

func New(cwd func() string) *Module {
	return &Module{cwd: cwd}
}

func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	defaultPath := m.pathExports(vm, runtime.GOOS == "windows")
	_ = defaultPath.Set("posix", m.pathExports(vm, false))
	_ = defaultPath.Set("win32", m.pathExports(vm, true))
	_ = module.Set("exports", defaultPath)
}

func (m *Module) pathExports(vm *goja.Runtime, windows bool) *goja.Object {
	exports := vm.NewObject()
	separator, delimiter := "/", ":"
	if windows {
		separator, delimiter = `\`, ";"
	}
	normalize := func(value string) string {
		if windows {
			return winPathNormalize(value)
		}
		return posixPathNormalize(value)
	}
	join := func(parts []string) string {
		if windows {
			return winPathJoin(parts)
		}
		return posixPathJoin(parts)
	}
	resolve := func(parts []string) string {
		if windows {
			return winPathResolve(m.cwd(), parts)
		}
		return posixPathResolve(m.cwd(), parts)
	}
	dirname := func(value string) string {
		if windows {
			return winPathDirname(value)
		}
		return path.Dir(value)
	}
	basename := func(value string) string {
		if windows {
			return winPathBasename(value)
		}
		return posixPathBasename(value)
	}
	isAbs := func(value string) bool {
		if windows {
			_, root, _ := splitWinPath(value)
			return root != ""
		}
		return path.IsAbs(value)
	}
	extname := func(value string) string { return nodeExtname(value, windows) }

	_ = exports.Set("sep", separator)
	_ = exports.Set("delimiter", delimiter)
	_ = exports.Set("normalize", normalize)
	_ = exports.Set("isAbsolute", isAbs)
	_ = exports.Set("join", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for index, value := range call.Arguments {
			parts[index] = value.String()
		}
		return vm.ToValue(join(parts))
	})
	_ = exports.Set("resolve", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for index, value := range call.Arguments {
			parts[index] = value.String()
		}
		return vm.ToValue(resolve(parts))
	})
	_ = exports.Set("relative", func(from, to string) string {
		if windows {
			return winPathRelative(m.cwd(), from, to)
		}
		return posixPathRelative(m.cwd(), from, to)
	})
	_ = exports.Set("dirname", dirname)
	_ = exports.Set("basename", func(call goja.FunctionCall) goja.Value {
		result := basename(call.Argument(0).String())
		if suffix := call.Argument(1); !goja.IsUndefined(suffix) && strings.HasSuffix(result, suffix.String()) {
			result = strings.TrimSuffix(result, suffix.String())
		}
		return vm.ToValue(result)
	})
	_ = exports.Set("extname", extname)
	_ = exports.Set("parse", func(value string) map[string]string {
		if windows {
			return winPathParse(value)
		}
		return posixPathParse(value)
	})
	_ = exports.Set("format", func(call goja.FunctionCall) goja.Value {
		object := call.Argument(0).ToObject(vm)
		resultDir := jsStringProperty(object, "dir")
		if resultDir == "" {
			resultDir = jsStringProperty(object, "root")
		}
		resultBase := jsStringProperty(object, "base")
		if resultBase == "" {
			extension := jsStringProperty(object, "ext")
			if extension != "" && !strings.HasPrefix(extension, ".") {
				extension = "." + extension
			}
			resultBase = jsStringProperty(object, "name") + extension
		}
		if resultDir == "" {
			return vm.ToValue(resultBase)
		}
		return vm.ToValue(join([]string{resultDir, resultBase}))
	})
	_ = exports.Set("toNamespacedPath", func(value string) string {
		if !windows || value == "" {
			return value
		}
		resolved := winPathResolve(m.cwd(), []string{value})
		if strings.HasPrefix(resolved, `\\?\`) || strings.HasPrefix(resolved, `\\.\`) {
			return resolved
		}
		if strings.HasPrefix(resolved, `\\`) {
			return `\\?\UNC\` + strings.TrimLeft(resolved, `\`)
		}
		if winDriveVolume(resolved) != "" {
			return `\\?\` + resolved
		}
		return value
	})
	return exports
}

func jsStringProperty(object *goja.Object, name string) string {
	value := object.Get(name)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	return value.String()
}

func posixPathResolve(cwd string, parts []string) string {
	cwd = strings.ReplaceAll(cwd, `\`, "/")
	if volume := winDriveVolume(cwd); volume != "" {
		cwd = strings.TrimPrefix(cwd, volume)
	}
	if !strings.HasPrefix(cwd, "/") {
		cwd = "/" + cwd
	}
	resolved := ""
	absolute := false
	for index := len(parts) - 1; index >= -1 && !absolute; index-- {
		part := cwd
		if index >= 0 {
			part = parts[index]
		}
		if part == "" {
			continue
		}
		resolved = part + "/" + resolved
		absolute = strings.HasPrefix(part, "/")
	}
	result := path.Clean(resolved)
	if !strings.HasPrefix(result, "/") {
		result = "/" + strings.TrimLeft(result, "/")
	}
	return result
}

func posixPathNormalize(value string) string {
	if value == "" {
		return "."
	}
	trailingSeparator := strings.HasSuffix(value, "/")
	result := path.Clean(value)
	if trailingSeparator && result != "/" {
		result += "/"
	}
	return result
}

func posixPathJoin(parts []string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	if len(nonEmpty) == 0 {
		return "."
	}
	return posixPathNormalize(strings.Join(nonEmpty, "/"))
}

func posixPathParts(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func posixPathRelative(cwd, from, to string) string {
	from = posixPathResolve(cwd, []string{from})
	to = posixPathResolve(cwd, []string{to})
	if from == to {
		return ""
	}
	fromParts := posixPathParts(from)
	toParts := posixPathParts(to)
	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	result := make([]string, 0, len(fromParts)+len(toParts))
	for range len(fromParts) - common {
		result = append(result, "..")
	}
	result = append(result, toParts[common:]...)
	return strings.Join(result, "/")
}

func posixPathBasename(value string) string {
	end := len(value)
	for end > 0 && value[end-1] == '/' {
		end--
	}
	if end == 0 {
		return ""
	}
	start := end
	for start > 0 && value[start-1] != '/' {
		start--
	}
	return value[start:end]
}

func posixPathParse(value string) map[string]string {
	root := ""
	rootEnd := 0
	if strings.HasPrefix(value, "/") {
		root, rootEnd = "/", 1
	}
	end := len(value)
	for end > rootEnd && value[end-1] == '/' {
		end--
	}
	start := end
	for start > rootEnd && value[start-1] != '/' {
		start--
	}
	base := value[start:end]
	dir := ""
	if start == rootEnd {
		dir = root
	} else if start > 0 {
		dir = value[:start-1]
		if dir == "" && root != "" {
			dir = root
		}
	}
	extension := nodeExtname(base, false)
	return map[string]string{"root": root, "dir": dir, "base": base, "ext": extension, "name": strings.TrimSuffix(base, extension)}
}

func isWinSeparator(character byte) bool { return character == '\\' || character == '/' }

func winDriveVolume(value string) string {
	if len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' {
		return value[:2]
	}
	return ""
}

func splitWinPath(value string) (volume, root, rest string) {
	value = strings.ReplaceAll(value, "/", `\`)
	if drive := winDriveVolume(value); drive != "" {
		volume, rest = drive, value[len(drive):]
		if rest != "" && isWinSeparator(rest[0]) {
			root, rest = `\`, strings.TrimLeft(rest, `\`)
		}
		return
	}
	if strings.HasPrefix(value, `\\`) {
		parts := strings.FieldsFunc(strings.TrimLeft(value, `\`), func(character rune) bool { return character == '\\' })
		if len(parts) >= 2 {
			volume = `\\` + parts[0] + `\` + parts[1]
			root = `\`
			prefix := strings.TrimLeft(value, `\`)
			prefix = strings.TrimPrefix(prefix, parts[0])
			prefix = strings.TrimLeft(prefix, `\`)
			prefix = strings.TrimPrefix(prefix, parts[1])
			rest = strings.TrimLeft(prefix, `\`)
			return
		}
	}
	if strings.HasPrefix(value, `\`) {
		return "", `\`, strings.TrimLeft(value, `\`)
	}
	return "", "", value
}

func cleanWinParts(rest string, absolute bool) []string {
	parts := strings.FieldsFunc(rest, func(character rune) bool { return character == '\\' || character == '/' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
		case "..":
			if len(result) > 0 && result[len(result)-1] != ".." {
				result = result[:len(result)-1]
			} else if !absolute {
				result = append(result, part)
			}
		default:
			result = append(result, part)
		}
	}
	return result
}

func winPathNormalize(value string) string {
	if value == "" {
		return "."
	}
	trailing := isWinSeparator(value[len(value)-1])
	volume, root, rest := splitWinPath(value)
	parts := cleanWinParts(rest, root != "")
	result := volume + root + strings.Join(parts, `\`)
	if result == "" {
		result = "."
	} else if volume != "" && root == "" && len(parts) == 0 {
		result = volume + "."
	}
	if trailing && len(parts) > 0 && !strings.HasSuffix(result, `\`) {
		result += `\`
	}
	return result
}

func winPathJoin(parts []string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	if len(nonEmpty) == 0 {
		return "."
	}
	return winPathNormalize(strings.Join(nonEmpty, `\`))
}

func winPathResolve(cwd string, parts []string) string {
	resolvedDevice, resolvedTail := "", ""
	resolvedAbsolute := false
	for index := len(parts) - 1; index >= -1; index-- {
		part := ""
		if index >= 0 {
			part = parts[index]
		} else if resolvedDevice == "" {
			part = cwd
		} else {
			cwdDevice, cwdRoot, _ := splitWinPath(cwd)
			if strings.EqualFold(cwdDevice, resolvedDevice) && cwdRoot != "" {
				part = cwd
			} else {
				part = resolvedDevice + `\`
			}
		}
		if part == "" {
			continue
		}
		device, root, rest := splitWinPath(part)
		if device != "" {
			if resolvedDevice != "" && !strings.EqualFold(device, resolvedDevice) {
				continue
			}
			resolvedDevice = device
		}
		if !resolvedAbsolute {
			if rest != "" {
				resolvedTail = rest + `\` + resolvedTail
			}
			resolvedAbsolute = root != ""
		}
		if resolvedAbsolute && resolvedDevice != "" {
			break
		}
	}
	tail := strings.Join(cleanWinParts(resolvedTail, resolvedAbsolute), `\`)
	if resolvedAbsolute {
		prefix := `\`
		if resolvedDevice != "" {
			prefix = resolvedDevice + `\`
		}
		return prefix + tail
	}
	result := resolvedDevice + tail
	if result == "" {
		return "."
	}
	return result
}

func winPathRelative(cwd, from, to string) string {
	from = winPathResolve(cwd, []string{from})
	to = winPathResolve(cwd, []string{to})
	if strings.EqualFold(from, to) {
		return ""
	}
	fromVolume, _, fromRest := splitWinPath(from)
	toVolume, _, toRest := splitWinPath(to)
	if !strings.EqualFold(fromVolume, toVolume) {
		return to
	}
	fromParts := cleanWinParts(fromRest, true)
	toParts := cleanWinParts(toRest, true)
	common := 0
	for common < len(fromParts) && common < len(toParts) && strings.EqualFold(fromParts[common], toParts[common]) {
		common++
	}
	result := make([]string, 0, len(fromParts)+len(toParts))
	for range len(fromParts) - common {
		result = append(result, "..")
	}
	result = append(result, toParts[common:]...)
	return strings.Join(result, `\`)
}

func winPathBasename(value string) string {
	value = strings.TrimRightFunc(value, func(character rune) bool { return character == '\\' || character == '/' })
	if value == "" {
		return ""
	}
	index := strings.LastIndexAny(value, `/\`)
	start := index + 1
	if drive := winDriveVolume(value); drive != "" && start < len(drive) {
		start = len(drive)
	}
	return value[start:]
}

func winPathParse(value string) map[string]string {
	value = strings.ReplaceAll(value, "/", `\`)
	volume, rootSeparator, rest := splitWinPath(value)
	if strings.HasPrefix(volume, `\\`) && rest == "" {
		root := volume
		if strings.HasSuffix(value, `\`) {
			root += `\`
		}
		return map[string]string{"root": root, "dir": root, "base": "", "ext": "", "name": ""}
	}
	root := volume + rootSeparator
	if volume != "" && rootSeparator == "" {
		root = volume
	}
	end := len(rest)
	for end > 0 && isWinSeparator(rest[end-1]) {
		end--
	}
	start := end
	for start > 0 && !isWinSeparator(rest[start-1]) {
		start--
	}
	base := rest[start:end]
	dir := ""
	if start == 0 {
		dir = root
	} else {
		dir = root + rest[:start-1]
	}
	extension := nodeExtname(base, true)
	return map[string]string{"root": root, "dir": dir, "base": base, "ext": extension, "name": strings.TrimSuffix(base, extension)}
}

func winPathDirname(value string) string {
	volume, root, _ := splitWinPath(value)
	trimmed := strings.TrimRightFunc(value, func(character rune) bool { return character == '\\' || character == '/' })
	index := strings.LastIndexAny(trimmed, `/\`)
	if index < len(volume) {
		if root != "" {
			return volume + root
		}
		if volume != "" {
			return volume
		}
		return "."
	}
	result := strings.TrimRightFunc(trimmed[:index], func(character rune) bool { return character == '\\' || character == '/' })
	if result == volume {
		return volume + root
	}
	return result
}

func nodeExtname(value string, windows bool) string {
	startDot, startPart, end := -1, 0, -1
	matchedSlash, preDotState := true, 0
	for index := len(value) - 1; index >= 0; index-- {
		character := value[index]
		separator := character == '/'
		if windows {
			separator = separator || character == '\\'
		}
		if separator {
			if !matchedSlash {
				startPart = index + 1
				break
			}
			continue
		}
		if end == -1 {
			matchedSlash = false
			end = index + 1
		}
		if character == '.' {
			if startDot == -1 {
				startDot = index
			} else if preDotState != 1 {
				preDotState = 1
			}
		} else if startDot != -1 {
			preDotState = -1
		}
	}
	if startDot == -1 || end == -1 || preDotState == 0 || (preDotState == 1 && startDot == end-1 && startDot == startPart+1) {
		return ""
	}
	return value[startDot:end]
}
