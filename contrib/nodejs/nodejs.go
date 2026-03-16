package nodejs

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"slices"
	"strings"

	"github.com/dop251/goja"
	gojabuffer "github.com/dop251/goja_nodejs/buffer"
	gojaconsole "github.com/dop251/goja_nodejs/console"
	gojarequire "github.com/dop251/goja_nodejs/require"
	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
)

type NodeJS struct{}

type sourceKind int

const (
	sourceStdin sourceKind = iota
	sourceEval
	sourceFile
)

type nodeSource struct {
	kind      sourceKind
	code      string
	scriptArg string
	entryPath string
}

type interruptSignal struct {
	err error
}

type processExitSignal struct {
	code int
}

type nodeRuntime struct {
	ctx context.Context //nolint:containedctx // intentional: runtime-scoped context
	inv *commands.Invocation
	vm  *goja.Runtime

	rawRequire goja.Callable
	req        *gojarequire.RequireModule
	source     nodeSource
	args       []string

	extraSources map[string][]byte

	consoleExports *goja.Object
	processExports *goja.Object
	fsExports      *goja.Object
	pathExports    *goja.Object
}

func NewNodeJS() *NodeJS {
	return &NodeJS{}
}

func Register(registry commands.CommandRegistry) error {
	if registry == nil {
		return nil
	}
	return registry.Register(NewNodeJS())
}

func (c *NodeJS) Name() string {
	return "nodejs"
}

func (c *NodeJS) Run(ctx context.Context, inv *commands.Invocation) error {
	return commands.RunCommand(ctx, c, inv)
}

func (c *NodeJS) Spec() commands.CommandSpec {
	return commands.CommandSpec{
		Name:  c.Name(),
		About: "Run sandboxed CommonJS JavaScript inside gbash",
		Usage: "nodejs [-e script] [script.js] [-- arg ...]",
		Options: []commands.OptionSpec{
			{Name: "eval", Short: 'e', ValueName: "script", Arity: commands.OptionRequiredValue, Help: "evaluate the provided script string"},
		},
		Args: []commands.ArgSpec{
			{Name: "arg", ValueName: "arg", Repeatable: true},
		},
		Parse: commands.ParseConfig{
			ShortOptionValueAttached: true,
			StopAtFirstPositional:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		VersionRenderer: func(w io.Writer, _ commands.CommandSpec) error {
			return commands.RenderSimpleVersion(w, c.Name())
		},
	}
}

func (c *NodeJS) RunParsed(ctx context.Context, inv *commands.Invocation, matches *commands.ParsedCommand) error {
	source, args, err := classifyNodeSource(ctx, inv, matches)
	if err != nil {
		return err
	}

	rt, err := newNodeRuntime(ctx, inv, source, args)
	if err != nil {
		return err
	}
	return rt.run()
}

func classifyNodeSource(ctx context.Context, inv *commands.Invocation, matches *commands.ParsedCommand) (nodeSource, []string, error) {
	if matches == nil {
		return prepareStdinSource(ctx, inv, nil)
	}
	args := matches.Args("arg")
	if matches.Has("eval") {
		return prepareEvalSource(ctx, inv, matches.Value("eval"), args)
	}
	if len(args) > 0 {
		return prepareFileSource(ctx, inv, args[0], args[1:])
	}
	return prepareStdinSource(ctx, inv, nil)
}

func prepareEvalSource(_ context.Context, inv *commands.Invocation, code string, args []string) (nodeSource, []string, error) {
	return nodeSource{
		kind:      sourceEval,
		code:      code,
		entryPath: gbfs.Resolve(inv.Cwd, ".gbash-nodejs-eval.js"),
	}, append([]string(nil), args...), nil
}

func prepareStdinSource(ctx context.Context, inv *commands.Invocation, args []string) (nodeSource, []string, error) {
	data, err := readAllStdinLimited(ctx, inv)
	if err != nil {
		return nodeSource{}, nil, err
	}
	return nodeSource{
		kind:      sourceStdin,
		code:      string(data),
		entryPath: gbfs.Resolve(inv.Cwd, ".gbash-nodejs-stdin.js"),
	}, append([]string(nil), args...), nil
}

func prepareFileSource(ctx context.Context, inv *commands.Invocation, scriptArg string, args []string) (nodeSource, []string, error) {
	abs := inv.FS.Resolve(scriptArg)
	info, err := inv.FS.Stat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nodeSource{}, nil, commands.Exitf(inv, 1, "nodejs: %s: No such file or directory", scriptArg)
		}
		return nodeSource{}, nil, err
	}
	if info.IsDir() {
		return nodeSource{}, nil, commands.Exitf(inv, 1, "nodejs: %s: Is a directory", scriptArg)
	}
	return nodeSource{
		kind:      sourceFile,
		scriptArg: scriptArg,
		entryPath: abs,
	}, append([]string(nil), args...), nil
}

