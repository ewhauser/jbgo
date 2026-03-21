package builtins

import (
	"context"
	"fmt"
	"strings"
)

const hostnameEnvKey = "GBASH_UNAME_NODENAME"
const defaultHostname = "gbash"

type Hostname struct{}

func NewHostname() *Hostname {
	return &Hostname{}
}

func (c *Hostname) Name() string {
	return "hostname"
}

func (c *Hostname) Run(_ context.Context, inv *Invocation) error {
	name := defaultHostname
	if inv != nil && inv.Env != nil {
		if value := strings.TrimSpace(inv.Env[hostnameEnvKey]); value != "" {
			name = value
		}
	}
	if _, err := fmt.Fprintln(inv.Stdout, name); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Hostname)(nil)
