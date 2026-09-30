// Package imagegen talks to an OpenAI-compatible image generation endpoint
// (for example a relay that exposes GPT Image 2.5 at /v1/images/generations).
package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultModel         = "gpt-image-2.5"
	defaultSize          = "1024x1024"
	defaultQuality       = "high"
	defaultOutputFormat  = "png"
	defaultTimeoutSecond = 180
	defaultMaxPrompt     = 4000
	maxResponseBytes     = 20 << 20
	maxImageBytes        = 20 << 20
)

// Config is the normalized image generation configuration.
type Config struct {
	Enabled                 bool
	BaseURL                 string
	Endpoint                string
	APIKey                  string
	APIKeyEnv               string
	Model                   string
	Size                    string
	Quality                 string
	OutputFormat            string
	ResponseFormat          string
	TimeoutSeconds          int
	PresetPrompt            string
	NegativePrompt          string
	MaxPromptRunes          int
	Optimize                string
	OptimizeTermMode        string
	OptimizeMaxAnchors      int
	OptimizeMaxNegatives    int
	OptimizeMaxAddedRunes   int
	OptimizeMaxTags         int
	OptimizeRewrite         string
	OptimizeRewriteModel    string
	OptimizeRewriteMinRunes int
	AutoCharacter           bool
	AutoContext             bool
	ContextDefaultLimit     int
	SuperadminOnly          bool
	SaveToCharacter         bool
	SendByDefault           bool
	SupportsReference       bool
	ReferenceField          string
	ExtraPayload            map[string]any
	ExtraHeaders            map[string]string
	Proxy                   string
}

// Normalize fills defaults.
func (c Config) Normalize() Config {
	if strings.TrimSpace(c.Model) == "" {
		c.Model = defaultModel
	}
	if strings.TrimSpace(c.Size) == "" {
		c.Size = defaultSize
	}
	if strings.TrimSpace(c.Quality) == "" {
		c.Quality = defaultQuality
	}
	if strings.TrimSpace(c.OutputFormat) == "" {
		c.OutputFormat = defaultOutputFormat
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = defaultTimeoutSecond
	}
	if c.MaxPromptRunes <= 0 {
		c.MaxPromptRunes = defaultMaxPrompt
	}
	if strings.TrimSpace(c.ReferenceField) == "" {
		c.ReferenceField = "image"
	}
	if strings.TrimSpace(c.APIKeyEnv) == "" {
		c.APIKeyEnv = "IMAGE_API_KEY"
	}
	if strings.TrimSpace(c.Optimize) == "" {
		c.Optimize = "rules"
	}
	if c.OptimizeMaxAnchors <= 0 {
		c.OptimizeMaxAnchors = 4
	}
	if c.OptimizeMaxNegatives <= 0 {
		c.OptimizeMaxNegatives = 10
	}
	if c.OptimizeMaxAddedRunes <= 0 {
		c.OptimizeMaxAddedRunes = 400
	}
	if strings.TrimSpace(c.OptimizeTermMode) == "" {
		c.OptimizeTermMode = "phrase"
	}
	if c.OptimizeMaxTags <= 0 {
		c.OptimizeMaxTags = 12
	}
	if strings.TrimSpace(c.OptimizeRewrite) == "" {
		c.OptimizeRewrite = "auto"
	}
	if strings.TrimSpace(c.OptimizeRewriteModel) == "" {
		c.OptimizeRewriteModel = "naming"
	}
	if c.OptimizeRewriteMinRunes <= 0 {
		c.OptimizeRewriteMinRunes = 40
	}
	if c.ContextDefaultLimit <= 0 {
		c.ContextDefaultLimit = 6
	}
	return c
}

// Request is one image generation request.
type Request struct {
	Prompt        string
	Size          string
	Quality       string
	N             int
	ReferenceData []byte
	ReferenceMIME string
}

// Result is one generated image.
type Result struct {
	Data          []byte
	MIMEType      string
	RevisedPrompt string
	Model         string
	Size          string
}

// Client is a concurrency-safe image generation client.
type Client struct {
	cfg    Config
	http   *http.Client
	lookup func(string) (string, bool)
}

