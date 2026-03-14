// Package extras registers all official opt-in contrib commands.
package extras

import (
	"fmt"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	contribawk "github.com/ewhauser/gbash/contrib/awk"
	contribjq "github.com/ewhauser/gbash/contrib/jq"
	contribsqlite3 "github.com/ewhauser/gbash/contrib/sqlite3"
	contribyq "github.com/ewhauser/gbash/contrib/yq"
)

// FullRegistry returns the default registry plus all official contrib commands.
func FullRegistry() *commands.Registry {
	registry := gbash.DefaultRegistry()
	if err := Register(registry); err != nil {
		panic(fmt.Sprintf("extras: register full registry: %v", err))
	}
	return registry
}

// Register adds every official contrib command module to the registry.
func Register(registry commands.CommandRegistry) error {
	if registry == nil {
		return nil
	}
	if err := contribawk.Register(registry); err != nil {
		return err
	}
	if err := contribjq.Register(registry); err != nil {
		return err
	}
	if err := contribsqlite3.Register(registry); err != nil {
		return err
	}
	return contribyq.Register(registry)
}
