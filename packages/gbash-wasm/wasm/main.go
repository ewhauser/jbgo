//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/ewhauser/gbash"
	gbfs "github.com/ewhauser/gbash/fs"
)

type browserShell struct {
	mu      sync.Mutex
	session *gbash.Session
	cwd     string
	env     map[string]string
	funcs   []js.Func
}

func main() {
	root := js.Global().Get("Object").New()
	root.Set("createShell", js.FuncOf(createShell))
	js.Global().Set("GBashWasm", root)
	select {}
}

func createShell(this js.Value, args []js.Value) any {
	options := js.Undefined()
	if len(args) > 0 {
		options = args[0]
	}
	return promise(func(resolve, reject js.Value) {
		shell, err := newBrowserShell(options)
		if err != nil {
			reject.Invoke(err.Error())
			return
		}
		resolve.Invoke(shell.jsObject())
	})
}

func newBrowserShell(options js.Value) (*browserShell, error) {
	cwd := cleanPath(valueOrDefault(options, "cwd", "/home/agent"))
	baseEnv := envFromOptions(options, cwd)
	initialFiles, err := initialFilesFromOptions(options)
	if err != nil {
		return nil, err
	}

	runtimeOptions := []gbash.Option{
		gbash.WithBaseEnv(baseEnv),
		gbash.WithWorkingDir(cwd),
	}
	if len(initialFiles) > 0 {
		runtimeOptions = append([]gbash.Option{
			gbash.WithFileSystem(gbash.SeededInMemoryFileSystem(initialFiles)),
		}, runtimeOptions...)
	}

	rt, err := gbash.New(runtimeOptions...)
	if err != nil {
		return nil, err
	}

	session, err := rt.NewSession(context.Background())
	if err != nil {
		return nil, err
	}

	shell := &browserShell{
		session: session,
		cwd:     cwd,
		env:     cloneEnv(baseEnv),
	}

	return shell, nil
}

func envFromOptions(options js.Value, cwd string) map[string]string {
	env := stringMapValue(options, "env")
	if len(env) == 0 {
		env = make(map[string]string)
	}
	if env["HOME"] == "" && strings.HasPrefix(cwd, "/home/") {
		env["HOME"] = cwd
	}
	if strings.HasPrefix(env["HOME"], "/home/") {
		user := path.Base(env["HOME"])
		if user != "." && user != "/" && user != "" {
			if env["USER"] == "" {
				env["USER"] = user
			}
			if env["LOGNAME"] == "" {
				env["LOGNAME"] = user
			}
			if env["GROUP"] == "" {
				env["GROUP"] = user
			}
		}
	}
	return env
}

func (s *browserShell) jsObject() js.Value {
	obj := js.Global().Get("Object").New()
	execFn := js.FuncOf(func(this js.Value, args []js.Value) any {
		command := ""
		if len(args) > 0 {
			command = args[0].String()
		}
		return promise(func(resolve, reject js.Value) {
			go func() {
				result, err := s.exec(command)
				if err != nil {
					reject.Invoke(err.Error())
					return
				}
				resolve.Invoke(result)
			}()
		})
	})
	writeFileFn := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 2 {
			panic("writeFile requires path and content")
		}
		if err := s.writeFileSync(args[0].String(), args[1].String()); err != nil {
			panic(err.Error())
		}
		return nil
	})
	readFileFn := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			panic("readFile requires a path")
		}
		content, err := s.readFileSync(args[0].String())
		if err != nil {
			panic(err.Error())
		}
		return content
	})
	disposeFn := js.FuncOf(func(this js.Value, args []js.Value) any {
		for _, fn := range s.funcs {
			fn.Release()
		}
		s.funcs = nil
		return nil
	})

	s.funcs = append(s.funcs, execFn, writeFileFn, readFileFn, disposeFn)
	obj.Set("exec", execFn)
	obj.Set("writeFile", writeFileFn)
	obj.Set("readFile", readFileFn)
	obj.Set("dispose", disposeFn)
	return obj
}

func (s *browserShell) exec(command string) (js.Value, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.session.Exec(context.Background(), &gbash.ExecutionRequest{
		Script:     command + "\n",
		WorkDir:    s.cwd,
		Env:        cloneEnv(s.env),
		Stdin:      os.Stdin,
		ReplaceEnv: true,
	})
	if err != nil {
		return js.Value{}, err
	}
	if len(result.FinalEnv) > 0 {
		s.env = cloneEnv(result.FinalEnv)
		if pwd := strings.TrimSpace(result.FinalEnv["PWD"]); pwd != "" {
			s.cwd = cleanPath(pwd)
		}
	}
	return resultValue(result), nil
}

