package tool

import "context"

type capabilitiesContextKey struct{}

// Capabilities describes what the model serving the current request can accept.
// It travels with the request context so discovery, preload and execution can
// hide tools the model cannot use.
type Capabilities struct {
	// Vision is true when the current model accepts image input.
	Vision bool
}

func WithCapabilities(ctx context.Context, capabilities Capabilities) context.Context {
	return context.WithValue(ctx, capabilitiesContextKey{}, capabilities)
}

func CapabilitiesFromContext(ctx context.Context) (Capabilities, bool) {
	capabilities, ok := ctx.Value(capabilitiesContextKey{}).(Capabilities)
	return capabilities, ok
}

// VisionAvailable reports whether the current model accepts images. A context
// without capabilities keeps the historic behaviour, so callers that never set
// them (background jobs, direct tool calls in tests) are unaffected.
func VisionAvailable(ctx context.Context) bool {
	capabilities, ok := CapabilitiesFromContext(ctx)
	if !ok {
		return true
	}
	return capabilities.Vision
}
