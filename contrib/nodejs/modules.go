package nodejs

import (
	"errors"
	"fmt"
	stdfs "io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	gojabuffer "github.com/dop251/goja_nodejs/buffer"
	"github.com/ewhauser/gbash/commands"
)

func (rt *nodeRuntime) processModule() (*goja.Object, error) {
	if rt.processExports != nil {
		return rt.processExports, nil
	}

	env := rt.vm.NewObject()
	for key, value := range rt.inv.Env {
		if err := env.Set(key, value); err != nil {
			return nil, err
		}
	}

	argvValues := make([]any, 0, len(rt.args)+2)
	for _, value := range rt.scriptArgv() {
		argvValues = append(argvValues, value)
	}
	argv := rt.vm.NewArray(argvValues...)

	obj := rt.vm.NewObject()
	_ = obj.Set("argv", argv)
	_ = obj.Set("env", env)
	_ = obj.Set("cwd", func(goja.FunctionCall) goja.Value {
		rt.ensureContext()
		return rt.vm.ToValue(rt.inv.Cwd)
	})
	_ = obj.Set("exit", func(call goja.FunctionCall) goja.Value {
		rt.ensureContext()
		code := 0
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) {
			code = int(call.Argument(0).ToInteger())
		}
		rt.vm.Interrupt(processExitSignal{code: code})
		return goja.Undefined()
	})
	_ = obj.Set("title", "nodejs")

	rt.processExports = obj
	return obj, nil
}

func (rt *nodeRuntime) fsModule() *goja.Object {
	if rt.fsExports != nil {
		return rt.fsExports
	}

	obj := rt.vm.NewObject()
	_ = obj.Set("readFileSync", rt.readFileSync)
	_ = obj.Set("writeFileSync", rt.writeFileSync)
	_ = obj.Set("readdirSync", rt.readDirSync)
	_ = obj.Set("statSync", rt.statSync)
	_ = obj.Set("lstatSync", rt.lstatSync)
	_ = obj.Set("existsSync", rt.existsSync)
	_ = obj.Set("mkdirSync", rt.mkdirSync)
	_ = obj.Set("rmSync", rt.rmSync)
	_ = obj.Set("unlinkSync", rt.unlinkSync)
	_ = obj.Set("rmdirSync", rt.rmdirSync)
	_ = obj.Set("renameSync", rt.renameSync)
	_ = obj.Set("readlinkSync", rt.readlinkSync)
	_ = obj.Set("realpathSync", rt.realpathSync)

	rt.fsExports = obj
	_ = freezeObject(rt.vm, obj)
	return obj
}

func (rt *nodeRuntime) pathModule() *goja.Object {
	if rt.pathExports != nil {
		return rt.pathExports
	}

	obj := rt.vm.NewObject()
	_ = obj.Set("sep", "/")
	_ = obj.Set("delimiter", ":")
	_ = obj.Set("basename", rt.pathBasename)
	_ = obj.Set("dirname", rt.pathDirname)
	_ = obj.Set("extname", rt.pathExtname)
	_ = obj.Set("isAbsolute", rt.pathIsAbsolute)
	_ = obj.Set("join", rt.pathJoin)
	_ = obj.Set("normalize", rt.pathNormalize)
	_ = obj.Set("resolve", rt.pathResolve)
	_ = obj.Set("posix", obj)

	rt.pathExports = obj
	_ = freezeObject(rt.vm, obj)
	return obj
}

func (rt *nodeRuntime) readFileSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()

	name := rt.stringArg(call, 0, "path")
	data, err := rt.readFileData(name)
	if err != nil {
		rt.throw(err)
	}

	encoding, ok, err := rt.readEncodingOption(call.Argument(1))
	if err != nil {
		rt.throw(err)
	}
	if ok {
		return rt.vm.ToValue(rt.decodeText(data, encoding))
	}
	return gojabuffer.WrapBytes(rt.vm, data)
}

