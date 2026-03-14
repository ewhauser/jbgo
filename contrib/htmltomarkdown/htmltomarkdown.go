package htmltomarkdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"strings"
	"unicode/utf8"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/ewhauser/gbash/commands"
)

const htmlToMarkdownName = "html-to-markdown"

type HTMLToMarkdown struct{}

type htmlToMarkdownOptions struct {
	bullet         string
	codeFence      string
	horizontalRule string
	headingStyle   string
}

func NewHTMLToMarkdown() *HTMLToMarkdown {
	return &HTMLToMarkdown{}
}

func Register(registry commands.CommandRegistry) error {
	if registry == nil {
		return nil
	}
	return registry.Register(NewHTMLToMarkdown())
}

func (c *HTMLToMarkdown) Name() string {
	return htmlToMarkdownName
}

func (c *HTMLToMarkdown) Run(ctx context.Context, inv *commands.Invocation) error {
	if hasHTMLToMarkdownHelpFlag(inv.Args) {
		spec := c.Spec()
		return commands.RenderCommandHelp(inv.Stdout, &spec)
	}

	opts, files, err := parseHTMLToMarkdownArgs(inv)
	if err != nil {
		return err
	}

	input, err := loadHTMLToMarkdownInput(ctx, inv, files)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input) == "" {
		return nil
	}

	markdown, err := convertHTMLToMarkdown(input, opts)
	if err != nil {
		return commands.Exitf(inv, 1, "%s: conversion error: %v", c.Name(), err)
	}
	if _, err := io.WriteString(inv.Stdout, markdown+"\n"); err != nil {
		return &commands.ExitError{Code: 1, Err: err}
	}
	return nil
}

func (c *HTMLToMarkdown) Spec() commands.CommandSpec {
	return commands.CommandSpec{
		Name:  c.Name(),
		About: "Convert HTML to Markdown.",
		Usage: "html-to-markdown [OPTION]... [FILE]",
		Options: []commands.OptionSpec{
			{Name: "bullet", Short: 'b', Long: "bullet", ValueName: "CHAR", Arity: commands.OptionRequiredValue, Help: "bullet character for unordered lists (-, +, or *)"},
			{Name: "code", Short: 'c', Long: "code", ValueName: "FENCE", Arity: commands.OptionRequiredValue, Help: "fence style for code blocks (``` or ~~~)"},
			{Name: "hr", Short: 'r', Long: "hr", ValueName: "STRING", Arity: commands.OptionRequiredValue, Help: "string for horizontal rules"},
			{Name: "heading-style", Long: "heading-style", ValueName: "STYLE", Arity: commands.OptionRequiredValue, Help: "heading style: atx or setext"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
		},
		Args: []commands.ArgSpec{
			{Name: "file", ValueName: "FILE", Help: "read HTML from FILE instead of standard input"},
		},
		AfterHelp: "Reads HTML from FILE or standard input and writes Markdown to standard output.\n\nExamples:\n  echo '<h1>Hello</h1><p>World</p>' | html-to-markdown\n  html-to-markdown page.html",
	}
}

func hasHTMLToMarkdownHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func parseHTMLToMarkdownArgs(inv *commands.Invocation) (htmlToMarkdownOptions, []string, error) {
	opts := htmlToMarkdownOptions{
		bullet:         "-",
		codeFence:      "```",
		horizontalRule: "---",
		headingStyle:   "atx",
	}
	files := make([]string, 0, len(inv.Args))
	args := inv.Args
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-b" || arg == "--bullet":
			if i+1 < len(args) && isHTMLToMarkdownBulletValue(args[i+1]) {
				i++
				opts.bullet = args[i]
			} else {
				opts.bullet = "-"
			}
		case strings.HasPrefix(arg, "--bullet="):
			opts.bullet = strings.TrimPrefix(arg, "--bullet=")
		case arg == "-c" || arg == "--code":
			if i+1 < len(args) && isHTMLToMarkdownCodeFenceValue(args[i+1]) {
				i++
				opts.codeFence = args[i]
			} else {
				opts.codeFence = "```"
			}
		case strings.HasPrefix(arg, "--code="):
			opts.codeFence = strings.TrimPrefix(arg, "--code=")
		case arg == "-r" || arg == "--hr":
			if i+1 < len(args) && isHTMLToMarkdownHorizontalRuleValue(args[i+1]) {
				i++
				opts.horizontalRule = args[i]
			} else {
				opts.horizontalRule = "---"
			}
		case strings.HasPrefix(arg, "--hr="):
			opts.horizontalRule = strings.TrimPrefix(arg, "--hr=")
		case arg == "--heading-style":
			if i+1 < len(args) && isHTMLToMarkdownHeadingStyleValue(args[i+1]) {
				i++
				opts.headingStyle = args[i]
			}
		case strings.HasPrefix(arg, "--heading-style="):
			style := strings.TrimPrefix(arg, "--heading-style=")
			if isHTMLToMarkdownHeadingStyleValue(style) {
				opts.headingStyle = style
			}
		case arg == "-":
			files = append(files, arg)
		case strings.HasPrefix(arg, "--"):
			return htmlToMarkdownOptions{}, nil, htmlToMarkdownUsageError(inv, "unrecognized option '%s'", arg)
		case strings.HasPrefix(arg, "-"):
			return htmlToMarkdownOptions{}, nil, htmlToMarkdownUsageError(inv, "unrecognized option '%s'", arg)
		default:
			files = append(files, arg)
		}
	}
	return opts, files, nil
}

