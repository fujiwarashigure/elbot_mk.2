package media

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"path/filepath"
	"strings"

	"elbot/internal/config"
)

func imageDimensions(data []byte) (int, int, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, fmt.Errorf("decode image dimensions: %w", err)
	}
	return config.Width, config.Height, nil
}

func compressImage(data []byte, maxBytes int64, maxLength int) ([]byte, error) {
	return compressImageContext(context.Background(), data, maxBytes, maxLength)
}

// compressImageContext is compressImage with cooperative cancellation: the
// quality/shrink loops check ctx between rounds so a deadline can stop a very
// slow optimisation instead of only being noticed after it returns.
func compressImageContext(ctx context.Context, data []byte, maxBytes int64, maxLength int) ([]byte, error) {
	if maxBytes <= 1 || maxLength <= 1 {
		return nil, fmt.Errorf("image compression limits are too small")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	maxDimension := maxLength - 1
	if width > maxDimension || height > maxDimension {
		scale := float64(maxDimension) / float64(max(width, height))
		width = max(1, int(float64(width)*scale))
		height = max(1, int(float64(height)*scale))
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate := resizeImage(source, width, height)
		for quality := 92; quality >= 35; quality -= 5 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			encoded, err := encodeJPEG(candidate, quality)
			if err != nil {
				return nil, err
			}
			if int64(len(encoded)) < maxBytes {
				return encoded, nil
			}
		}
		if width == 1 && height == 1 {
			break
		}
		width = max(1, int(float64(width)*0.85))
		height = max(1, int(float64(height)*0.85))
		if width > maxDimension || height > maxDimension {
			scale := float64(maxDimension) / float64(max(width, height))
			width = max(1, int(float64(width)*scale))
			height = max(1, int(float64(height)*scale))
		}
	}
	return nil, fmt.Errorf("compressed image still exceeds %d bytes", maxBytes)
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var output bytes.Buffer
	if err := jpeg.Encode(&output, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode JPEG: %w", err)
	}
	return output.Bytes(), nil
}

func resizeImage(source image.Image, width, height int) image.Image {
	bounds := source.Bounds()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	for y := 0; y < height; y++ {
		fy := (float64(y)+0.5)*float64(bounds.Dy())/float64(height) - 0.5
		for x := 0; x < width; x++ {
			fx := (float64(x)+0.5)*float64(bounds.Dx())/float64(width) - 0.5
			pixel := bilinearNRGBA(source, bounds, fx, fy)
			alpha := uint32(pixel.A)
			canvas.Set(x, y, color.NRGBA{
				R: uint8((uint32(pixel.R)*alpha + 255*(255-alpha)) / 255),
				G: uint8((uint32(pixel.G)*alpha + 255*(255-alpha)) / 255),
				B: uint8((uint32(pixel.B)*alpha + 255*(255-alpha)) / 255),
				A: 255,
			})
		}
	}
	return canvas
}