func (rt *nodeRuntime) writeFileSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()

	name := rt.stringArg(call, 0, "path")
	data, err := rt.bytesArg(call, 1, "data", call.Argument(2))
	if err != nil {
		rt.throw(err)
	}
	abs := rt.inv.FS.Resolve(name)
	file, err := rt.inv.FS.OpenFile(rt.ctx, abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		rt.throw(err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(data); err != nil {
		rt.throw(&commands.ExitError{Code: 1, Err: err})
	}
	return goja.Undefined()
}

func (rt *nodeRuntime) readDirSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()

	name := rt.stringArg(call, 0, "path")
	if options := call.Argument(1); !goja.IsUndefined(options) && !goja.IsNull(options) {
		if options.ToObject(rt.vm).Get("withFileTypes").ToBoolean() {
			rt.throw(fmt.Errorf("nodejs: fs.readdirSync with withFileTypes is not supported"))
		}
	}

	entries, err := rt.inv.FS.ReadDir(rt.ctx, name)
	if err != nil {
		rt.throw(err)
	}
	values := make([]any, 0, len(entries))
	for _, entry := range entries {
		values = append(values, rt.vm.ToValue(entry.Name()))
	}
	return rt.vm.NewArray(values...)
}

func (rt *nodeRuntime) statSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()
	name := rt.stringArg(call, 0, "path")
	info, err := rt.inv.FS.Stat(rt.ctx, name)
	if err != nil {
		rt.throw(err)
	}
	return rt.statObject(info)
}

func (rt *nodeRuntime) lstatSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()
	name := rt.stringArg(call, 0, "path")
	info, err := rt.inv.FS.Lstat(rt.ctx, name)
	if err != nil {
		rt.throw(err)
	}
	return rt.statObject(info)
}

func (rt *nodeRuntime) existsSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()
	name := rt.stringArg(call, 0, "path")
	_, err := rt.inv.FS.Stat(rt.ctx, name)
	switch {
	case err == nil:
		return rt.vm.ToValue(true)
	case errors.Is(err, stdfs.ErrNotExist):
		return rt.vm.ToValue(false)
	default:
		rt.throw(err)
	}
	return goja.Undefined()
}

func (rt *nodeRuntime) mkdirSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()

	name := rt.stringArg(call, 0, "path")
	recursive := false
	mode := os.FileMode(0o755)
	if arg := call.Argument(1); !goja.IsUndefined(arg) && !goja.IsNull(arg) {
		switch value := arg.Export().(type) {
		case bool:
			recursive = value
		default:
			obj := arg.ToObject(rt.vm)
			recursive = obj.Get("recursive").ToBoolean()
			parsed, err := parseUintMode(obj.Get("mode"))
			if err != nil {
				rt.throw(fmt.Errorf("nodejs: invalid mkdir mode: %w", err))
			}
			mode = parsed
		}
	}

	abs := rt.inv.FS.Resolve(name)
	info, err := rt.inv.FS.Stat(rt.ctx, abs)
	switch {
	case err == nil && info.IsDir() && recursive:
		return goja.Undefined()
	case err == nil && info.IsDir():
		rt.throw(fmt.Errorf("nodejs: %s: file already exists", name))
	case err == nil:
		rt.throw(fmt.Errorf("nodejs: %s: file already exists", name))
	case !errors.Is(err, stdfs.ErrNotExist):
		rt.throw(err)
	}

	if !recursive {
		if err := rt.ensureParentDir(abs); err != nil {
			rt.throw(err)
		}
	}
	if err := rt.inv.FS.MkdirAll(rt.ctx, abs, mode); err != nil {
		rt.throw(err)
	}
	return goja.Undefined()
}

func (rt *nodeRuntime) rmSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()

	name := rt.stringArg(call, 0, "path")
	recursive, force := false, false
	if arg := call.Argument(1); !goja.IsUndefined(arg) && !goja.IsNull(arg) {
		obj := arg.ToObject(rt.vm)
		recursive = obj.Get("recursive").ToBoolean()
		force = obj.Get("force").ToBoolean()
	}

	if err := rt.inv.FS.Remove(rt.ctx, name, recursive); err != nil {
		if force && errors.Is(err, stdfs.ErrNotExist) {
			return goja.Undefined()
		}
		rt.throw(err)
	}
	return goja.Undefined()
}

func (rt *nodeRuntime) unlinkSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()
	name := rt.stringArg(call, 0, "path")
	if err := rt.inv.FS.Remove(rt.ctx, name, false); err != nil {
		rt.throw(err)
	}
	return goja.Undefined()
}