func isHTMLToMarkdownBulletValue(value string) bool {
	return value == "-" || value == "+" || value == "*"
}

func isHTMLToMarkdownCodeFenceValue(value string) bool {
	return value == "```" || value == "~~~"
}

func isHTMLToMarkdownHorizontalRuleValue(value string) bool {
	if utf8.RuneCountInString(value) < 3 {
		return false
	}
	for _, ch := range value {
		switch ch {
		case '-', '_', '*':
		default:
			return false
		}
	}
	return true
}

func isHTMLToMarkdownHeadingStyleValue(value string) bool {
	return value == "atx" || value == "setext"
}

func htmlToMarkdownUsageError(inv *commands.Invocation, format string, args ...any) error {
	return commands.Exitf(inv, 1, "%s: %s\nTry '%s --help' for more information.", htmlToMarkdownName, fmt.Sprintf(format, args...), htmlToMarkdownName)
}

func loadHTMLToMarkdownInput(ctx context.Context, inv *commands.Invocation, files []string) (string, error) {
	if len(files) == 0 || (len(files) == 1 && files[0] == "-") {
		data, err := commands.ReadAllStdin(ctx, inv)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	file := files[0]

	info, err := inv.FS.Stat(ctx, file)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return "", commands.Exitf(inv, 1, "%s: %s: No such file or directory", htmlToMarkdownName, file)
		}
		return "", err
	}
	if info.IsDir() {
		return "", commands.Exitf(inv, 1, "%s: %s: Is a directory", htmlToMarkdownName, file)
	}

	data, err := inv.FS.ReadFile(ctx, file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func convertHTMLToMarkdown(input string, opts htmlToMarkdownOptions) (string, error) {
	pluginOptions := []commonmark.OptionFunc{
		commonmark.WithBulletListMarker(opts.bullet),
		commonmark.WithCodeBlockFence(opts.codeFence),
		commonmark.WithHorizontalRule(opts.horizontalRule),
		commonmark.WithEmDelimiter("_"),
	}

	switch opts.headingStyle {
	case "setext":
		pluginOptions = append(pluginOptions, commonmark.WithHeadingStyle(commonmark.HeadingStyleSetext))
	default:
		pluginOptions = append(pluginOptions, commonmark.WithHeadingStyle(commonmark.HeadingStyleATX))
	}

	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(pluginOptions...),
		),
	)
	conv.Register.TagType("script", converter.TagTypeRemove, converter.PriorityStandard)
	conv.Register.TagType("style", converter.TagTypeRemove, converter.PriorityStandard)

	output, err := conv.ConvertString(input)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

var _ commands.Command = (*HTMLToMarkdown)(nil)
var _ commands.SpecProvider = (*HTMLToMarkdown)(nil)