func (s *browserShell) writeFileSync(name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeFileLocked(name, content)
}

func (s *browserShell) writeFileLocked(name, content string) error {
	abs := resolvePath(s.cwd, name)
	fsys := s.session.FileSystem()
	if fsys == nil {
		return fmt.Errorf("gbash wasm shell has no filesystem")
	}
	if err := fsys.MkdirAll(context.Background(), path.Dir(abs), 0o755); err != nil {
		return err
	}
	file, err := fsys.OpenFile(context.Background(), abs, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = io.WriteString(file, content)
	return err
}

func (s *browserShell) readFileSync(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	abs := resolvePath(s.cwd, name)
	fsys := s.session.FileSystem()
	if fsys == nil {
		return "", fmt.Errorf("gbash wasm shell has no filesystem")
	}
	file, err := fsys.Open(context.Background(), abs)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func resultValue(result *gbash.ExecutionResult) js.Value {
	obj := js.Global().Get("Object").New()
	if result == nil {
		obj.Set("stdout", "")
		obj.Set("stderr", "")
		obj.Set("exitCode", 1)
		return obj
	}
	obj.Set("stdout", result.Stdout)
	obj.Set("stderr", result.Stderr)
	obj.Set("exitCode", result.ExitCode)
	obj.Set("shellExited", result.ShellExited)
	return obj
}

func promise(run func(resolve, reject js.Value)) js.Value {
	constructor := js.Global().Get("Promise")
	executor := js.FuncOf(func(this js.Value, args []js.Value) any {
		run(args[0], args[1])
		return nil
	})
	defer executor.Release()
	return constructor.New(executor)
}

func valueOrDefault(value js.Value, key, fallback string) string {
	if value.IsUndefined() || value.IsNull() || value.Type() != js.TypeObject {
		return fallback
	}
	field := value.Get(key)
	if field.Type() != js.TypeString {
		return fallback
	}
	text := strings.TrimSpace(field.String())
	if text == "" {
		return fallback
	}
	return text
}

func stringMapValue(value js.Value, key string) map[string]string {
	if value.IsUndefined() || value.IsNull() || value.Type() != js.TypeObject {
		return nil
	}
	field := value.Get(key)
	if field.IsUndefined() || field.IsNull() || field.Type() != js.TypeObject {
		return nil
	}
	keys := js.Global().Get("Object").Call("keys", field)
	out := make(map[string]string, keys.Length())
	for i := 0; i < keys.Length(); i++ {
		name := keys.Index(i).String()
		out[name] = field.Get(name).String()
	}
	return out
}

func initialFilesFromOptions(value js.Value) (gbfs.InitialFiles, error) {
	if value.IsUndefined() || value.IsNull() || value.Type() != js.TypeObject {
		return nil, nil
	}
	field := value.Get("files")
	if field.IsUndefined() || field.IsNull() || field.Type() != js.TypeObject {
		return nil, nil
	}

	keys := js.Global().Get("Object").Call("keys", field)
	out := make(gbfs.InitialFiles, keys.Length())
	for i := 0; i < keys.Length(); i++ {
		name := keys.Index(i).String()
		file, err := initialFileFromJSValue(field.Get(name))
		if err != nil {
			return nil, fmt.Errorf("files[%q]: %w", name, err)
		}
		out[name] = file
	}
	return out, nil
}

func initialFileFromJSValue(value js.Value) (gbfs.InitialFile, error) {
	switch {
	case value.Type() == js.TypeString:
		return gbfs.InitialFile{Content: []byte(value.String())}, nil
	case isUint8Array(value):
		content, err := bytesFromJSValue(value)
		if err != nil {
			return gbfs.InitialFile{}, err
		}
		return gbfs.InitialFile{Content: content}, nil
	case value.Type() == js.TypeFunction:
		return gbfs.InitialFile{Lazy: lazyProviderFromJS(value)}, nil
	case value.Type() != js.TypeObject || value.IsNull():
		return gbfs.InitialFile{}, fmt.Errorf("unsupported file value type %s", value.Type())
	}

	contentField := value.Get("content")
	lazyField := value.Get("lazy")
	hasContent := isDefined(contentField)
	hasLazy := isDefined(lazyField)
	if hasContent == hasLazy {
		return gbfs.InitialFile{}, fmt.Errorf("expected exactly one of content or lazy")
	}

	var file gbfs.InitialFile
	var err error
	if hasContent {
		file.Content, err = bytesFromJSValue(contentField)
		if err != nil {
			return gbfs.InitialFile{}, err
		}
	} else {
		if lazyField.Type() != js.TypeFunction {
			return gbfs.InitialFile{}, fmt.Errorf("lazy must be a function")
		}
		file.Lazy = lazyProviderFromJS(lazyField)
	}

	if modeField := value.Get("mode"); isDefined(modeField) {
		if modeField.Type() != js.TypeNumber {
			return gbfs.InitialFile{}, fmt.Errorf("mode must be a number")
		}
		file.Mode = stdfs.FileMode(modeField.Int())
	}
	if mtimeField := value.Get("mtime"); isDefined(mtimeField) {
		modTime, err := timeFromJSValue(mtimeField)
		if err != nil {
			return gbfs.InitialFile{}, err
		}
		file.ModTime = modTime
	}

	return file, nil
}

func lazyProviderFromJS(provider js.Value) gbfs.LazyFileProvider {
	return func(ctx context.Context) ([]byte, error) {
		type result struct {
			data []byte
			err  error
		}

		done := make(chan result, 1)
		var once sync.Once
		resolve := func(data []byte, err error) {
			once.Do(func() {
				done <- result{data: data, err: err}
			})
		}

		onResolve := js.FuncOf(func(this js.Value, args []js.Value) any {
			var value js.Value
			if len(args) > 0 {
				value = args[0]
			}
			data, err := bytesFromJSValue(value)
			resolve(data, err)
			return nil
		})
		onReject := js.FuncOf(func(this js.Value, args []js.Value) any {
			reason := "lazy file provider rejected"
			if len(args) > 0 {
				reason = jsValueString(args[0])
			}
			resolve(nil, fmt.Errorf("%s", reason))
			return nil
		})
		defer onResolve.Release()
		defer onReject.Release()

		invoked := provider.Invoke()
		if isPromise(invoked) {
			invoked.Call("then", onResolve)
			invoked.Call("catch", onReject)
		} else {
			data, err := bytesFromJSValue(invoked)
			resolve(data, err)
		}

		select {
		case result := <-done:
			return result.data, result.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func bytesFromJSValue(value js.Value) ([]byte, error) {
	switch {
	case value.Type() == js.TypeUndefined || value.Type() == js.TypeNull:
		return nil, fmt.Errorf("file content must be a string or Uint8Array")
	case value.Type() == js.TypeString:
		return []byte(value.String()), nil
	case isUint8Array(value):
		buf := make([]byte, value.Get("byteLength").Int())
		js.CopyBytesToGo(buf, value)
		return buf, nil
	default:
		return nil, fmt.Errorf("unsupported file content type %s", value.Type())
	}
}

func isUint8Array(value js.Value) bool {
	ctor := js.Global().Get("Uint8Array")
	return ctor.Truthy() && value.Type() == js.TypeObject && value.InstanceOf(ctor)
}

func isPromise(value js.Value) bool {
	ctor := js.Global().Get("Promise")
	return ctor.Truthy() && value.Type() == js.TypeObject && value.InstanceOf(ctor)
}

func isDefined(value js.Value) bool {
	return !value.IsUndefined() && !value.IsNull()
}

func timeFromJSValue(value js.Value) (time.Time, error) {
	dateCtor := js.Global().Get("Date")
	if !dateCtor.Truthy() || value.Type() != js.TypeObject || !value.InstanceOf(dateCtor) {
		return time.Time{}, fmt.Errorf("mtime must be a Date")
	}
	return time.UnixMilli(int64(value.Call("getTime").Float())).UTC(), nil
}

func jsValueString(value js.Value) string {
	switch value.Type() {
	case js.TypeString:
		return value.String()
	case js.TypeUndefined:
		return "undefined"
	case js.TypeNull:
		return "null"
	default:
		text := value.String()
		if strings.TrimSpace(text) == "" {
			return value.Type().String()
		}
		return text
	}
}

func cleanPath(name string) string {
	if strings.TrimSpace(name) == "" {
		return "/"
	}
	cleaned := path.Clean(name)
	if !strings.HasPrefix(cleaned, "/") {
		return "/" + cleaned
	}
	return cleaned
}

func resolvePath(dir, name string) string {
	if strings.HasPrefix(name, "/") {
		return cleanPath(name)
	}
	return cleanPath(path.Join(dir, name))
}

func cloneEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		out[key] = value
	}
	return out
}
