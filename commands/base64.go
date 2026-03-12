package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

type Base64 struct{}

func NewBase64() *Base64 {
	return &Base64{}
}

func (c *Base64) Name() string {
	return "base64"
}

func (c *Base64) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	decode := false
	ignoreGarbage := false
	wrap := 76

optionLoop:
	for len(args) > 0 {
		arg := args[0]
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}

		switch {
		case arg == "--":
			args = args[1:]
			break optionLoop
		case arg == "--help":
			_, _ = fmt.Fprintln(inv.Stdout, "usage: base64 [OPTION]... [FILE]")
			return nil
		case arg == "--version":
			_, _ = fmt.Fprintln(inv.Stdout, "base64 (gbash)")
			return nil
		case arg == "--decode":
			decode = true
			args = args[1:]
		case arg == "--ignore-garbage":
			ignoreGarbage = true
			args = args[1:]
		case arg == "--wrap":
			if len(args) < 2 {
				return exitf(inv, 1, "base64: option requires an argument -- wrap")
			}
			value, err := parseBaseEncWrap(c.Name(), args[1], inv)
			if err != nil {
				return err
			}
			wrap = value
			args = args[2:]
		case strings.HasPrefix(arg, "--wrap="):
			value, err := parseBaseEncWrap(c.Name(), strings.TrimPrefix(arg, "--wrap="), inv)
			if err != nil {
				return err
			}
			wrap = value
			args = args[1:]
		default:
			consumed, err := parseBase64ShortOptions(arg, args, &decode, &ignoreGarbage, &wrap, inv)
			if err != nil {
				return err
			}
			args = args[consumed:]
		}
	}

	data, err := readSingleBaseEncInput(ctx, inv, c.Name(), args)
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

func parseBase64ShortOptions(arg string, args []string, decode, ignoreGarbage *bool, wrap *int, inv *Invocation) (int, error) {
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 'd':
			*decode = true
		case 'i':
			*ignoreGarbage = true
		case 'w':
			value := arg[i+1:]
			if value == "" {
				if len(args) < 2 {
					return 0, exitf(inv, 1, "base64: option requires an argument -- w")
				}
				parsed, err := parseBaseEncWrap("base64", args[1], inv)
				if err != nil {
					return 0, err
				}
				*wrap = parsed
				return 2, nil
			}
			parsed, err := parseBaseEncWrap("base64", value, inv)
			if err != nil {
				return 0, err
			}
			*wrap = parsed
			return 1, nil
		default:
			return 0, exitf(inv, 1, "base64: unsupported flag -%c", arg[i])
		}
	}
	return 1, nil
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
