// Package asr transcribes inbound audio with an OpenAI-compatible
// /audio/transcriptions endpoint.
//
// It is deliberately independent from the chat model: an operator points
// [asr] at one existing [providers.*] entry, and the package handles the
// multipart upload, bounded response parsing and a small result cache. The
// caller owns segment selection, permissions and budget accounting.
package asr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"elbot/internal/ops/concurrency"
)

const (
	DefaultTimeout                 = 120 * time.Second
	DefaultMaxAudioBytes           = 20 * 1024 * 1024
	DefaultMaxResponseBytes        = 256 * 1024
	DefaultMaxConcurrentJobs       = 2
	DefaultMaxJobQueue             = 8
	DefaultCacheTTL                = 30 * time.Minute
	DefaultCacheMaxEntries         = 128
	DefaultMaxCacheValueSize       = 8 * 1024
	DefaultNegativeCacheTTL        = 30 * time.Second
	DefaultNegativeCacheMaxEntries = 128
	DefaultRetryInitialDelay       = 1 * time.Second
	DefaultMaxRetries              = 2
	maxRetryAfter                  = 30 * time.Second
)

// Options configures a Service. New applies defaults for unset fields.
type Options struct {
	BaseURL  string
	APIKey   string
	Model    string
	Provider string
	Language string
	Prompt   string

	Timeout           time.Duration
	MaxAudioBytes     int64
	MaxResponseBytes  int
	MaxRetries        int
	RetryInitialDelay time.Duration
	Proxy             string

	MaxConcurrentJobs int
	MaxJobQueue       int
	JobWaitTimeout    time.Duration

	CacheTTL          time.Duration
	CacheMaxEntries   int
	MaxCacheValueSize int
	NegativeTTL       time.Duration
	NegativeMax       int

	BaseContext     context.Context
	CredentialEpoch uint64

	Logger *slog.Logger
	Now    func() time.Time
}

// Request is one audio recording to transcribe.
type Request struct {
	MediaID  string
	Data     []byte
	Name     string
	MIMEType string
	Language string
}

// Result is one successful transcription.
type Result struct {
	Text     string
	Language string
	Duration time.Duration
	Provider string
	Model    string
	Cached   bool
}

// Service transcribes audio through one provider with bounded concurrency and
// a small process-local cache.
type Service struct {
	opts       Options
	client     *http.Client
	cache      *successCache
	negative   *negativeCache
	limiter    *concurrency.Limiter
	baseCancel context.CancelFunc
}

