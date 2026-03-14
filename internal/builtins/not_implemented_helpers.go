package builtins

func runNotImplemented(inv *Invocation, name string) error {
	return Exitf(inv, 1, "%s: not implemented", name)
}
