package expand

import (
	"strings"

	"github.com/ewhauser/gbash/shell/syntax"
)

func (cfg *Config) langVariant() syntax.LangVariant {
	if cfg == nil || cfg.LangVariant == 0 || cfg.LangVariant == syntax.LangAuto {
		return syntax.LangBash
	}
	return cfg.LangVariant
}

func (cfg *Config) quoteForVariant(value string) (string, error) {
	return syntax.Quote(value, cfg.langVariant())
}

func (cfg *Config) parseVarRef(src string) (*syntax.VarRef, error) {
	return syntax.NewParser(syntax.Variant(cfg.langVariant())).VarRef(strings.NewReader(src))
}