// New builds a Service, applying defaults for unset fields.
func New(opts Options) *Service {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxAudioBytes <= 0 {
		opts.MaxAudioBytes = DefaultMaxAudioBytes
	}
	if opts.MaxResponseBytes <= 0 {
		opts.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	} else if opts.MaxRetries == 0 {
		opts.MaxRetries = DefaultMaxRetries
	}
	if opts.RetryInitialDelay <= 0 {
		opts.RetryInitialDelay = DefaultRetryInitialDelay
	}
	if opts.JobWaitTimeout < 0 {
		opts.JobWaitTimeout = 0
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	maxConcurrent := DefaultMaxConcurrentJobs
	if opts.MaxConcurrentJobs < 0 {
		maxConcurrent = 0
	} else if opts.MaxConcurrentJobs > 0 {
		maxConcurrent = opts.MaxConcurrentJobs
	}
	maxQueue := DefaultMaxJobQueue
	if opts.MaxJobQueue < 0 {
		maxQueue = 0
	} else if opts.MaxJobQueue > 0 {
		maxQueue = opts.MaxJobQueue
	}
	negativeMax := opts.NegativeMax
	if negativeMax <= 0 {
		negativeMax = DefaultNegativeCacheMaxEntries
	}
	negativeTTL := opts.NegativeTTL
	if negativeTTL <= 0 {
		negativeTTL = DefaultNegativeCacheTTL
	}

	baseCancel := func() {}
	if opts.BaseContext != nil {
		_, baseCancel = context.WithCancel(opts.BaseContext)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = opts.Timeout
	client := &http.Client{Transport: transport}
	if strings.TrimSpace(opts.Proxy) != "" {
		if proxyURL, err := url.Parse(opts.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return &Service{
		opts:       opts,
		client:     client,
		cache:      newSuccessCache(opts.CacheMaxEntries, opts.CacheTTL, opts.MaxCacheValueSize, opts.Now),
		negative:   newNegativeCache(negativeMax, negativeTTL, opts.Now),
		limiter:    concurrency.New(concurrency.Config{Max: maxConcurrent, QueueSize: maxQueue, WaitTimeout: opts.JobWaitTimeout}),
		baseCancel: baseCancel,
	}
}

// Close cancels in-flight jobs and releases resources.
func (s *Service) Close() {
	if s == nil || s.baseCancel == nil {
		return
	}
	s.baseCancel()
}

// Transcribe returns the text of one recording.
func (s *Service) Transcribe(ctx context.Context, req Request) (Result, error) {
	if s == nil {
		return Result{}, errors.New("asr: service is nil")
	}
	if strings.TrimSpace(s.opts.BaseURL) == "" {
		return Result{}, errors.New("asr: base URL is not configured")
	}
	if strings.TrimSpace(s.opts.Model) == "" {
		return Result{}, errors.New("asr: model is not configured")
	}
	if strings.TrimSpace(req.MediaID) == "" {
		return Result{}, errors.New("asr: media id is required")
	}
	if len(req.Data) == 0 {
		return Result{}, errors.New("asr: audio is empty")
	}
	if int64(len(req.Data)) > s.opts.MaxAudioBytes {
		return Result{}, fmt.Errorf("asr: audio is %d bytes, over the %d byte limit", len(req.Data), s.opts.MaxAudioBytes)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.opts.Timeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			timeoutCtx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
			defer cancel()
			ctx = timeoutCtx
		}
	}

	key := s.cacheKey(req)
	if cached, ok := s.cache.get(key); ok {
		return cached, nil
	}
	if err := s.negative.get(key); err != nil {
		return Result{}, err
	}

	release, err := s.limiter.Acquire(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("asr: job queue: %w", err)
	}
	defer release()

	result, err := s.transcribeWithRetries(ctx, req)
	if err != nil {
		s.negative.put(key, err)
		return Result{}, err
	}
	result.Provider = s.opts.Provider
	result.Model = s.opts.Model
	s.cache.put(key, result)
	return result, nil
}

func (s *Service) transcribeWithRetries(ctx context.Context, req Request) (Result, error) {
	attempts := s.opts.MaxRetries + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		result, err := s.transcribeOnce(ctx, req)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == attempts-1 || !retryable(err) {
			break
		}
		delay := s.retryDelay(attempt, err)
		if s.opts.Logger != nil {
			s.opts.Logger.Warn("asr request retry", "attempt", attempt+1, "delay", delay.String(), "error", err.Error())
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Result{}, ctx.Err()
		case <-timer.C:
		}
	}
	return Result{}, lastErr
}

func (s *Service) retryDelay(attempt int, err error) time.Duration {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
		if httpErr.RetryAfter > maxRetryAfter {
			return maxRetryAfter
		}
		return httpErr.RetryAfter
	}
	delay := s.opts.RetryInitialDelay << uint(attempt)
	if delay <= 0 {
		delay = s.opts.RetryInitialDelay
	}
	return delay
}

func (s *Service) transcribeOnce(ctx context.Context, req Request) (Result, error) {
	body, contentType, err := s.multipartBody(req)
	if err != nil {
		return Result{}, err
	}
	endpoint := strings.TrimRight(s.opts.BaseURL, "/") + "/audio/transcriptions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return Result{}, fmt.Errorf("asr: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(s.opts.APIKey); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return Result{}, &HTTPError{StatusCode: 0, Message: boundedMessage(err.Error())}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return Result{}, newHTTPError(resp, body)
	}
	return s.parseResponse(resp.Body)
}

