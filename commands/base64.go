package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
)

type Base64 struct{}

func NewBase64() *Base64 {
	return &Base64{}
}

func (c *Base64) Name() string {
	return "base64"
}

func (c *Base64) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Base64) Spec() CommandSpec {
	return CommandSpec{
		Name:  "base64",
		About: "encode/decode data and print to standard output\nWith no FILE, or when FILE is -, read standard input.\n\nThe data are encoded as described for the base64 alphabet in RFC 3548.\nWhen decoding, the input may contain newlines in addition\nto the bytes of the formal base64 alphabet. Use --ignore-garbage\nto attempt to recover from any other non-alphabet bytes in the\nencoded stream.",
		Usage: "base64 [OPTION]... [FILE]",
		Options: []OptionSpec{
			{Name: "decode", Short: 'd', ShortAliases: []rune{'D'}, Long: "decode", Help: "decode data"},
			{Name: "ignore-garbage", Short: 'i', Long: "ignore-garbage", Help: "when decoding, ignore non-alphabetic characters"},
			{Name: "wrap", Short: 'w', Long: "wrap", ValueName: "COLS", Arity: OptionRequiredValue, Help: "wrap encoded lines after COLS character (default 76, 0 to disable wrapping)"},
		},
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
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := io.WriteString(w, spec.About+"\n\nUsage: "+spec.Usage+"\n\nOptions:\n  -d, --decode           decode data\n  -i, --ignore-garbage   when decoding, ignore non-alphabetic characters\n  -w, --wrap=COLS        wrap encoded lines after COLS character (default 76, 0 to disable wrapping)\n  -h, --help             display this help and exit\n      --version          output version information and exit\n")
			return err
		},
	}
}

func (c *Base64) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	decode := matches.Has("decode")
	ignoreGarbage := matches.Has("ignore-garbage")
	wrap := 76
	if matches.Has("wrap") {
		value, err := parseBaseEncWrap(c.Name(), matches.Value("wrap"), inv)
		if err != nil {
			return err
		}
		wrap = value
	}

	data, err := readSingleBaseEncInput(ctx, inv, c.Name(), matches.Positionals())
	if err != nil {
		return err
	}

	if decode {
		decoded, invalid := decodeBase64Data(data, ignoreGarbage)
		if len(decoded) > 0 {
			if _, err := inv.Stdout.Write(decoded); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if invalid {
			return exitf(inv, 1, "base64: invalid input")
		}
		return nil
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	if err := writeBaseEncOutput(inv.Stdout, encoded, wrap); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func decodeBase64Data(data []byte, ignoreGarbage bool) ([]byte, bool) {
	filtered := make([]byte, 0, len(data))
	invalid := false

	for _, b := range data {
		switch {
		case b == ' ' || b == '\n' || b == '\r' || b == '\t':
			continue
		case isBase64Byte(b) || b == '=':
			filtered = append(filtered, b)
		case ignoreGarbage:
			continue
		default:
			invalid = true
			goto decode
		}
	}

decode:
	decoded, decodeInvalid := decodeBase64Filtered(filtered)
	return decoded, invalid || decodeInvalid
}

func decodeBase64Filtered(filtered []byte) ([]byte, bool) {
	if len(filtered) == 0 {
		return nil, false
	}

	decoded := make([]byte, 0, len(filtered))
	invalid := false

	for len(filtered) > 0 {
		eq := bytes.IndexByte(filtered, '=')
		if eq < 0 {
			tail, tailInvalid := decodeBase64Tail(filtered)
			decoded = append(decoded, tail...)
			invalid = invalid || tailInvalid
			break
		}

		segmentLen := ((eq / 4) + 1) * 4
		if segmentLen > len(filtered) {
			tail, tailInvalid := decodeBase64PaddedTail(filtered)
			decoded = append(decoded, tail...)
			invalid = invalid || tailInvalid
			break
		}

		segment, segmentInvalid := decodeBase64PaddedSegment(filtered[:segmentLen])
		decoded = append(decoded, segment...)
		invalid = invalid || segmentInvalid
		filtered = filtered[segmentLen:]
	}

	return decoded, invalid
}

func decodeBase64Tail(segment []byte) ([]byte, bool) {
	switch len(segment) % 4 {
	case 0:
		if decoded, ok := tryDecodeBase64(base64.StdEncoding.Strict(), segment); ok {
			return decoded, false
		}
		return recoverBase64Segment(segment, false), true
	case 2, 3:
		if decoded, ok := tryDecodeBase64(base64.RawStdEncoding.Strict(), segment); ok {
			return decoded, false
		}
		return recoverBase64Segment(segment, false), true
	default:
		return nil, true
	}
}

func decodeBase64PaddedSegment(segment []byte) ([]byte, bool) {
	if decoded, ok := tryDecodeBase64(base64.StdEncoding.Strict(), segment); ok {
		return decoded, false
	}
	return recoverBase64Segment(segment, true), true
}

func decodeBase64PaddedTail(segment []byte) ([]byte, bool) {
	padded := padBase64Segment(segment)
	if len(segment)%4 == 0 {
		if decoded, ok := tryDecodeBase64(base64.StdEncoding.Strict(), padded); ok {
			return decoded, false
		}
	}
	return recoverBase64Segment(padded, true), true
}

func recoverBase64Segment(segment []byte, padded bool) []byte {
	var best []byte
	consider := func(decoded []byte) {
		if len(decoded) > len(best) {
			best = decoded
		}
	}

	if padded {
		if decoded, _ := base64.StdEncoding.Strict().DecodeString(string(segment)); len(decoded) > 0 {
			consider(decoded)
		}
		if decoded, _ := base64.StdEncoding.DecodeString(string(segment)); len(decoded) > 0 {
			consider(decoded)
		}
		paddedSegment := padBase64Segment(segment)
		if !bytes.Equal(paddedSegment, segment) {
			if decoded, _ := base64.StdEncoding.Strict().DecodeString(string(paddedSegment)); len(decoded) > 0 {
				consider(decoded)
			}
			if decoded, _ := base64.StdEncoding.DecodeString(string(paddedSegment)); len(decoded) > 0 {
				consider(decoded)
			}
		}
		return best
	}

	if decoded, _ := base64.RawStdEncoding.Strict().DecodeString(string(segment)); len(decoded) > 0 {
		consider(decoded)
	}
	if decoded, _ := base64.RawStdEncoding.DecodeString(string(segment)); len(decoded) > 0 {
		consider(decoded)
	}

	paddedSegment := padBase64Segment(segment)
	if decoded, _ := base64.StdEncoding.Strict().DecodeString(string(paddedSegment)); len(decoded) > 0 {
		consider(decoded)
	}
	if decoded, _ := base64.StdEncoding.DecodeString(string(paddedSegment)); len(decoded) > 0 {
		consider(decoded)
	}

	return best
}

func tryDecodeBase64(enc *base64.Encoding, segment []byte) ([]byte, bool) {
	decoded, err := enc.DecodeString(string(segment))
	return decoded, err == nil
}

func padBase64Segment(segment []byte) []byte {
	if rem := len(segment) % 4; rem != 0 {
		padded := append([]byte(nil), segment...)
		return append(padded, bytes.Repeat([]byte{'='}, 4-rem)...)
	}
	return segment
}

func isBase64Byte(b byte) bool {
	return (b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') ||
		b == '+' || b == '/'
}

var _ Command = (*Base64)(nil)
var _ SpecProvider = (*Base64)(nil)
var _ ParsedRunner = (*Base64)(nil)
