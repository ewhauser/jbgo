package main

import (
	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/cli"
	"github.com/ewhauser/gbash/contrib/extras"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = ""
	builtBy = ""
)

func newCLIConfig() cli.Config {
	return cli.Config{
		Name: "gbash-extras",
		Build: &cli.BuildInfo{
			Version: version,
			Commit:  commit,
			Date:    date,
			BuiltBy: builtBy,
		},
		BaseOptions: []gbash.Option{
			gbash.WithRegistry(extras.FullRegistry()),
		},
	}
}