func (s *Service) multipartBody(req Request) (*bytes.Buffer, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	filename := safeFilename(req.Name, req.MIMEType)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeQuotes(filename)))
	mimeType := strings.TrimSpace(req.MIMEType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("asr: create multipart file: %w", err)
	}
	if _, err := part.Write(req.Data); err != nil {
		return nil, "", fmt.Errorf("asr: write multipart file: %w", err)
	}
	if err := writer.WriteField("model", s.opts.Model); err != nil {
		return nil, "", err
	}
	if language := s.languageFor(req); language != "" {
		if err := writer.WriteField("language", language); err != nil {
			return nil, "", err
		}
	}
	if prompt := strings.TrimSpace(s.opts.Prompt); prompt != "" {
		if err := writer.WriteField("prompt", prompt); err != nil {
			return nil, "", err
		}
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("asr: close multipart body: %w", err)
	}
	return &body, writer.FormDataContentType(), nil
}

func (s *Service) parseResponse(reader io.Reader) (Result, error) {
	body, err := io.ReadAll(io.LimitReader(reader, int64(s.opts.MaxResponseBytes)+1))
	if err != nil {
		return Result{}, fmt.Errorf("asr: read response: %w", err)
	}
	if len(body) > s.opts.MaxResponseBytes {
		return Result{}, fmt.Errorf("asr: response exceeded %d bytes", s.opts.MaxResponseBytes)
	}
	var payload struct {
		Text          string  `json:"text"`
		Result        string  `json:"result"`
		Transcription string  `json:"transcription"`
		Language      string  `json:"language"`
		Duration      float64 `json:"duration"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Result{}, fmt.Errorf("asr: decode response: %w", err)
	}
	text := firstNonEmpty(payload.Text, payload.Result, payload.Transcription)
	text = CleanText(text)
	if text == "" {
		return Result{}, errors.New("asr: provider returned an empty transcript")
	}
	result := Result{Text: text, Language: strings.TrimSpace(payload.Language)}
	if payload.Duration > 0 {
		result.Duration = time.Duration(payload.Duration * float64(time.Second))
	}
	if result.Language == "" {
		result.Language = s.opts.Language
	}
	return result, nil
}

func (s *Service) languageFor(req Request) string {
	if language := strings.TrimSpace(req.Language); language != "" {
		return language
	}
	return strings.TrimSpace(s.opts.Language)
}

func (s *Service) cacheKey(req Request) string {
	h := sha256.New()
	_, _ = io.WriteString(h, "asr\x00")
	_, _ = io.WriteString(h, strings.TrimSpace(req.MediaID))
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, strings.TrimSpace(s.opts.BaseURL))
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, strings.TrimSpace(s.opts.Model))
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, s.languageFor(req))
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, strings.TrimSpace(s.opts.Prompt))
	_, _ = io.WriteString(h, fmt.Sprintf("\x00%d", s.opts.CredentialEpoch))
	return hex.EncodeToString(h.Sum(nil))
}

// CleanText normalizes a transcript into one line and removes control
// characters. It never returns a value containing NUL.
func CleanText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(value))
	lastSpace := false
	for _, r := range value {
		switch {
		case r == '\x00' || r < 0x20 || r == 0x7f:
			if !lastSpace && builder.Len() > 0 {
				builder.WriteByte(' ')
				lastSpace = true
			}
		default:
			builder.WriteRune(r)
			lastSpace = r == ' ' || r == '\u3000'
		}
	}
	return strings.TrimSpace(builder.String())
}

func safeFilename(name, mimeType string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	if name == "." || name == "/" || name == "" {
		name = "audio"
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\'' || r == ';' || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if len(name) > 120 {
		name = name[:120]
	}
	if filepath.Ext(name) == "" {
		name += extensionForMIME(mimeType)
	}
	return name
}

func extensionForMIME(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "audio/amr":
		return ".amr"
	case "audio/webm":
		return ".webm"
	default:
		return ".bin"
	}
}

func escapeQuotes(value string) string {
	return strings.ReplaceAll(value, `"`, `\"`)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boundedMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
