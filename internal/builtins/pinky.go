package builtins

import "context"

type Pinky struct{}

func NewPinky() *Pinky {
	return &Pinky{}
}

func (c *Pinky) Name() string {
	return "pinky"
}

func (c *Pinky) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Pinky)(nil)
