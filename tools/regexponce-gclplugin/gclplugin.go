// Package gclplugin registers regexponce as a golangci-lint v2 module plugin.
package gclplugin

import (
	"github.com/ewhauser/gbash/tools/regexponce-gclplugin/regexponce"
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("regexponce", New)
}

// New returns the golangci-lint plugin wrapping the regexponce analyzer.
func New(_ any) (register.LinterPlugin, error) {
	return &plugin{}, nil
}

type plugin struct{}

func (p *plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{regexponce.Analyzer}, nil
}

func (p *plugin) GetLoadMode() string { return register.LoadModeTypesInfo }
