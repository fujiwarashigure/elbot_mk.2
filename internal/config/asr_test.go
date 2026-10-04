package config

import (
	"path/filepath"
	"testing"
)

func TestASRDefaultsAndAudioSupport(t *testing.T) {
	cfg := Default()
	asr := cfg.ASR.Normalized()
	if asr.TimeoutSeconds != 120 || asr.MaxAudioBytes != 20*1024*1024 || asr.MaxConcurrent != 2 || asr.QueueSize != 8 || asr.MaxSegments != 4 {
		t.Fatalf("asr defaults = %#v", asr)
	}
	if asr.CacheTTLSeconds != 1800 || asr.CacheMaxEntries != 128 || asr.NegativeCacheTTLSeconds != 30 || asr.NegativeCacheMaxEntries != 128 {
		t.Fatalf("asr cache defaults = %#v", asr)
	}
	if cfg.ASR.IsEnabled() {
		t.Fatal("asr must be opt-in")
	}
	if cfg.AudioSupportFor("missing", "model") != AudioUnknown {
		t.Fatalf("missing provider audio support = %v", cfg.AudioSupportFor("missing", "model"))
	}
	cfg.Providers["p"] = ProviderConfig{
		Audio:        boolPtr(true),
		ModelConfigs: map[string]ModelConfig{"disabled": {Audio: boolPtr(false)}},
	}
	if got := cfg.AudioSupportFor("p", "other"); got != AudioSupported {
		t.Fatalf("provider audio support = %v", got)
	}
	if got := cfg.AudioSupportFor("p", "disabled"); got != AudioUnsupported {
		t.Fatalf("model audio support = %v", got)
	}
}

func TestLoadServicesASROverridesLegacySection(t *testing.T) {
	configDir := t.TempDir()
	appPath := filepath.Join(configDir, "app.toml")
	servicesPath := filepath.Join(configDir, "services.toml")
	statePath := filepath.Join(configDir, "state.toml")

	writeFile(t, appPath, `
[config_files]
services = "services.toml"
state = "state.toml"

[asr]
enabled = false
provider = "legacy"
model = "legacy-whisper"
`)
	writeFile(t, servicesPath, `
[providers.central]
base_url = "https://central.example/v1"
models = ["whisper-large"]

[asr]
enabled = true
provider = "central"
model = "whisper-large"
language = "zh"
max_audio_bytes = 1024
`)
	writeFile(t, statePath, `
[mode_models.work]
provider = "central"
model = "whisper-large"

[mode_models.chat]
provider = "central"
model = "whisper-large"
`)
	cfg, err := Load(appPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	asr := cfg.ASR.Normalized()
	if !asr.IsEnabled() || asr.Provider != "central" || asr.Model != "whisper-large" || asr.Language != "zh" || asr.MaxAudioBytes != 1024 {
		t.Fatalf("asr = %#v", asr)
	}
}