func newNodeRuntime(ctx context.Context, inv *commands.Invocation, source nodeSource, args []string) (*nodeRuntime, error) {
	rt := &nodeRuntime{
		ctx:          ctx,
		inv:          inv,
		vm:           goja.New(),
		source:       source,
		args:         append([]string(nil), args...),
		extraSources: make(map[string][]byte),
	}

	if source.kind != sourceFile {
		if err := rt.storeInlineSource(source.entryPath, []byte(source.code)); err != nil {
			return nil, err
		}
	}

	registry := gojarequire.NewRegistry(
		gojarequire.WithLoader(rt.loadModuleSource),
		gojarequire.WithPathResolver(rt.resolveModulePath),
	)
	rt.registerNativeModules(registry)
	rt.req = registry.Enable(rt.vm)

	rawRequire, ok := goja.AssertFunction(rt.vm.Get("require"))
	if !ok {
		return nil, fmt.Errorf("nodejs: require() was not installed")
	}
	rt.rawRequire = rawRequire
	if err := rt.vm.Set("require", rt.require); err != nil {
		return nil, err
	}

	if err := rt.installGlobals(); err != nil {
		return nil, err
	}
	return rt, nil
}

func (rt *nodeRuntime) run() error {
	done := make(chan struct{})
	if rt.ctx != nil {
		go func() {
			select {
			case <-rt.ctx.Done():
				rt.vm.Interrupt(interruptSignal{err: rt.ctx.Err()})
			case <-done:
			}
		}()
	}
	defer close(done)

	_, err := rt.req.Require(rt.source.entryPath)
	if err == nil {
		return nil
	}
	return rt.normalizeRunError(err)
}

func (rt *nodeRuntime) installGlobals() error {
	gojabuffer.Enable(rt.vm)

	consoleObj, err := rt.consoleModule()
	if err != nil {
		return err
	}
	processObj, err := rt.processModule()
	if err != nil {
		return err
	}

	if err := rt.vm.Set("console", consoleObj); err != nil {
		return err
	}
	if err := rt.vm.Set("process", processObj); err != nil {
		return err
	}

	if err := freezeObject(rt.vm, consoleObj); err != nil {
		return err
	}
	if err := freezeObject(rt.vm, processObj); err != nil {
		return err
	}
	return nil
}

func (rt *nodeRuntime) consoleModule() (*goja.Object, error) {
	if rt.consoleExports != nil {
		return rt.consoleExports, nil
	}

	module := rt.vm.NewObject()
	exports := rt.vm.NewObject()
	if err := module.Set("exports", exports); err != nil {
		return nil, err
	}
	rt.loadConsoleModule(rt.vm, module)
	if rt.consoleExports == nil {
		return nil, fmt.Errorf("nodejs: console exports were not initialized")
	}
	return rt.consoleExports, nil
}

func (rt *nodeRuntime) registerNativeModules(registry *gojarequire.Registry) {
	if registry == nil {
		return
	}
	rt.registerModuleAlias(registry, "console", rt.loadConsoleModule)
	rt.registerModuleAlias(registry, "process", rt.loadProcessModule)
	rt.registerModuleAlias(registry, "fs", rt.loadFSModule)
	rt.registerModuleAlias(registry, "path", rt.loadPathModule)
}

func (rt *nodeRuntime) registerModuleAlias(registry *gojarequire.Registry, name string, loader gojarequire.ModuleLoader) {
	registry.RegisterNativeModule(name, loader)
	registry.RegisterNativeModule("node:"+name, loader)
}

func (rt *nodeRuntime) loadConsoleModule(runtime *goja.Runtime, module *goja.Object) {
	if rt.consoleExports == nil {
		gojaconsole.RequireWithPrinter(nodeConsolePrinter{ctx: rt.ctx, inv: rt.inv})(runtime, module)
		rt.consoleExports = module.Get("exports").(*goja.Object)
		_ = freezeObject(runtime, rt.consoleExports)
		return
	}
	if err := module.Set("exports", rt.consoleExports); err != nil {
		panic(runtime.NewGoError(err))
	}
}