func bilinearNRGBA(source image.Image, bounds image.Rectangle, fx, fy float64) color.NRGBA {
	x0 := clamp(int(fx), 0, bounds.Dx()-1)
	y0 := clamp(int(fy), 0, bounds.Dy()-1)
	x1 := min(x0+1, bounds.Dx()-1)
	y1 := min(y0+1, bounds.Dy()-1)
	dx, dy := fx-float64(int(fx)), fy-float64(int(fy))
	if fx < 0 {
		dx = 0
	}
	if fy < 0 {
		dy = 0
	}
	pixels := [4]color.NRGBA{
		color.NRGBAModel.Convert(source.At(bounds.Min.X+x0, bounds.Min.Y+y0)).(color.NRGBA),
		color.NRGBAModel.Convert(source.At(bounds.Min.X+x1, bounds.Min.Y+y0)).(color.NRGBA),
		color.NRGBAModel.Convert(source.At(bounds.Min.X+x0, bounds.Min.Y+y1)).(color.NRGBA),
		color.NRGBAModel.Convert(source.At(bounds.Min.X+x1, bounds.Min.Y+y1)).(color.NRGBA),
	}
	blend := func(values [4]uint8) uint8 {
		top := float64(values[0])*(1-dx) + float64(values[1])*dx
		bottom := float64(values[2])*(1-dx) + float64(values[3])*dx
		return uint8(top*(1-dy) + bottom*dy + 0.5)
	}
	return color.NRGBA{
		R: blend([4]uint8{pixels[0].R, pixels[1].R, pixels[2].R, pixels[3].R}),
		G: blend([4]uint8{pixels[0].G, pixels[1].G, pixels[2].G, pixels[3].G}),
		B: blend([4]uint8{pixels[0].B, pixels[1].B, pixels[2].B, pixels[3].B}),
		A: blend([4]uint8{pixels[0].A, pixels[1].A, pixels[2].A, pixels[3].A}),
	}
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func shouldCompressImage(data []byte, cfg config.MediaConfig) bool {
	if int64(len(data)) > cfg.LLMImageCompressionThresholdBytes {
		return true
	}
	width, height, err := imageDimensions(data)
	return err == nil && (width >= cfg.LLMImageMaxLength || height >= cfg.LLMImageMaxLength)
}

func compressedName(name string) string {
	name = sanitizeMediaName(name)
	ext := filepath.Ext(name)
	if ext == "" {
		return name + ".jpg"
	}
	return strings.TrimSuffix(name, ext) + ".jpg"
}

const (
	// visionImageHardLimit caps how much image data PrepareVisionImage will look at.
	visionImageHardLimit = 64 * 1024 * 1024
	// visionImageDefaultByteBudget bounds the re-encoded payload when the caller
	// does not set max_image_bytes. It is deliberately generous because the edge
	// limit, not the byte limit, is what normally drives this downscale.
	visionImageDefaultByteBudget = 12 * 1024 * 1024
	// visionImageMaxInputEdge / visionImageMaxInputPixels bound what we are
	// willing to decode. max_edge and max_image_bytes only constrain the result,
	// but a small file can still declare an enormous canvas, so the original
	// picture is checked before any full decode.
	visionImageMaxInputEdge   = 20000
	visionImageMaxInputPixels = 24000000
	// visionImageMaxConcurrentDecodes bounds how many tool calls may hold a
	// decoded bitmap at once, so a burst of large references cannot pin the
	// whole process.
	visionImageMaxConcurrentDecodes = 2
)

var visionImageDecodeSlots = make(chan struct{}, visionImageMaxConcurrentDecodes)

// VisionImageInputLimit is the largest stored object image_to_prompt will
// materialise. It is exported so callers that read the image themselves can use
// Manager.ReadLimited and reject an oversized object before loading it.
const VisionImageInputLimit = visionImageHardLimit

// PrepareVisionImage is PrepareVisionImageContext with context.Background().
func PrepareVisionImage(data []byte, mimeType string, maxEdge int, maxBytes int64) ([]byte, string, error) {
	return PrepareVisionImageContext(context.Background(), data, mimeType, maxEdge, maxBytes)
}

// PrepareVisionImageContext returns image bytes ready to upload to a vision model.
//
// On success, for a format the local decoders understand, the returned payload
// satisfies both limits and mimeType matches the actual encoding:
//
//   - long edge <= maxEdge (when maxEdge > 0)
//   - byte length <= maxBytes (when maxBytes > 0, else the default budget)
//
// A payload the local decoders do not recognise cannot be measured or shrunk,
// so it is passed through only while it already fits the byte budget. For those
// formats only the byte budget is guaranteed; the canvas size is left to the
// upstream service. An oversized opaque blob is rejected locally instead of
// being uploaded.
//
// When the input already fits it is returned untouched, keeping its original
// format. Otherwise it is downscaled and re-encoded as JPEG; transparent pixels
// are composited on white and animated GIFs are reduced to their first frame.
// The work is cancellable through ctx and serialised so only a bounded number
// of bitmaps are decoded at once.
func PrepareVisionImageContext(ctx context.Context, data []byte, mimeType string, maxEdge int, maxBytes int64) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", fmt.Errorf("image is empty")
	}
	if int64(len(data)) > visionImageHardLimit {
		return nil, "", fmt.Errorf("image is %d bytes, exceeds the %d byte decode limit", len(data), visionImageHardLimit)
	}
	mimeType = visionImageMIMEType(data, mimeType)
	width, height, err := imageDimensions(data)
	if err != nil {
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return nil, "", fmt.Errorf("image format is not supported for local processing and the payload is %d bytes, over the %d byte limit", len(data), maxBytes)
		}
		return data, mimeType, nil
	}
	if width <= 0 || height <= 0 {
		return nil, "", fmt.Errorf("image declares invalid dimensions %dx%d", width, height)
	}
	if width > visionImageMaxInputEdge || height > visionImageMaxInputEdge {
		return nil, "", fmt.Errorf("image edge %dx%d exceeds the %d px decode limit", width, height, visionImageMaxInputEdge)
	}
	// int64 math also keeps the product from overflowing on 32-bit builds.
	if pixels := int64(width) * int64(height); pixels > visionImageMaxInputPixels {
		return nil, "", fmt.Errorf("image is %dx%d (%d pixels), over the %d pixel decode limit", width, height, pixels, visionImageMaxInputPixels)
	}
	longEdge := max(width, height)
	overEdge := maxEdge > 0 && longEdge > maxEdge
	overBytes := maxBytes > 0 && int64(len(data)) > maxBytes
	if !overEdge && !overBytes {
		return data, mimeType, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	limit := maxEdge
	if limit <= 0 {
		limit = longEdge
	}
	bytesLimit := maxBytes
	if bytesLimit <= 0 {
		bytesLimit = visionImageDefaultByteBudget
	}
	encoded, err := compressVisionImage(ctx, data, bytesLimit, limit)
	if err != nil {
		return nil, "", err
	}
	if err := verifyVisionImage(encoded, maxEdge, bytesLimit); err != nil {
		return nil, "", err
	}
	return encoded, "image/jpeg", nil
}

