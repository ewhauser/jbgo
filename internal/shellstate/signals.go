package shellstate

import "context"

type SignalDispatcher func(target string, signal int) error

type signalDispatcherKey struct{}
type signalFamilyKey struct{}

type SignalFamily struct {
	Owner         any
	StablePID     int
	ParentBASHPID int
}

func WithSignalDispatcher(ctx context.Context, dispatcher SignalDispatcher) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if dispatcher == nil {
		return ctx
	}
	return context.WithValue(ctx, signalDispatcherKey{}, dispatcher)
}

func SignalDispatcherFromContext(ctx context.Context) SignalDispatcher {
	if ctx == nil {
		return nil
	}
	dispatcher, _ := ctx.Value(signalDispatcherKey{}).(SignalDispatcher)
	return dispatcher
}

func WithSignalFamily(ctx context.Context, family SignalFamily) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if family.Owner == nil && family.StablePID == 0 && family.ParentBASHPID == 0 {
		return ctx
	}
	return context.WithValue(ctx, signalFamilyKey{}, family)
}

func SignalFamilyFromContext(ctx context.Context) (SignalFamily, bool) {
	if ctx == nil {
		return SignalFamily{}, false
	}
	family, ok := ctx.Value(signalFamilyKey{}).(SignalFamily)
	return family, ok
}