func (rt *nodeRuntime) loadProcessModule(runtime *goja.Runtime, module *goja.Object) {
	exports, err := rt.processModule()
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	if err := module.Set("exports", exports); err != nil {
		panic(runtime.NewGoError(err))
	}
}

func (rt *nodeRuntime) loadFSModule(runtime *goja.Runtime, module *goja.Object) {
	exports, err := rt.fsModule()
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	if err := module.Set("exports", exports); err != nil {
		panic(runtime.NewGoError(err))
	}
}

func (rt *nodeRuntime) loadPathModule(runtime *goja.Runtime, module *goja.Object) {
	exports, err := rt.pathModule()
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	if err := module.Set("exports", exports); err != nil {
		panic(runtime.NewGoError(err))
	}
}

func (rt *nodeRuntime) require(call goja.FunctionCall) goja.Value {
	rt.ensureContext()

	name := call.Argument(0).String()
	if err := rt.validateRequire(name); err != nil {
		panic(rt.vm.NewGoError(err))
	}

	value, err := rt.rawRequire(goja.Undefined(), call.Arguments...)
	if err != nil {
		panic(err)
	}
	return value
}

func (rt *nodeRuntime) validateRequire(name string) error {
	switch {
	case isAllowedBuiltin(name):
		return nil
	case !isFileOrDirectoryPath(name):
		return fmt.Errorf("nodejs: unsupported module %q", name)
	}

	resolved := rt.resolveModulePath(rt.currentModuleDir(), name)
	info, err := rt.inv.FS.Stat(rt.ctx, resolved)
	switch {
	case err == nil && info.IsDir():
		return fmt.Errorf("nodejs: directory modules are not supported: %s", name)
	case err == nil:
		return nil
	case errors.Is(err, stdfs.ErrNotExist):
		return nil
	default:
		return err
	}
}

func (rt *nodeRuntime) loadModuleSource(name string) ([]byte, error) {
	rt.ensureContext()

	name = gbfs.Clean(strings.ReplaceAll(name, "\\", "/"))
	if data, ok := rt.extraSources[name]; ok {
		return append([]byte(nil), data...), nil
	}

	info, err := rt.inv.FS.Stat(rt.ctx, name)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, gojarequire.ModuleFileDoesNotExistError
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, gojarequire.ModuleFileDoesNotExistError
	}
	if limit := maxFileBytes(rt.inv); limit > 0 && info.Size() > limit {
		return nil, fmt.Errorf("nodejs: %s: file too large (%d bytes, max %d)", name, info.Size(), limit)
	}

	file, err := rt.inv.FS.Open(rt.ctx, name)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, gojarequire.ModuleFileDoesNotExistError
		}
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
	return normalizeModuleSource(data), nil
}

func (rt *nodeRuntime) resolveModulePath(base, target string) string {
	base = strings.ReplaceAll(strings.TrimSpace(base), "\\", "/")
	target = strings.ReplaceAll(strings.TrimSpace(target), "\\", "/")

	if strings.HasPrefix(target, "/") {
		return gbfs.Clean(target)
	}

	if base == "" || base == "." {
		base = rt.inv.Cwd
	}
	return gbfs.Resolve(gbfs.Clean(base), target)
}

func (rt *nodeRuntime) currentModuleDir() string {
	frames := rt.vm.CaptureCallStack(2, nil)
	if len(frames) < 2 {
		return rt.inv.Cwd
	}
	src := strings.ReplaceAll(strings.TrimSpace(frames[1].SrcName()), "\\", "/")
	if src == "" {
		return rt.inv.Cwd
	}
	return gbfs.Clean(path.Dir(src))
}

func (rt *nodeRuntime) scriptArgv() []string {
	argv := []string{rt.Name()}
	if rt.source.kind == sourceFile {
		argv = append(argv, rt.source.scriptArg)
	}
	argv = append(argv, rt.args...)
	return argv
}

func (rt *nodeRuntime) Name() string {
	if rt == nil || rt.inv == nil {
		return "nodejs"
	}
	return "nodejs"
}

func (rt *nodeRuntime) ensureContext() {
	if rt.ctx == nil {
		return
	}
	if err := rt.ctx.Err(); err != nil {
		panic(rt.vm.NewGoError(err))
	}
}

