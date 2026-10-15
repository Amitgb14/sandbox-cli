package api

import "context"

type backgroundKey struct{}

// Background returns a context whose requests carry HeaderBackground: a
// client's own bookkeeping, which neither holds a sandbox's idle timeout off
// nor resumes one suspended for being idle.
func Background(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

func background(ctx context.Context) bool {
	b, _ := ctx.Value(backgroundKey{}).(bool)
	return b
}
