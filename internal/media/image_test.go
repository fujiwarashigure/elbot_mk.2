package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestCompressImageProducesBoundedJPEGWithWhiteBackground(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			if x < 40 {
				source.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
			}
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}

	output, err := compressImage(input.Bytes(), 16*1024, 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) >= 16*1024 {
		t.Fatalf("compressed size = %d", len(output))
	}
	decoded, err := jpeg.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode JPEG: %v", err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() >= 32 || bounds.Dy() >= 32 || bounds.Dx() != 31 || bounds.Dy() != 15 {
		t.Fatalf("compressed bounds = %v", bounds)
	}
	white := color.NRGBAModel.Convert(decoded.At(bounds.Max.X-2, bounds.Max.Y/2)).(color.NRGBA)
	if white.R < 240 || white.G < 240 || white.B < 240 {
		t.Fatalf("transparent background was not white: %#v", white)
	}
}

func encodeVisionTestPNG(t *testing.T, width, height int, noisy bool) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if noisy {
				source.SetNRGBA(x, y, color.NRGBA{
					R: uint8((x*7 + y*13) % 256),
					G: uint8((x*29 + y*3) % 256),
					B: uint8((x*11 + y*31) % 256),
					A: 255,
				})
				continue
			}
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, source); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return output.Bytes()
}

func TestPrepareVisionImageKeepsSmallImageUntouched(t *testing.T) {
	data := encodeVisionTestPNG(t, 8, 8, false)
	output, mimeType, err := PrepareVisionImage(data, "image/png", 64, 1024*1024)
	if err != nil {
		t.Fatalf("PrepareVisionImage: %v", err)
	}
	if !bytes.Equal(output, data) || mimeType != "image/png" {
		t.Fatalf("small image changed: %d bytes -> %d bytes, mime %q", len(data), len(output), mimeType)
	}
}

func TestPrepareVisionImageDownscalesLongEdge(t *testing.T) {
	data := encodeVisionTestPNG(t, 200, 100, false)
	output, mimeType, err := PrepareVisionImage(data, "image/png", 64, 0)
	if err != nil {
		t.Fatalf("PrepareVisionImage: %v", err)
	}
	if mimeType != "image/jpeg" {
		t.Fatalf("mimeType = %q, want image/jpeg", mimeType)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
	bounds := decoded.Bounds()
	if long := max(bounds.Dx(), bounds.Dy()); long > 64 {
		t.Fatalf("long edge = %d, want <= 64", long)
	}
}

func TestPrepareVisionImageEnforcesByteBudget(t *testing.T) {
	data := encodeVisionTestPNG(t, 256, 256, true)
	const budget = 4 * 1024
	if int64(len(data)) <= budget {
		t.Fatalf("test image is unexpectedly small: %d", len(data))
	}
	output, mimeType, err := PrepareVisionImage(data, "image/png", 0, budget)
	if err != nil {
		t.Fatalf("PrepareVisionImage: %v", err)
	}
	if mimeType != "image/jpeg" || int64(len(output)) >= budget {
		t.Fatalf("bounded image = %d bytes, mime %q", len(output), mimeType)
	}
}

func TestPrepareVisionImagePassesUndecodableThrough(t *testing.T) {
	data := []byte("this is not an image")
	output, mimeType, err := PrepareVisionImage(data, "application/octet-stream", 64, 4096)
	if err != nil {
		t.Fatalf("PrepareVisionImage: %v", err)
	}
	if !bytes.Equal(output, data) || mimeType != "application/octet-stream" {
		t.Fatalf("undecodable payload was changed: %q %q", string(output), mimeType)
	}
}

func TestPrepareVisionImageRejectsUndecodableOverBudget(t *testing.T) {
	data := []byte("this is not an image")
	if _, _, err := PrepareVisionImage(data, "application/octet-stream", 64, 8); err == nil {
		t.Fatal("oversized undecodable payload was uploaded")
	}
}

// craftedPNGHeader builds a tiny valid PNG and rewrites the IHDR dimensions.
// The result is not a decodable picture, but DecodeConfig reads the declared
// size, which is exactly the cheap pre-flight check under test.
func craftedPNGHeader(t *testing.T, width, height int) []byte {
	t.Helper()
	data := encodeVisionTestPNG(t, 8, 8, false)
	if len(data) < 33 || string(data[12:16]) != "IHDR" {
		t.Fatalf("unexpected png layout: %d bytes", len(data))
	}
	binary.BigEndian.PutUint32(data[16:20], uint32(width))
	binary.BigEndian.PutUint32(data[20:24], uint32(height))
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}

func TestPrepareVisionImageRejectsUnsafeDecodeSizes(t *testing.T) {
	for name, dims := range map[string][2]int{
		"oversized edge":   {visionImageMaxInputEdge + 1, 8},
		"oversized pixels": {12000, 4000},
	} {
		data := craftedPNGHeader(t, dims[0], dims[1])
		if _, _, err := PrepareVisionImage(data, "image/png", 64, 0); err == nil {
			t.Fatalf("%s (%dx%d): unsafe size was accepted", name, dims[0], dims[1])
		}
	}
}

func TestPrepareVisionImageHonoursBothLimits(t *testing.T) {
	data := encodeVisionTestPNG(t, 400, 200, true)
	const budget = 5120
	output, mimeType, err := PrepareVisionImage(data, "image/png", 64, budget)
	if err != nil {
		t.Fatalf("PrepareVisionImage: %v", err)
	}
	if mimeType != "image/jpeg" {
		t.Fatalf("mimeType = %q, want image/jpeg", mimeType)
	}
	if int64(len(output)) > budget {
		t.Fatalf("output = %d bytes, want <= %d", len(output), budget)
	}
	header, _, err := image.DecodeConfig(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode output config: %v", err)
	}
	if long := max(header.Width, header.Height); long > 64 {
		t.Fatalf("output long edge = %d, want <= 64", long)
	}
}

func TestPrepareVisionImageRejectsEmpty(t *testing.T) {
	if _, _, err := PrepareVisionImage(nil, "image/png", 64, 1024); err == nil {
		t.Fatal("empty image was accepted")
	}
}

func TestPrepareVisionImageCorrectsMismatchedMIMEType(t *testing.T) {
	data := encodeVisionTestPNG(t, 8, 8, false)
	output, mimeType, err := PrepareVisionImage(data, "image/jpeg", 64, 1024*1024)
	if err != nil {
		t.Fatalf("PrepareVisionImage: %v", err)
	}
	if !bytes.Equal(output, data) {
		t.Fatal("fits-in-budget image should pass through untouched")
	}
	if mimeType != "image/png" {
		t.Fatalf("mimeType = %q, want image/png from content sniffing", mimeType)
	}
}

func TestPrepareVisionImageContextCancellation(t *testing.T) {
	data := encodeVisionTestPNG(t, 400, 200, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := PrepareVisionImageContext(ctx, data, "image/png", 64, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