func (rt *nodeRuntime) storeInlineSource(name string, data []byte) error {
	if limit := maxFileBytes(rt.inv); limit > 0 && int64(len(data)) > limit {
		return commands.Exitf(rt.inv, 1, "nodejs: inline script too large (%d bytes, max %d)", len(data), limit)
	}
	rt.extraSources[gbfs.Clean(name)] = normalizeModuleSource(data)
	return nil
}

func (rt *nodeRuntime) normalizeRunError(err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		switch value := interrupted.Value().(type) {
		case processExitSignal:
			return &commands.ExitError{Code: value.code}
		case interruptSignal:
			if value.err != nil {
				return value.err
			}
		case error:
			if errors.Is(value, context.Canceled) || errors.Is(value, context.DeadlineExceeded) {
				return value
			}
		}
	}

	message := formatNodeError(err)
	if message != "" && rt.inv != nil && rt.inv.Stderr != nil {
		_, _ = fmt.Fprintf(rt.inv.Stderr, "nodejs: %s\n", message)
	}
	return &commands.ExitError{Code: 1, Err: err}
}

func formatNodeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	message = strings.TrimPrefix(message, "GoError: ")
	message = strings.TrimPrefix(message, "nodejs: ")
	return message
}

func isAllowedBuiltin(name string) bool {
	return slices.Contains([]string{
		"buffer",
		"node:buffer",
		"console",
		"node:console",
		"fs",
		"node:fs",
		"path",
		"node:path",
		"process",
		"node:process",
		"url",
		"node:url",
		"util",
		"node:util",
	}, name)
}

func isFileOrDirectoryPath(name string) bool {
	return name == "." || name == ".." ||
		strings.HasPrefix(name, "/") ||
		strings.HasPrefix(name, "./") ||
		strings.HasPrefix(name, "../")
}

func readAllStdinLimited(ctx context.Context, inv *commands.Invocation) ([]byte, error) {
	data, err := commands.ReadAllStdin(ctx, inv)
	if err != nil {
		if isMaxFileBytesError(inv, err) {
			return nil, commands.Exitf(inv, 1, "nodejs: stdin too large (max %d bytes)", maxFileBytes(inv))
		}
		return nil, err
	}
	return data, nil
}

func maxFileBytes(inv *commands.Invocation) int64 {
	if inv == nil || inv.Limits.MaxFileBytes <= 0 {
		return 0
	}
	return inv.Limits.MaxFileBytes
}

func isMaxFileBytesError(inv *commands.Invocation, err error) bool {
	if err == nil || maxFileBytes(inv) <= 0 {
		return false
	}
	message, ok := commands.DiagnosticMessage(err)
	if !ok {
		return false
	}
	return message == fmt.Sprintf("input exceeds maximum file size of %d bytes", maxFileBytes(inv))
}

func freezeObject(runtime *goja.Runtime, obj *goja.Object) error {
	if runtime == nil || obj == nil {
		return nil
	}
	objectCtor := runtime.Get("Object")
	if objectCtor == nil {
		return nil
	}
	object := objectCtor.ToObject(runtime)
	freeze, ok := goja.AssertFunction(object.Get("freeze"))
	if !ok {
		return fmt.Errorf("object.freeze is not available")
	}
	_, err := freeze(object, obj)
	return err
}

func normalizeModuleSource(data []byte) []byte {
	normalized := append([]byte(nil), data...)
	if len(normalized) >= 2 && normalized[0] == '#' && normalized[1] == '!' {
		normalized[0] = '/'
		normalized[1] = '/'
	}
	return normalized
}

type nodeConsolePrinter struct {
	ctx context.Context //nolint:containedctx // intentional: printer-scoped context
	inv *commands.Invocation
}

func (p nodeConsolePrinter) Log(value string) {
	p.write(p.inv.Stdout, value)
}

func (p nodeConsolePrinter) Warn(value string) {
	p.write(p.inv.Stderr, value)
}

func (p nodeConsolePrinter) Error(value string) {
	p.write(p.inv.Stderr, value)
}

func (p nodeConsolePrinter) write(dst io.Writer, value string) {
	if dst == nil {
		return
	}
	if p.ctx != nil && p.ctx.Err() != nil {
		return
	}
	_, _ = fmt.Fprintln(dst, value)
}

var _ commands.Command = (*NodeJS)(nil)
var _ commands.SpecProvider = (*NodeJS)(nil)
var _ commands.ParsedRunner = (*NodeJS)(nil)
