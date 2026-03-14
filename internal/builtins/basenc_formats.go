package builtins

import (
	"bytes"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	stdfs "io/fs"
	"math"
	"math/big"
	"os"
	"strings"
	"syscall"
)

type basencEncoding int

const (
	basencEncodingBase64 basencEncoding = iota
	basencEncodingBase64URL
	basencEncodingBase32
	basencEncodingBase32Hex
	basencEncodingBase16
	basencEncodingBase2LSBF
	basencEncodingBase2MSBF
	basencEncodingZ85
	basencEncodingBase58
)

const (
	basencBase58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	basencZ85Alphabet    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.-:+=^!/*?&<>()[]{}@%$#"
)

var (
	basencBase64Lookup    = basencByteLookup("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=")
	basencBase64URLLookup = basencByteLookup("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_=")
	basencBase32Lookup    = basencByteLookup("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567=")
	basencBase32HexLookup = basencByteLookup("0123456789ABCDEFGHIJKLMNOPQRSTUV=")
	basencBase16Lookup    = basencByteLookup("0123456789ABCDEFabcdef")
	basencBase2Lookup     = basencByteLookup("01")
	basencBase58Lookup    = basencByteLookup(basencBase58Alphabet)
	basencZ85Lookup       = basencByteLookup(basencZ85Alphabet)
	basencZ85DecodeMap    = basencDecodeMap(basencZ85Alphabet)
	basencBase58IndexMap  = basencDecodeMap(basencBase58Alphabet)
)

func basencByteLookup(alphabet string) [256]bool {
	var table [256]bool
	for i := 0; i < len(alphabet); i++ {
		table[alphabet[i]] = true
	}
	return table
}

func basencDecodeMap(alphabet string) [256]int {
	var table [256]int
	for i := range table {
		table[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		table[alphabet[i]] = i
	}
	return table
}

func (e basencEncoding) encode(data []byte) (string, error) {
	switch e {
	case basencEncodingBase64:
		return base64.StdEncoding.EncodeToString(data), nil
	case basencEncodingBase64URL:
		return base64.RawURLEncoding.EncodeToString(data), nil
	case basencEncodingBase32:
		return base32.StdEncoding.EncodeToString(data), nil
	case basencEncodingBase32Hex:
		return base32.HexEncoding.EncodeToString(data), nil
	case basencEncodingBase16:
		return strings.ToUpper(hex.EncodeToString(data)), nil
	case basencEncodingBase2LSBF:
		return encodeBase2(data, true), nil
	case basencEncodingBase2MSBF:
		return encodeBase2(data, false), nil
	case basencEncodingZ85:
		return encodeZ85(data)
	case basencEncodingBase58:
		return encodeBase58(data), nil
	default:
		return "", fmt.Errorf("unsupported encoding")
	}
}

func (e basencEncoding) decode(data []byte, ignoreGarbage bool) ([]byte, bool) {
	switch e {
	case basencEncodingBase64:
		return decodeBase64Like(data, ignoreGarbage, base64.StdEncoding, base64.RawStdEncoding, &basencBase64Lookup)
	case basencEncodingBase64URL:
		return decodeBase64Like(data, ignoreGarbage, base64.URLEncoding, base64.RawURLEncoding, &basencBase64URLLookup)
	case basencEncodingBase32:
		return decodeBase32Like(data, ignoreGarbage, base32.StdEncoding, &basencBase32Lookup)
	case basencEncodingBase32Hex:
		return decodeBase32Like(data, ignoreGarbage, base32.HexEncoding, &basencBase32HexLookup)
	case basencEncodingBase16:
		return decodeBase16(data, ignoreGarbage)
	case basencEncodingBase2LSBF:
		return decodeBase2(data, ignoreGarbage, true)
	case basencEncodingBase2MSBF:
		return decodeBase2(data, ignoreGarbage, false)
	case basencEncodingZ85:
		return decodeZ85(data, ignoreGarbage)
	case basencEncodingBase58:
		return decodeBase58(data, ignoreGarbage)
	default:
		return nil, true
	}
}

func decodeBase64Like(data []byte, ignoreGarbage bool, stdEnc, rawEnc *base64.Encoding, lookup *[256]bool) ([]byte, bool) {
	filtered, ok := filterWholeEncodedInput(data, lookup, ignoreGarbage)
	if !ok {
		return nil, true
	}
	if len(filtered) == 0 {
		return nil, false
	}
	decoded, err := decodeBase64Segments(filtered, stdEnc, rawEnc)
	if err != nil {
		return nil, true
	}
	return decoded, false
}

func decodeBase64Segments(filtered []byte, stdEnc, rawEnc *base64.Encoding) ([]byte, error) {
	stdStrict := stdEnc.Strict()
	rawStrict := rawEnc.Strict()
	decoded := make([]byte, 0, len(filtered))

	for len(filtered) > 0 {
		eq := bytes.IndexByte(filtered, '=')
		if eq < 0 {
			var chunk []byte
			switch len(filtered) % 4 {
			case 0:
				chunk = filtered
				data, err := stdStrict.DecodeString(string(chunk))
				if err != nil {
					return nil, err
				}
				decoded = append(decoded, data...)
			case 2, 3:
				chunk = filtered
				data, err := rawStrict.DecodeString(string(chunk))
				if err != nil {
					return nil, err
				}
				decoded = append(decoded, data...)
			default:
				return nil, errors.New("invalid input")
			}
			break
		}

		segmentLen := ((eq / 4) + 1) * 4
		if segmentLen > len(filtered) {
			return nil, errors.New("invalid input")
		}
		data, err := stdStrict.DecodeString(string(filtered[:segmentLen]))
		if err != nil {
			return nil, err
		}
		decoded = append(decoded, data...)
		filtered = filtered[segmentLen:]
	}

	return decoded, nil
}

func decodeBase32Like(data []byte, ignoreGarbage bool, enc *base32.Encoding, lookup *[256]bool) ([]byte, bool) {
	var (
		buffer  []byte
		decoded []byte
	)

	flushFullBlocks := func() bool {
		for len(buffer) >= 8 {
			block, err := enc.DecodeString(string(buffer[:8]))
			if err != nil {
				return false
			}
			decoded = append(decoded, block...)
			buffer = buffer[8:]
		}
		return true
	}

	for _, b := range data {
		switch {
		case b == '\n' || b == '\r':
			continue
		case lookup[b]:
			buffer = append(buffer, b)
			if !flushFullBlocks() {
				return decoded, true
			}
		case ignoreGarbage:
			continue
		default:
			if !flushFullBlocks() {
				return decoded, true
			}
			return decoded, true
		}
	}

	if !flushFullBlocks() {
		return decoded, true
	}
	if len(buffer) == 0 {
		return decoded, false
	}

	remainder, invalid := decodeBase32Remainder(enc, buffer)
	decoded = append(decoded, remainder...)
	return decoded, invalid
}

func decodeBase32Remainder(enc *base32.Encoding, remainder []byte) ([]byte, bool) {
	if len(remainder) == 0 {
		return nil, false
	}
	if bytes.IndexByte(remainder, '=') >= 0 {
		decoded, err := enc.DecodeString(string(remainder))
		if err != nil {
			return nil, true
		}
		return decoded, false
	}

	trimmedLen := len(remainder)
	for trimmedLen > 0 && !isValidBase32RemainderLength(trimmedLen) {
		trimmedLen--
	}
	if trimmedLen == 0 {
		return nil, true
	}

	padded := append([]byte(nil), remainder[:trimmedLen]...)
	padded = append(padded, bytes.Repeat([]byte{'='}, 8-trimmedLen)...)
	decoded, err := enc.DecodeString(string(padded))
	if err != nil {
		return nil, true
	}
	return decoded, trimmedLen != len(remainder)
}

func isValidBase32RemainderLength(length int) bool {
	switch length {
	case 2, 4, 5, 7:
		return true
	default:
		return false
	}
}

func decodeBase16(data []byte, ignoreGarbage bool) ([]byte, bool) {
	filtered, ok := filterWholeEncodedInput(data, &basencBase16Lookup, ignoreGarbage)
	if !ok {
		return nil, true
	}
	if len(filtered)%2 != 0 {
		return nil, true
	}
	decoded := make([]byte, hex.DecodedLen(len(filtered)))
	if _, err := hex.Decode(decoded, filtered); err != nil {
		return nil, true
	}
	return decoded, false
}

func encodeBase2(data []byte, lsbf bool) string {
	var b strings.Builder
	b.Grow(len(data) * 8)
	for _, value := range data {
		if lsbf {
			for bit := range 8 {
				if value&(1<<bit) != 0 {
					b.WriteByte('1')
				} else {
					b.WriteByte('0')
				}
			}
			continue
		}
		for bit := 7; bit >= 0; bit-- {
			if value&(1<<bit) != 0 {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
	}
	return b.String()
}

func decodeBase2(data []byte, ignoreGarbage, lsbf bool) ([]byte, bool) {
	filtered, ok := filterWholeEncodedInput(data, &basencBase2Lookup, ignoreGarbage)
	if !ok {
		return nil, true
	}
	if len(filtered)%8 != 0 {
		return nil, true
	}

	decoded := make([]byte, 0, len(filtered)/8)
	for i := 0; i < len(filtered); i += 8 {
		var value byte
		for j := range 8 {
			if filtered[i+j] == '0' {
				continue
			}
			if lsbf {
				value |= 1 << j
			} else {
				value |= 1 << (7 - j)
			}
		}
		decoded = append(decoded, value)
	}
	return decoded, false
}

func encodeZ85(data []byte) (string, error) {
	if len(data)%4 != 0 {
		return "", errors.New("invalid input (length must be multiple of 4 characters)")
	}
	if len(data) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.Grow(len(data) / 4 * 5)
	divisors := [...]uint32{85 * 85 * 85 * 85, 85 * 85 * 85, 85 * 85, 85, 1}

	for i := 0; i < len(data); i += 4 {
		value := binary.BigEndian.Uint32(data[i : i+4])
		for _, divisor := range divisors {
			digit := value / divisor
			b.WriteByte(basencZ85Alphabet[digit])
			value %= divisor
		}
	}

	return b.String(), nil
}

func decodeZ85(data []byte, ignoreGarbage bool) ([]byte, bool) {
	filtered, ok := filterWholeEncodedInput(data, &basencZ85Lookup, ignoreGarbage)
	if !ok {
		return nil, true
	}
	if len(filtered)%5 != 0 {
		return nil, true
	}

	decoded := make([]byte, 0, len(filtered)/5*4)
	var block [4]byte
	for i := 0; i < len(filtered); i += 5 {
		var value uint64
		for _, b := range filtered[i : i+5] {
			digit := basencZ85DecodeMap[b]
			if digit < 0 {
				return nil, true
			}
			value = value*85 + uint64(digit)
		}
		if value > math.MaxUint32 {
			return nil, true
		}
		binary.BigEndian.PutUint32(block[:], uint32(value))
		decoded = append(decoded, block[:]...)
	}

	return decoded, false
}

func encodeBase58(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	leadingZeros := 0
	for leadingZeros < len(data) && data[leadingZeros] == 0 {
		leadingZeros++
	}
	if leadingZeros == len(data) {
		return strings.Repeat("1", leadingZeros)
	}

	value := new(big.Int).SetBytes(data[leadingZeros:])
	base := big.NewInt(58)
	mod := new(big.Int)
	result := make([]byte, 0, len(data)*138/100+1)

	for value.Sign() > 0 {
		value.DivMod(value, base, mod)
		result = append(result, basencBase58Alphabet[mod.Int64()])
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return strings.Repeat("1", leadingZeros) + string(result)
}

func decodeBase58(data []byte, ignoreGarbage bool) ([]byte, bool) {
	filtered, ok := filterWholeEncodedInput(data, &basencBase58Lookup, ignoreGarbage)
	if !ok {
		return nil, true
	}
	if len(filtered) == 0 {
		return nil, false
	}

	leadingOnes := 0
	for leadingOnes < len(filtered) && filtered[leadingOnes] == '1' {
		leadingOnes++
	}
	if leadingOnes == len(filtered) {
		return make([]byte, leadingOnes), false
	}

	value := big.NewInt(0)
	base := big.NewInt(58)
	for _, b := range filtered[leadingOnes:] {
		digit := basencBase58IndexMap[b]
		if digit < 0 {
			return nil, true
		}
		value.Mul(value, base)
		value.Add(value, big.NewInt(int64(digit)))
	}

	decoded := value.Bytes()
	if len(decoded) == 0 {
		decoded = []byte{0}
	}
	result := make([]byte, leadingOnes+len(decoded))
	copy(result[leadingOnes:], decoded)
	return result, false
}

func filterWholeEncodedInput(data []byte, lookup *[256]bool, ignoreGarbage bool) ([]byte, bool) {
	filtered := make([]byte, 0, len(data))
	for _, b := range data {
		switch {
		case b == '\n' || b == '\r':
			continue
		case lookup[b]:
			filtered = append(filtered, b)
		case ignoreGarbage:
			continue
		default:
			return nil, false
		}
	}
	return filtered, true
}

func basencPathError(inv *Invocation, name string, err error) error {
	return exitf(inv, 1, "basenc: %s: %s", name, basencErrorText(err))
}

func basencReadError(inv *Invocation, err error) error {
	return exitf(inv, 1, "basenc: read error: %s", basencErrorText(err))
}

func basencErrorText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return basencErrorText(pathErr.Err)
	}

	switch {
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, stdfs.ErrPermission):
		return "Permission denied"
	case errors.Is(err, syscall.ENOTDIR):
		return "Not a directory"
	case errors.Is(err, syscall.EISDIR):
		return "Is a directory"
	case errors.Is(err, stdfs.ErrInvalid):
		return "Invalid argument"
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "is a directory"):
		return "Is a directory"
	case strings.Contains(lower, "not a directory"):
		return "Not a directory"
	case strings.Contains(lower, "permission denied"):
		return "Permission denied"
	case strings.Contains(lower, "no such file or directory"):
		return "No such file or directory"
	default:
		return err.Error()
	}
}