// visionImageMIMEType prefers what the bytes actually are over a declared type
// the content contradicts, so an extension or stale metadata cannot pair JPEG
// bytes with image/png in the upload. Non-image detections fall back to the
// declared type.
func visionImageMIMEType(data []byte, declared string) string {
	declared = strings.TrimSpace(declared)
	if index := strings.IndexByte(declared, ';'); index >= 0 {
		declared = strings.TrimSpace(declared[:index])
	}
	detected := http.DetectContentType(data)
	if index := strings.IndexByte(detected, ';'); index >= 0 {
		detected = strings.TrimSpace(detected[:index])
	}
	if strings.HasPrefix(detected, "image/") && !strings.EqualFold(detected, declared) {
		return detected
	}
	if declared != "" {
		return declared
	}
	return detected
}

// compressVisionImage serialises the CPU/memory-heavy decode behind a small
// concurrency gate that the caller can abandon through ctx. compressImage
// itself already enforces the byte budget by lowering JPEG quality and then
// shrinking the canvas until the output fits.
func compressVisionImage(ctx context.Context, data []byte, maxBytes int64, maxLength int) ([]byte, error) {
	select {
	case visionImageDecodeSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-visionImageDecodeSlots }()
	return compressImageContext(ctx, data, maxBytes, maxLength)
}

// verifyVisionImage re-checks the documented output contract. compressImage
// produces it today; this guard makes a future regression fail loudly instead
// of silently shipping an over-budget image.
func verifyVisionImage(data []byte, maxEdge int, maxBytes int64) error {
	width, height, err := imageDimensions(data)
	if err != nil {
		return fmt.Errorf("verify processed image: %w", err)
	}
	if maxEdge > 0 {
		if long := max(width, height); long > maxEdge {
			return fmt.Errorf("processed image long edge %d exceeds max_edge %d", long, maxEdge)
		}
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return fmt.Errorf("processed image is %d bytes, over max_image_bytes %d", len(data), maxBytes)
	}
	return nil
}