// New builds a client. lookup resolves environment variables for api_key_env.
func New(cfg Config, lookup func(string) (string, bool)) *Client {
	cfg = cfg.Normalize()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy := strings.TrimSpace(cfg.Proxy); proxy != "" {
		if parsed, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	client := &http.Client{
		Timeout:   time.Duration(cfg.TimeoutSeconds) * time.Second,
		Transport: transport,
	}
	return &Client{cfg: cfg, http: client, lookup: lookup}
}

// Config returns the normalized configuration.
func (c *Client) Config() Config {
	if c == nil {
		return Config{}
	}
	return c.cfg
}

// Enabled reports whether the image service is usable.
func (c *Client) Enabled() bool {
	return c != nil && c.cfg.Enabled
}

func (c *Client) endpoint() (string, error) {
	if raw := strings.TrimSpace(c.cfg.Endpoint); raw != "" {
		return raw, nil
	}
	base := strings.TrimRight(strings.TrimSpace(c.cfg.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("image_generation.base_url 未配置")
	}
	if strings.HasSuffix(base, "/images/generations") {
		return base, nil
	}
	return base + "/images/generations", nil
}

func (c *Client) apiKey() string {
	if key := strings.TrimSpace(c.cfg.APIKey); key != "" {
		return key
	}
	if c.lookup != nil {
		if value, ok := c.lookup(c.cfg.APIKeyEnv); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Generate calls the endpoint and returns the first image.
func (c *Client) Generate(ctx context.Context, req Request) (*Result, error) {
	if c == nil || !c.cfg.Enabled {
		return nil, fmt.Errorf("生图服务未启用")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt 不能为空")
	}
	endpoint, err := c.endpoint()
	if err != nil {
		return nil, err
	}
	apiKey := c.apiKey()
	if apiKey == "" {
		return nil, fmt.Errorf("生图服务缺少 API Key：请设置环境变量 %s 或 image_generation.api_key", c.cfg.APIKeyEnv)
	}

	payload := map[string]any{
		"model":  c.cfg.Model,
		"prompt": prompt,
	}
	if n := req.N; n > 0 {
		payload["n"] = n
	} else {
		payload["n"] = 1
	}
	if size := firstNonEmpty(req.Size, c.cfg.Size); size != "" {
		payload["size"] = size
	}
	if quality := firstNonEmpty(req.Quality, c.cfg.Quality); quality != "" {
		payload["quality"] = quality
	}
	if format := strings.TrimSpace(c.cfg.OutputFormat); format != "" {
		payload["output_format"] = format
	}
	if format := strings.TrimSpace(c.cfg.ResponseFormat); format != "" {
		payload["response_format"] = format
	}
	if c.cfg.SupportsReference && len(req.ReferenceData) > 0 {
		mime := firstNonEmpty(req.ReferenceMIME, "image/png")
		payload[c.cfg.ReferenceField] = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(req.ReferenceData)
	}
	for key, value := range c.cfg.ExtraPayload {
		payload[key] = value
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode image request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create image request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	for key, value := range c.cfg.ExtraHeaders {
		httpReq.Header.Set(key, value)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("生图请求失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := readErrorBody(resp.Body)
		return nil, fmt.Errorf("生图服务返回 %d：%s", resp.StatusCode, message)
	}

	var parsed imagesResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("解析生图响应失败：%w", err)
	}
	if len(parsed.Data) == 0 {
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return nil, fmt.Errorf("生图服务错误：%s", strings.TrimSpace(parsed.Error.Message))
		}
		return nil, fmt.Errorf("生图服务没有返回图片")
	}
	item := parsed.Data[0]
	data, mimeType, err := c.decodeImage(ctx, item)
	if err != nil {
		return nil, err
	}
	return &Result{
		Data:          data,
		MIMEType:      mimeType,
		RevisedPrompt: strings.TrimSpace(item.RevisedPrompt),
		Model:         c.cfg.Model,
		Size:          firstName(payload["size"]),
	}, nil
}

type imagesResponse struct {
	Data []struct {
		B64JSON       string `json:"b64_json"`
		URL           string `json:"url"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) decodeImage(ctx context.Context, item struct {
	B64JSON       string `json:"b64_json"`
	URL           string `json:"url"`
	RevisedPrompt string `json:"revised_prompt"`
}) ([]byte, string, error) {
	if raw := strings.TrimSpace(item.B64JSON); raw != "" {
		mimeType := ""
		if strings.HasPrefix(raw, "data:") {
			if header, encoded, ok := strings.Cut(raw, ","); ok {
				raw = encoded
				mimeType = strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
			}
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, "", fmt.Errorf("解码生图结果失败：%w", err)
		}
		if len(data) > maxImageBytes {
			return nil, "", fmt.Errorf("生图结果超过 %d bytes 上限", maxImageBytes)
		}
		return data, firstNonEmpty(mimeType, c.mimeTypeForOutput()), nil
	}
	if rawURL := strings.TrimSpace(item.URL); rawURL != "" {
		return c.download(ctx, rawURL)
	}
	return nil, "", fmt.Errorf("生图服务返回了空图片")
}

func (c *Client) download(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create image download request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("下载生图结果失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("下载生图结果失败：%s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("读取生图结果失败：%w", err)
	}
	if len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("生图结果超过 %d bytes 上限", maxImageBytes)
	}
	mimeType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if strings.Contains(mimeType, ";") {
		mimeType = strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])
	}
	return data, firstNonEmpty(mimeType, c.mimeTypeForOutput()), nil
}

func (c *Client) mimeTypeForOutput() string {
	switch strings.ToLower(strings.TrimSpace(c.cfg.OutputFormat)) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func readErrorBody(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return "无法读取错误响应"
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "空响应"
	}
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &parsed) == nil {
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return strings.TrimSpace(parsed.Error.Message)
		}
		if strings.TrimSpace(parsed.Message) != "" {
			return strings.TrimSpace(parsed.Message)
		}
	}
	if len(text) > 500 {
		text = text[:500] + "..."
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstName(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}
