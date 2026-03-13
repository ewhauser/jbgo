package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/big"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

const factorWriterBufferSize = 4 * 1024

var factorSmallDivisors = []int64{
	3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47,
	53, 59, 61, 67, 71, 73, 79, 83, 89, 97,
}

type Factor struct{}

func NewFactor() *Factor {
	return &Factor{}
}

func (c *Factor) Name() string {
	return "factor"
}

func (c *Factor) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Factor) Spec() CommandSpec {
	return CommandSpec{
		Name:  "factor",
		About: "Print the prime factors of the given NUMBER(s).",
		Usage: "factor [OPTION]... [NUMBER]...",
		Options: []OptionSpec{
			{Name: "exponents", Short: 'h', Long: "exponents", Help: "Print factors in the form p^e", Repeatable: true},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "number", ValueName: "NUMBER", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:  true,
			GroupShortOptions: true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			return factorRenderHelp(w, &spec)
		},
	}
}

func (c *Factor) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	switch factorSpecialAction(matches) {
	case "help":
		spec := c.Spec()
		return RenderCommandHelp(inv.Stdout, &spec)
	case "version":
		spec := c.Spec()
		return RenderCommandVersion(inv.Stdout, &spec)
	}

	writer := bufio.NewWriterSize(inv.Stdout, factorWriterBufferSize)
	printExponents := matches.Count("exponents") > 0
	hadWarning := false

	if numbers := matches.Args("number"); len(numbers) > 0 {
		for _, raw := range numbers {
			if err := ctx.Err(); err != nil {
				return err
			}
			warned, err := factorWriteToken(ctx, inv, writer, []byte(strings.TrimSpace(raw)), printExponents)
			hadWarning = hadWarning || warned
			if err != nil {
				return err
			}
		}
	} else {
		warned, err := factorReadStdin(ctx, inv, writer, printExponents)
		hadWarning = hadWarning || warned
		if err != nil {
			return err
		}
	}

	if err := writer.Flush(); err != nil {
		return factorWriteError(inv, err)
	}
	if hadWarning {
		return &ExitError{Code: 1}
	}
	return nil
}

func factorSpecialAction(matches *ParsedCommand) string {
	order := matches.OptionOrder()
	for i := len(order) - 1; i >= 0; i-- {
		switch order[i] {
		case "help", "version":
			return order[i]
		}
	}
	return ""
}

func factorRenderHelp(w io.Writer, spec *CommandSpec) error {
	text := spec.About + "\n\n" +
		"Usage: " + spec.Usage + "\n\n" +
		"If no NUMBER is specified, read it from standard input.\n\n" +
		"Options:\n" +
		"  -h, --exponents  Print factors in the form p^e\n" +
		"      --help       display this help and exit\n" +
		"      --version    output version information and exit\n"
	_, err := io.WriteString(w, text)
	return err
}

func factorReadStdin(ctx context.Context, inv *Invocation, writer *bufio.Writer, printExponents bool) (bool, error) {
	reader := bufio.NewReader(inv.Stdin)
	hadWarning := false
	for {
		if err := ctx.Err(); err != nil {
			return hadWarning, err
		}

		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		if runtime.GOOS == "windows" && len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}

		warned, lineErr := factorWriteLine(ctx, inv, writer, line, printExponents)
		hadWarning = hadWarning || warned
		if lineErr != nil {
			return hadWarning, lineErr
		}

		if err == io.EOF {
			return hadWarning, nil
		}
		if err != nil {
			return hadWarning, exitf(inv, 1, "factor: error reading input: %v", err)
		}
	}
}

func factorWriteLine(ctx context.Context, inv *Invocation, writer *bufio.Writer, line []byte, printExponents bool) (bool, error) {
	hadWarning := false
	display := true
	prev := 0
	for i := 0; i <= len(line); i++ {
		hasNull := false
		if i < len(line) {
			switch line[i] {
			case ' ', '\t':
			case 0:
				hasNull = true
			default:
				continue
			}
		}

		if display && (prev != i || hasNull) {
			warned, err := factorWriteToken(ctx, inv, writer, line[prev:i], printExponents)
			hadWarning = hadWarning || warned
			if err != nil {
				return hadWarning, err
			}
		}

		display = !hasNull
		prev = i + 1
	}
	return hadWarning, nil
}

func factorWriteToken(ctx context.Context, inv *Invocation, writer *bufio.Writer, token []byte, printExponents bool) (bool, error) {
	number, err := factorParseNumber(token)
	if err != nil {
		factorWarnInvalid(inv, token)
		return true, nil
	}

	factors, err := factorPrimeFactors(ctx, number)
	if err != nil {
		return false, err
	}
	if err := factorWriteResult(writer, number, factors, printExponents); err != nil {
		return false, factorWriteError(inv, err)
	}
	return false, nil
}

func factorWarnInvalid(inv *Invocation, token []byte) {
	if inv == nil || inv.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(inv.Stderr, "factor: warning: %s: invalid digit found in string\n", factorQuoteBytes(token))
}

func factorParseNumber(token []byte) (*big.Int, error) {
	if len(token) == 0 || token[0] == '-' || !utf8.Valid(token) {
		return nil, fmt.Errorf("invalid number")
	}
	number, ok := new(big.Int).SetString(string(token), 10)
	if !ok || number.Sign() < 0 {
		return nil, fmt.Errorf("invalid number")
	}
	return number, nil
}

