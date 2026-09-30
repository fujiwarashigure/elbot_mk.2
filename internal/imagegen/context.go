package imagegen

import (
	"context"
	"strings"
)

type profileContextKey struct{}

// WithProfile records the per-turn image profile selected by @image:<name>.
func WithProfile(ctx context.Context, name string) context.Context {
	name = strings.TrimSpace(name)
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, profileContextKey{}, name)
}

// ProfileFrom returns the per-turn image profile, if any.
func ProfileFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(profileContextKey{}).(string)
	return strings.TrimSpace(name)
}
