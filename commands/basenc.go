package commands

import (
	"context"
	"io"
	"syscall"
)

type basencOptions struct {
	decode        bool
	ignoreGarbage bool
	wrap          int
	file          string
	format        basencEncoding
	hasFormat     bool
}

type basencFormatSpec struct {
	option string
	help   string
	format basencEncoding
}

var basencFormatSpecs = []basencFormatSpec{
	{option: "base64", help: "same as 'base64' program", format: basencEncodingBase64},
	{option: "base64url", help: "file- and url-safe base64", format: basencEncodingBase64URL},
	{option: "base32", help: "same as 'base32' program", format: basencEncodingBase32},
	{option: "base32hex", help: "extended hex alphabet base32", format: basencEncodingBase32Hex},
	{option: "base16", help: "hex encoding", format: basencEncodingBase16},
	{option: "base2lsbf", help: "bit string with least significant bit (lsb) first", format: basencEncodingBase2LSBF},
	{option: "base2msbf", help: "bit string with most significant bit (msb) first", format: basencEncodingBase2MSBF},
	{option: "z85", help: "ascii85-like encoding; when encoding, input length must be a multiple of 4; when decoding, input length must be a multiple of 5", format: basencEncodingZ85},
	{option: "base58", help: "visually unambiguous base58 encoding", format: basencEncodingBase58},
}

type Basenc struct{}

func NewBasenc() *Basenc {
	return &Basenc{}
}

func (c *Basenc) Name() string {
	return "basenc"
}

func (c *Basenc) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Basenc) Spec() CommandSpec {
	options := []OptionSpec{
		{Name: "decode", Short: 'd', Long: "decode", Help: "decode data"},
		{Name: "decode", Short: 'D', Hidden: true},
		{Name: "ignore-garbage", Short: 'i', Long: "ignore-garbage", Help: "when decoding, ignore non-alphabetic characters"},
		{Name: "wrap", Short: 'w', Long: "wrap", ValueName: "COLS", Arity: OptionRequiredValue, Help: "wrap encoded lines after COLS character (default 76, 0 to disable wrapping)"},
	}
	for _, spec := range basencFormatSpecs {
		options = append(options, OptionSpec{Name: spec.option, Long: spec.option, Help: spec.help})
	}

	return CommandSpec{
		Name: "basenc",
		About: "Encode/decode data and print to standard output\n\n" +
			"With no FILE, or when FILE is -, read standard input.\n\n" +
			"When decoding, the input may contain newlines in addition to the bytes of\n" +
			"the formal alphabet. Use --ignore-garbage to attempt to recover\n" +
			"from any other non-alphabet bytes in the encoded stream.",
		Usage:   "basenc [OPTION]... [FILE]",
		Options: options,
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE"},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Basenc) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := c.optionsFromMatches(inv, matches)
	if err != nil {
		return err
	}

	data, err := readBasencInput(ctx, inv, opts.file)
	if err != nil {
		return err
	}

	if opts.decode {
		decoded, invalid := opts.format.decode(data, opts.ignoreGarbage)
		if len(decoded) > 0 {
			if _, err := inv.Stdout.Write(decoded); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if invalid {
			return exitf(inv, 1, "basenc: error: invalid input")
		}
		return nil
	}

	encoded, err := opts.format.encode(data)
	if err != nil {
		return exitf(inv, 1, "basenc: error: %s", err.Error())
	}
	if err := writeBaseEncOutput(inv.Stdout, encoded, opts.wrap); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func (c *Basenc) optionsFromMatches(inv *Invocation, matches *ParsedCommand) (basencOptions, error) {
	opts := basencOptions{
		decode:        matches.Has("decode"),
		ignoreGarbage: matches.Has("ignore-garbage"),
		wrap:          76,
		file:          matches.Arg("file"),
	}

	if rawWrap := matches.Value("wrap"); rawWrap != "" {
		wrap, err := parseBaseEncWrap(c.Name(), rawWrap, inv)
		if err != nil {
			return basencOptions{}, err
		}
		opts.wrap = wrap
	}

	for _, name := range matches.OptionOrder() {
		for _, spec := range basencFormatSpecs {
			if spec.option == name {
				opts.format = spec.format
				opts.hasFormat = true
				break
			}
		}
	}
	if !opts.hasFormat {
		return basencOptions{}, commandUsageError(inv, c.Name(), "missing encoding type")
	}

	return opts, nil
}

func readBasencInput(ctx context.Context, inv *Invocation, name string) ([]byte, error) {
	if name == "" || name == "-" {
		data, err := readAllStdin(inv)
		if err != nil {
			return nil, basencReadError(inv, err)
		}
		return data, nil
	}

	if info, _, err := statPath(ctx, inv, name); err == nil && info.IsDir() {
		return nil, basencReadError(inv, syscall.EISDIR)
	}

	file, _, err := openRead(ctx, inv, name)
	if err != nil {
		return nil, basencPathError(inv, name, err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, basencReadError(inv, err)
	}
	return data, nil
}

var _ Command = (*Basenc)(nil)
var _ SpecProvider = (*Basenc)(nil)
var _ ParsedRunner = (*Basenc)(nil)
