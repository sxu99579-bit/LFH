package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writeSolidPNG(t *testing.T, path string, w, h int, c color.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
}

func TestGenerateImageThumbnailLandscapeAndNoUpscale(t *testing.T) {
	dir := t.TempDir()
	bigPath := filepath.Join(dir, "landscape.jpg")
	img := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 32, G: 120, B: 220, A: 255})
		}
	}
	f, err := os.Create(bigPath)
	if err != nil {
		t.Fatalf("create jpg: %v", err)
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		_ = f.Close()
		t.Fatalf("encode jpg: %v", err)
	}
	_ = f.Close()

	data, ct, outExt, err := GenerateImageThumbnail(bigPath, 200)
	if err != nil {
		t.Fatalf("GenerateImageThumbnail landscape: %v", err)
	}
	if ct != "image/jpeg" || outExt != "jpg" {
		t.Fatalf("unexpected format ct=%s ext=%s", ct, outExt)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode resized thumb: %v", err)
	}
	if cfg.Width != 200 || cfg.Height != 100 {
		t.Fatalf("expected 200x100 preserving 2:1 aspect, got %dx%d", cfg.Width, cfg.Height)
	}

	smallPath := filepath.Join(dir, "small.png")
	writeSolidPNG(t, smallPath, 90, 60, color.NRGBA{R: 255, G: 100, B: 80, A: 255})
	smallData, smallCT, _, err := GenerateImageThumbnail(smallPath, 360)
	if err != nil {
		t.Fatalf("GenerateImageThumbnail small: %v", err)
	}
	if smallCT != "image/jpeg" {
		t.Fatalf("expected opaque png to output jpeg, got %s", smallCT)
	}
	smallCfg, _, err := image.DecodeConfig(bytes.NewReader(smallData))
	if err != nil {
		t.Fatalf("decode small thumb: %v", err)
	}
	if smallCfg.Width != 90 || smallCfg.Height != 60 {
		t.Fatalf("small image must not upscale, got %dx%d", smallCfg.Width, smallCfg.Height)
	}
}

func TestGenerateImageThumbnailTransparentPNG(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "alpha.png")
	img := image.NewNRGBA(image.Rect(0, 0, 500, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 500; x++ {
			alpha := uint8(255)
			if x < 250 {
				alpha = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{R: 20, G: 180, B: 140, A: alpha})
		}
	}
	f, err := os.Create(srcPath)
	if err != nil {
		t.Fatalf("create alpha png: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode alpha png: %v", err)
	}
	_ = f.Close()

	data, ct, outExt, err := GenerateImageThumbnail(srcPath, 160)
	if err != nil {
		t.Fatalf("GenerateImageThumbnail alpha: %v", err)
	}
	if ct != "image/png" || outExt != "png" {
		t.Fatalf("expected transparent png preserved as png, got ct=%s ext=%s", ct, outExt)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode alpha thumb: %v", err)
	}
	if decoded.Bounds().Dx() != 160 || decoded.Bounds().Dy() != 160 {
		t.Fatalf("unexpected alpha dims: %v", decoded.Bounds())
	}
	_, _, _, a := decoded.At(10, 10).RGBA()
	if a != 0 {
		t.Fatalf("expected transparent corner pixel, got alpha=%d", a)
	}
}

func TestThumbCacheDiskHitAndCorruptInput(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.png")
	writeSolidPNG(t, srcPath, 400, 300, color.NRGBA{R: 12, G: 34, B: 56, A: 255})

	cache := NewThumbCache(filepath.Join(dir, "cache", "thumbs"))
	fp1, ct1, err := cache.GetOrBuild("file1_v1_360", srcPath, 360)
	if err != nil {
		t.Fatalf("first GetOrBuild: %v", err)
	}
	if ct1 != "image/jpeg" {
		t.Fatalf("unexpected ct1=%s", ct1)
	}

	// Delete source file to prove second call hits disk cache directly.
	_ = os.Remove(srcPath)
	fp2, ct2, err := cache.GetOrBuild("file1_v1_360", srcPath, 360)
	if err != nil {
		t.Fatalf("second GetOrBuild cache hit: %v", err)
	}
	if fp1 != fp2 || ct2 != ct1 {
		t.Fatalf("cache hit mismatch: %s vs %s", fp1, fp2)
	}

	corruptPath := filepath.Join(dir, "corrupt.jpg")
	_ = os.WriteFile(corruptPath, []byte("not-an-image"), 0o644)
	if _, _, err := cache.GetOrBuild("bad_v1_160", corruptPath, 160); err == nil {
		t.Fatalf("expected error on corrupt image")
	}
}