func (rt *nodeRuntime) rmdirSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()
	name := rt.stringArg(call, 0, "path")
	recursive := false
	if arg := call.Argument(1); !goja.IsUndefined(arg) && !goja.IsNull(arg) {
		switch value := arg.Export().(type) {
		case bool:
			recursive = value
		default:
			recursive = arg.ToObject(rt.vm).Get("recursive").ToBoolean()
		}
	}
	if err := rt.inv.FS.Remove(rt.ctx, name, recursive); err != nil {
		rt.throw(err)
	}
	return goja.Undefined()
}

func (rt *nodeRuntime) renameSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()
	oldName := rt.stringArg(call, 0, "oldPath")
	newName := rt.stringArg(call, 1, "newPath")
	if err := rt.inv.FS.Rename(rt.ctx, oldName, newName); err != nil {
		rt.throw(err)
	}
	return goja.Undefined()
}

func (rt *nodeRuntime) readlinkSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()
	name := rt.stringArg(call, 0, "path")
	value, err := rt.inv.FS.Readlink(rt.ctx, name)
	if err != nil {
		rt.throw(err)
	}
	return rt.vm.ToValue(value)
}

func (rt *nodeRuntime) realpathSync(call goja.FunctionCall) goja.Value {
	rt.ensureContext()
	name := rt.stringArg(call, 0, "path")
	value, err := rt.inv.FS.Realpath(rt.ctx, name)
	if err != nil {
		rt.throw(err)
	}
	return rt.vm.ToValue(value)
}

func (rt *nodeRuntime) pathBasename(call goja.FunctionCall) goja.Value {
	name := rt.stringArg(call, 0, "path")
	base := path.Base(name)
	if ext := call.Argument(1).String(); ext != "" && strings.HasSuffix(base, ext) {
		base = strings.TrimSuffix(base, ext)
	}
	return rt.vm.ToValue(base)
}

func (rt *nodeRuntime) pathDirname(call goja.FunctionCall) goja.Value {
	name := rt.stringArg(call, 0, "path")
	return rt.vm.ToValue(path.Dir(name))
}

func (rt *nodeRuntime) pathExtname(call goja.FunctionCall) goja.Value {
	name := rt.stringArg(call, 0, "path")
	return rt.vm.ToValue(path.Ext(name))
}

func (rt *nodeRuntime) pathIsAbsolute(call goja.FunctionCall) goja.Value {
	name := rt.stringArg(call, 0, "path")
	return rt.vm.ToValue(strings.HasPrefix(name, "/"))
}

func (rt *nodeRuntime) pathJoin(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) == 0 {
		return rt.vm.ToValue(".")
	}
	parts := make([]string, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		parts = append(parts, arg.String())
	}
	return rt.vm.ToValue(path.Join(parts...))
}

func (rt *nodeRuntime) pathNormalize(call goja.FunctionCall) goja.Value {
	name := rt.stringArg(call, 0, "path")
	if name == "" {
		return rt.vm.ToValue(".")
	}
	return rt.vm.ToValue(path.Clean(name))
}

func (rt *nodeRuntime) pathResolve(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) == 0 {
		return rt.vm.ToValue(rt.inv.Cwd)
	}
	parts := make([]string, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		parts = append(parts, arg.String())
	}
	resolved := ""
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if strings.TrimSpace(part) == "" {
			continue
		}
		if resolved == "" {
			resolved = part
		} else {
			resolved = path.Join(part, resolved)
		}
		if strings.HasPrefix(part, "/") {
			return rt.vm.ToValue(path.Clean(resolved))
		}
	}
	if resolved == "" {
		return rt.vm.ToValue(rt.inv.Cwd)
	}
	return rt.vm.ToValue(path.Clean(path.Join(rt.inv.Cwd, resolved)))
}

func (rt *nodeRuntime) stringArg(call goja.FunctionCall, index int, name string) string {
	if len(call.Arguments) <= index || goja.IsUndefined(call.Argument(index)) || goja.IsNull(call.Argument(index)) {
		rt.throw(rt.vm.NewTypeError("%s is required", name))
	}
	return call.Argument(index).String()
}

