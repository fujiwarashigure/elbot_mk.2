package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"elbot/internal/config"
	"elbot/internal/llm"
	"elbot/internal/session"
	"elbot/internal/storage"
)

// listModelsFake is a scripted llm.LLM whose ListModels can block and fail, so
// the model-list cache can be tested without any network access.
type listModelsFake struct {
	mu      sync.Mutex
	calls   int
	models  []string
	err     error
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newListModelsFake(models ...string) *listModelsFake {
	return &listModelsFake{models: models}
}

func (f *listModelsFake) ChatStream(context.Context, llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("chat is not used in this test")
}

func (f *listModelsFake) ListModels(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	f.calls++
	started, release := f.started, f.release
	models, err := append([]string(nil), f.models...), f.err
	f.mu.Unlock()
	if started != nil {
		f.once.Do(func() { close(started) })
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return models, err
}

func (f *listModelsFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *listModelsFake) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func newModelListAgent(t *testing.T, client llm.LLM, providerModels []string) *Agent {
	t.Helper()
	return mustNewWithOptions(t, Options{
		Platform: &fakePlatform{},
		Clients:  map[string]llm.LLM{"prov": client},
		ModeModels: map[string]config.ModelSelection{
			storage.SessionModeWork: {Provider: "prov", Model: "alpha"},
		},
		Providers: map[string]config.ProviderConfig{
			"prov": {BaseURL: "http://example.invalid", APIKey: "test-key", Models: providerModels},
		},
		Store:           newTestStore(t),
		CommandPrefixes: []string{"/"},
		SessionConfig:   session.Config{NamingConfig: session.NamingConfig{TriggerStep: 1}, DefaultMode: storage.SessionModeWork},
	})
}

func TestModelListCacheCoalescesConcurrentFetches(t *testing.T) {
	client := newListModelsFake("alpha", "beta")
	client.started = make(chan struct{})
	client.release = make(chan struct{})
	a := newModelListAgent(t, client, nil)

	const callers = 5
	type outcome struct {
		models []string
		err    error
	}
	results := make(chan outcome, callers)
	for i := 0; i < callers; i++ {
		go func() {
			models, err := a.cachedProviderModels("prov", true)
			results <- outcome{models: models, err: err}
		}()
	}
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("model listing never started")
	}
	// Let every caller reach the in-flight entry before the fetch completes.
	time.Sleep(50 * time.Millisecond)
	if got := client.callCount(); got != 1 {
		t.Fatalf("upstream ListModels calls = %d, want 1 while the fetch is in flight", got)
	}
	close(client.release)
	for i := 0; i < callers; i++ {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatalf("caller error: %v", got.err)
			}
			if len(got.models) != 2 || got.models[0] != "alpha" || got.models[1] != "beta" {
				t.Fatalf("caller models = %#v", got.models)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for coalesced callers")
		}
	}
	if got := client.callCount(); got != 1 {
		t.Fatalf("upstream ListModels calls = %d, want 1", got)
	}
}

func TestModelListCacheKeepsLastKnownGoodOnFailure(t *testing.T) {
	client := newListModelsFake("alpha", "beta")
	a := newModelListAgent(t, client, nil)

	first, err := a.cachedProviderModels("prov", true)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first models = %#v", first)
	}

	// The provider goes down. A forced refresh must report the error but still
	// hand back the models it listed while it was healthy.
	client.setError(errors.New("provider down"))
	second, secondErr := a.cachedProviderModels("prov", true)
	if secondErr == nil {
		t.Fatal("expected the provider error to be reported")
	}
	if len(second) != 2 || second[0] != "alpha" || second[1] != "beta" {
		t.Fatalf("models after failure = %#v, want the last known good list", second)
	}
	if got := client.callCount(); got != 2 {
		t.Fatalf("upstream ListModels calls = %d, want 2", got)
	}

	// Cached failure entries still carry the fallback list.
	third, thirdErr := a.cachedProviderModels("prov", false)
	if thirdErr == nil || len(third) != 2 {
		t.Fatalf("cached failure result = %#v, %v", third, thirdErr)
	}
}

func TestMergeModelNames(t *testing.T) {
	merged := mergeModelNames([]string{"local-only", "beta"}, []string{"beta", "alpha"})
	want := []string{"alpha", "beta", "local-only"}
	if len(merged) != len(want) {
		t.Fatalf("merged = %#v, want %#v", merged, want)
	}
	for i := range want {
		if merged[i] != want[i] {
			t.Fatalf("merged = %#v, want %#v", merged, want)
		}
	}
	if got := mergeModelNames([]string{"a"}, nil); len(got) != 1 || got[0] != "a" {
		t.Fatalf("merge without last known good = %#v", got)
	}
}
