package tool

import (
	"context"
	"testing"
)

func TestVisionRequiredAvailability(t *testing.T) {
	info := NewBuilder("view_image").VisionRequired().BuildInfo()
	if !info.VisionRequired {
		t.Fatal("builder must keep the vision requirement")
	}
	if !InfoAvailableInContext(context.Background(), info) {
		t.Fatal("a context without capabilities must keep tools available")
	}
	withoutVision := WithCapabilities(context.Background(), Capabilities{Vision: false})
	if InfoAvailableInContext(withoutVision, info) {
		t.Fatal("a model without vision must hide the tool")
	}
	withVision := WithCapabilities(context.Background(), Capabilities{Vision: true})
	if !InfoAvailableInContext(withVision, info) {
		t.Fatal("a vision model must keep the tool")
	}
	plain := NewBuilder("plain").BuildInfo()
	if !InfoAvailableInContext(withoutVision, plain) {
		t.Fatal("tools without the requirement stay available")
	}
}