func (rt *nodeRuntime) bytesArg(call goja.FunctionCall, index int, name string, encodingArg goja.Value) ([]byte, error) {
	if len(call.Arguments) <= index || goja.IsUndefined(call.Argument(index)) || goja.IsNull(call.Argument(index)) {
		return nil, fmt.Errorf("%s is required", name)
	}

	value := call.Argument(index)
	if exported, ok := value.Export().(string); ok {
		encoding, ok, err := rt.readEncodingOption(encodingArg)
		if err != nil {
			return nil, err
		}
		if ok {
			if normalized := normalizeEncoding(encoding); normalized != "" {
				return []byte(exported), nil
			}
		}
		return []byte(exported), nil
	}
	return gojabuffer.DecodeBytes(rt.vm, value, encodingArg), nil
}

func (rt *nodeRuntime) readEncodingOption(value goja.Value) (encoding string, present bool, err error) {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return "", false, nil
	}
	switch exported := value.Export().(type) {
	case string:
		return normalizeEncoding(exported), true, validateEncoding(exported)
	default:
		obj := value.ToObject(rt.vm)
		encoding := obj.Get("encoding")
		if goja.IsUndefined(encoding) || goja.IsNull(encoding) {
			return "", false, nil
		}
		return normalizeEncoding(encoding.String()), true, validateEncoding(encoding.String())
	}
}

func validateEncoding(value string) error {
	switch normalizeEncoding(value) {
	case "", "utf8":
		return nil
	default:
		return fmt.Errorf("nodejs: unsupported encoding %q", value)
	}
}

func normalizeEncoding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "utf8", "utf-8":
		return "utf8"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (rt *nodeRuntime) decodeText(data []byte, encoding string) string {
	switch normalizeEncoding(encoding) {
	case "", "utf8":
		return string(data)
	default:
		rt.throw(fmt.Errorf("nodejs: unsupported encoding %q", encoding))
	}
	return ""
}

func (rt *nodeRuntime) readFileData(name string) ([]byte, error) {
	file, err := rt.inv.FS.Open(rt.ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	data, err := commands.ReadAll(rt.ctx, rt.inv, file)
	if err != nil {
		if isMaxFileBytesError(rt.inv, err) {
			return nil, fmt.Errorf("nodejs: %s: file too large (max %d bytes)", name, maxFileBytes(rt.inv))
		}
		return nil, err
	}
	return data, nil
}

func (rt *nodeRuntime) ensureParentDir(abs string) error {
	parent := path.Dir(abs)
	info, err := rt.inv.FS.Stat(rt.ctx, parent)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("nodejs: %s: Not a directory", parent)
	}
	return nil
}

func (rt *nodeRuntime) statObject(info stdfs.FileInfo) goja.Value {
	obj := rt.vm.NewObject()
	_ = obj.Set("size", info.Size())
	_ = obj.Set("mode", int64(info.Mode()))
	_ = obj.Set("mtimeMs", float64(info.ModTime().UnixNano())/float64(time.Millisecond))
	_ = obj.Set("isFile", func(goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(info.Mode().IsRegular())
	})
	_ = obj.Set("isDirectory", func(goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(info.IsDir())
	})
	_ = obj.Set("isSymbolicLink", func(goja.FunctionCall) goja.Value {
		return rt.vm.ToValue(info.Mode()&stdfs.ModeSymlink != 0)
	})
	_ = freezeObject(rt.vm, obj)
	return obj
}

func (rt *nodeRuntime) throw(value any) {
	switch current := value.(type) {
	case error:
		panic(rt.vm.NewGoError(current))
	case goja.Value:
		panic(current)
	default:
		panic(rt.vm.NewGoError(fmt.Errorf("%v", current)))
	}
}

func parseUintMode(value goja.Value) (os.FileMode, error) {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return 0o755, nil
	}
	switch exported := value.Export().(type) {
	case int64:
		return os.FileMode(exported), nil
	case int32:
		return os.FileMode(exported), nil
	case int:
		return os.FileMode(exported), nil
	case float64:
		return os.FileMode(int64(exported)), nil
	case string:
		trimmed := strings.TrimSpace(exported)
		if trimmed == "" {
			return 0o755, nil
		}
		parsed, err := strconv.ParseUint(trimmed, 8, 32)
		if err != nil {
			return 0, err
		}
		return os.FileMode(parsed), nil
	default:
		return 0o755, nil
	}
}
