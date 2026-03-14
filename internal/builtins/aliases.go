package builtins

import pubcmd "github.com/ewhauser/gbash/commands"

type Command = pubcmd.Command
type CommandFunc = pubcmd.CommandFunc
type Invocation = pubcmd.Invocation
type ExitError = pubcmd.ExitError

type SpecProvider = pubcmd.SpecProvider
type ParsedRunner = pubcmd.ParsedRunner
type ParseInvocationNormalizer = pubcmd.ParseInvocationNormalizer
type ParseErrorNormalizer = pubcmd.ParseErrorNormalizer
type LegacySpecProvider = pubcmd.LegacySpecProvider

type CommandSpec = pubcmd.CommandSpec
type ParseConfig = pubcmd.ParseConfig
type OptionArity = pubcmd.OptionArity
type OptionSpec = pubcmd.OptionSpec
type ArgSpec = pubcmd.ArgSpec
type ParsedCommand = pubcmd.ParsedCommand

type ExecutionRequest = pubcmd.ExecutionRequest
type ExecutionResult = pubcmd.ExecutionResult
type InteractiveRequest = pubcmd.InteractiveRequest
type InteractiveResult = pubcmd.InteractiveResult

type InvocationOptions = pubcmd.InvocationOptions
type CommandFS = pubcmd.CommandFS
type LazyCommandLoader = pubcmd.LazyCommandLoader
type CommandRegistry = pubcmd.CommandRegistry
type Registry = pubcmd.Registry
type VersionInfo = pubcmd.VersionInfo

const (
	OptionNoValue       = pubcmd.OptionNoValue
	OptionRequiredValue = pubcmd.OptionRequiredValue
	OptionOptionalValue = pubcmd.OptionOptionalValue
)

var DefineCommand = pubcmd.DefineCommand
var ExitCode = pubcmd.ExitCode
var Exitf = pubcmd.Exitf
var NewInvocation = pubcmd.NewInvocation
var RunCommand = pubcmd.RunCommand
var ParseCommandSpec = pubcmd.ParseCommandSpec
var RenderCommandHelp = pubcmd.RenderCommandHelp
var RenderCommandVersion = pubcmd.RenderCommandVersion
var RenderSimpleVersion = pubcmd.RenderSimpleVersion
var RenderDetailedVersion = pubcmd.RenderDetailedVersion
var NewRegistry = pubcmd.NewRegistry