func factorQuoteBytes(token []byte) string {
	var builder strings.Builder
	builder.WriteByte('\'')
	for len(token) > 0 {
		r, size := utf8.DecodeRune(token)
		if r == utf8.RuneError && size == 1 {
			_, _ = fmt.Fprintf(&builder, "\\%03o", token[0])
			token = token[1:]
			continue
		}
		for _, ch := range string(token[:size]) {
			if ch == '\'' {
				builder.WriteString("'\\''")
				continue
			}
			builder.WriteRune(ch)
		}
		token = token[size:]
	}
	builder.WriteByte('\'')
	return builder.String()
}

func factorPrimeFactors(ctx context.Context, number *big.Int) ([]*big.Int, error) {
	if number.Sign() <= 0 || number.Cmp(big.NewInt(1)) == 0 {
		return nil, nil
	}

	remaining := new(big.Int).Set(number)
	factors := make([]*big.Int, 0, 8)
	for remaining.Bit(0) == 0 {
		factors = append(factors, big.NewInt(2))
		remaining.Rsh(remaining, 1)
	}

	for _, divisor := range factorSmallDivisors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		divisorBig := big.NewInt(divisor)
		for new(big.Int).Mod(remaining, divisorBig).Sign() == 0 {
			factors = append(factors, big.NewInt(divisor))
			remaining.Quo(remaining, divisorBig)
		}
		if remaining.Cmp(big.NewInt(1)) == 0 {
			return factors, nil
		}
	}

	if remaining.Cmp(big.NewInt(1)) > 0 {
		if err := factorFactorRecursive(ctx, remaining, &factors); err != nil {
			return nil, err
		}
	}

	sort.Slice(factors, func(i, j int) bool {
		return factors[i].Cmp(factors[j]) < 0
	})
	return factors, nil
}

func factorFactorRecursive(ctx context.Context, number *big.Int, factors *[]*big.Int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if number.Cmp(big.NewInt(1)) == 0 {
		return nil
	}
	if number.ProbablyPrime(32) {
		*factors = append(*factors, new(big.Int).Set(number))
		return nil
	}

	divisor, err := factorPollardRho(ctx, number)
	if err != nil {
		return err
	}
	if divisor == nil || divisor.Cmp(big.NewInt(1)) == 0 || divisor.Cmp(number) == 0 {
		return fmt.Errorf("factor: factorization incomplete")
	}

	quotient := new(big.Int).Quo(new(big.Int).Set(number), divisor)
	if err := factorFactorRecursive(ctx, divisor, factors); err != nil {
		return err
	}
	return factorFactorRecursive(ctx, quotient, factors)
}

func factorPollardRho(ctx context.Context, number *big.Int) (*big.Int, error) {
	if number.Bit(0) == 0 {
		return big.NewInt(2), nil
	}

	one := big.NewInt(1)
	for seed := int64(1); seed <= 32; seed++ {
		x := big.NewInt(seed + 1)
		y := big.NewInt(seed + 1)
		c := big.NewInt(seed)
		d := big.NewInt(1)
		diff := new(big.Int)

		for steps := 0; steps < 1<<20 && d.Cmp(one) == 0; steps++ {
			if steps%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}

			factorRhoAdvance(x, c, number)
			factorRhoAdvance(y, c, number)
			factorRhoAdvance(y, c, number)

			diff.Sub(x, y)
			diff.Abs(diff)
			d.GCD(nil, nil, diff, number)
		}

		if d.Cmp(one) > 0 && d.Cmp(number) < 0 {
			return new(big.Int).Set(d), nil
		}
	}

	return nil, fmt.Errorf("factor: factorization incomplete")
}

func factorRhoAdvance(value, c, modulus *big.Int) {
	value.Mul(value, value)
	value.Add(value, c)
	value.Mod(value, modulus)
}

func factorWriteResult(writer *bufio.Writer, number *big.Int, factors []*big.Int, printExponents bool) error {
	if _, err := writer.WriteString(number.String()); err != nil {
		return err
	}
	if err := writer.WriteByte(':'); err != nil {
		return err
	}
	if printExponents {
		for i := 0; i < len(factors); {
			j := i + 1
			for j < len(factors) && factors[j].Cmp(factors[i]) == 0 {
				j++
			}
			if _, err := writer.WriteString(" " + factors[i].String()); err != nil {
				return err
			}
			if j-i > 1 {
				if _, err := fmt.Fprintf(writer, "^%d", j-i); err != nil {
					return err
				}
			}
			i = j
		}
	} else {
		for _, factor := range factors {
			if _, err := writer.WriteString(" " + factor.String()); err != nil {
				return err
			}
		}
	}
	if err := writer.WriteByte('\n'); err != nil {
		return err
	}
	return writer.Flush()
}

func factorWriteError(inv *Invocation, err error) error {
	return exitf(inv, 1, "factor: write error: %v", err)
}

var _ Command = (*Factor)(nil)
var _ SpecProvider = (*Factor)(nil)
var _ ParsedRunner = (*Factor)(nil)
