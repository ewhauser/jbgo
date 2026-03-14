package htmltomarkdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"strings"

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
	file           string
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
	return commands.RunCommand(ctx, c, inv)
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
		},
		Args: []commands.ArgSpec{
			{Name: "file", ValueName: "FILE", Help: "read HTML from FILE instead of standard input"},
		},
		Parse: commands.ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		AfterHelp: "Reads HTML from FILE or standard input and writes Markdown to standard output.\n\nExamples:\n  echo '<h1>Hello</h1><p>World</p>' | html-to-markdown\n  html-to-markdown page.html",
	}
}

func (c *HTMLToMarkdown) RunParsed(ctx context.Context, inv *commands.Invocation, matches *commands.ParsedCommand) error {
	opts, err := parseHTMLToMarkdownOptions(inv, matches)
	if err != nil {
		return err
	}

	input, err := loadHTMLToMarkdownInput(ctx, inv, opts.file)
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

func parseHTMLToMarkdownOptions(inv *commands.Invocation, matches *commands.ParsedCommand) (htmlToMarkdownOptions, error) {
	opts := htmlToMarkdownOptions{
		bullet:         "-",
		codeFence:      "```",
		horizontalRule: "---",
		headingStyle:   "atx",
	}
	if matches == nil {
		return opts, nil
	}

	if matches.Has("bullet") {
		opts.bullet = matches.Value("bullet")
	}
	if matches.Has("code") {
		opts.codeFence = matches.Value("code")
	}
	if matches.Has("hr") {
		opts.horizontalRule = matches.Value("hr")
	}
	if matches.Has("heading-style") {
		opts.headingStyle = matches.Value("heading-style")
	}
	opts.file = matches.Arg("file")

	if err := validateHTMLToMarkdownOptions(inv, opts); err != nil {
		return htmlToMarkdownOptions{}, err
	}
	return opts, nil
}

func validateHTMLToMarkdownOptions(inv *commands.Invocation, opts htmlToMarkdownOptions) error {
	switch opts.bullet {
	case "-", "+", "*":
	default:
		return htmlToMarkdownUsageError(inv, "invalid value %q for --bullet (expected -, +, or *)", opts.bullet)
	}

	switch opts.codeFence {
	case "```", "~~~":
	default:
		return htmlToMarkdownUsageError(inv, "invalid value %q for --code (expected ``` or ~~~)", opts.codeFence)
	}

	switch opts.headingStyle {
	case "atx", "setext":
	default:
		return htmlToMarkdownUsageError(inv, "invalid value %q for --heading-style (expected atx or setext)", opts.headingStyle)
	}

	if countHTMLToMarkdownRuleChars(opts.horizontalRule) < 3 {
		return htmlToMarkdownUsageError(inv, "invalid value %q for --hr (expected at least 3 of -, _, or *)", opts.horizontalRule)
	}

	return nil
}

func countHTMLToMarkdownRuleChars(value string) int {
	count := 0
	for _, ch := range value {
		switch ch {
		case '-', '_', '*':
			count++
		}
	}
	return count
}

func htmlToMarkdownUsageError(inv *commands.Invocation, format string, args ...any) error {
	return commands.Exitf(inv, 1, "%s: %s\nTry '%s --help' for more information.", htmlToMarkdownName, fmt.Sprintf(format, args...), htmlToMarkdownName)
}

func loadHTMLToMarkdownInput(ctx context.Context, inv *commands.Invocation, file string) (string, error) {
	if file == "" || file == "-" {
		data, err := commands.ReadAllStdin(ctx, inv)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

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
	conv.Register.TagType("footer", converter.TagTypeRemove, converter.PriorityStandard)

	output, err := conv.ConvertString(input)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

var _ commands.Command = (*HTMLToMarkdown)(nil)
var _ commands.SpecProvider = (*HTMLToMarkdown)(nil)
var _ commands.ParsedRunner = (*HTMLToMarkdown)(nil)
