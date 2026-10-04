package vision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// CacheSchemaVersion is bumped whenever the meaning of a cache entry changes.
// It is part of every key, so an upgrade never serves a stale-format result.
const CacheSchemaVersion = 1

// CacheIdentity is the explicit, versioned identity of one cacheable result.
//
// It is serialized to JSON and hashed instead of concatenating an ad-hoc string
// so that adding or reordering a field is a compile-time change and a partial or
// ambiguous key cannot be produced accidentally.
//
// Secrets (API keys) must never appear here. Use CredentialEpoch to invalidate
// on a credential/account change without storing the credential itself.
type CacheIdentity struct {
	SchemaVersion     int    `json:"schema_version"`
	MediaID           string `json:"media_id"`
	Provider          string `json:"provider"`
	Endpoint          string `json:"endpoint,omitempty"`
	Model             string `json:"model"`
	PromptVersion     string `json:"prompt_version"`
	PromptHash        string `json:"prompt_hash"`
	PreprocessVersion string `json:"preprocess_version"`
	RequestHash       string `json:"request_hash"`
	CredentialEpoch   uint64 `json:"credential_epoch,omitempty"`
}

// Key returns the hex-encoded SHA-256 cache key.
func (id CacheIdentity) Key() (string, error) {
	payload, err := json.Marshal(id)
	if err != nil {
		return "", fmt.Errorf("vision: encode cache identity: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// hashText returns a stable hash of one text block, used for prompt content.
func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// requestFingerprint captures the request-level parameters that change the
// result. Anything that only affects logging or routing must stay out of it so
// a log-level change does not invalidate the cache.
type requestFingerprint struct {
	MaxTokens      int     `json:"max_tokens"`
	Temperature    float64 `json:"temperature"`
	MaxEdge        int     `json:"max_edge"`
	MaxImageBytes  int64   `json:"max_image_bytes"`
	OutputMIMEType string  `json:"output_mime_type"`
}

func (r requestFingerprint) hash() (string, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("vision: encode request fingerprint: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
