package preview

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	MinThumbWidth             = 64
	MaxThumbWidth             = 640
	DefaultThumbWidth         = 360
	MaxThumbSourceBytes int64 = 64 << 20
	MaxThumbPixels      int64 = 64_000_000
)

func IsThumbImageExt(ext string) bool {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), ".")) {
	case "jpg", "jpeg", "png", "gif":
		return true
	default:
		return false
	}
}

func ClampThumbWidth(w int) int {
	if w <= 0 {
		return DefaultThumbWidth
	}
	if w < MinThumbWidth {
		return MinThumbWidth
	}
	if w > MaxThumbWidth {
		return MaxThumbWidth
	}
	return w
}

func fitBoxDimensions(srcW, srcH, maxDim int) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return 1, 1
	}
	if maxDim <= 0 || (srcW <= maxDim && srcH <= maxDim) {
		return srcW, srcH
	}
	if srcW >= srcH {
		dstW := maxDim
		dstH := int(int64(srcH)*int64(maxDim)/int64(srcW) + 0)
		if dstH < 1 {
			dstH = 1
		}
		return dstW, dstH
	}
	dstH := maxDim
	dstW := int(int64(srcW) * int64(maxDim) / int64(srcH))
	if dstW < 1 {
		dstW = 1
	}
	return dstW, dstH
}

func toNRGBA(src image.Image) *image.NRGBA {
	if nrgba, ok := src.(*image.NRGBA); ok {
		return nrgba
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)))
		}
	}
	return dst
}

// downscaleBox averages source pixels in each destination box with alpha weighting
// so both sharp text screenshots and transparent PNGs resize without dark halos.
func downscaleBox(src *image.NRGBA, dstW, dstH int) *image.NRGBA {
	b := src.Bounds()
	srcW := b.Dx()
	srcH := b.Dy()
	if srcW == dstW && srcH == dstH {
		cp := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
		for y := 0; y < dstH; y++ {
			srcOff := src.PixOffset(b.Min.X, b.Min.Y+y)
			dstOff := cp.PixOffset(0, y)
			copy(cp.Pix[dstOff:dstOff+dstW*4], src.Pix[srcOff:srcOff+srcW*4])
		}
		return cp
	}

	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for dy := 0; dy < dstH; dy++ {
		y0 := int(int64(dy) * int64(srcH) / int64(dstH))
		y1 := int(int64(dy+1) * int64(srcH) / int64(dstH))
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > srcH {
			y1 = srcH
		}
		for dx := 0; dx < dstW; dx++ {
			x0 := int(int64(dx) * int64(srcW) / int64(dstW))
			x1 := int(int64(dx+1) * int64(srcW) / int64(dstW))
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > srcW {
				x1 = srcW
			}

			var sumR, sumG, sumB, sumA uint64
			var count uint64
			for sy := y0; sy < y1; sy++ {
				rowOff := src.PixOffset(b.Min.X+x0, b.Min.Y+sy)
				for sx := x0; sx < x1; sx++ {
					p := src.Pix[rowOff : rowOff+4]
					a := uint64(p[3])
					sumR += uint64(p[0]) * a
					sumG += uint64(p[1]) * a
					sumB += uint64(p[2]) * a
					sumA += a
					count++
					rowOff += 4
				}
			}

			outOff := dst.PixOffset(dx, dy)
			if count == 0 || sumA == 0 {
				dst.Pix[outOff] = 0
				dst.Pix[outOff+1] = 0
				dst.Pix[outOff+2] = 0
				dst.Pix[outOff+3] = 0
				continue
			}
			dst.Pix[outOff] = uint8(sumR / sumA)
			dst.Pix[outOff+1] = uint8(sumG / sumA)
			dst.Pix[outOff+2] = uint8(sumB / sumA)
			dst.Pix[outOff+3] = uint8(sumA / count)
		}
	}
	return dst
}

func hasAlphaTransparency(img *image.NRGBA) bool {
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	for y := 0; y < h; y++ {
		off := img.PixOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < w; x++ {
			if img.Pix[off+3] < 255 {
				return true
			}
			off += 4
		}
	}
	return false
}

// GenerateImageThumbnail decodes a JPEG/PNG/GIF image safely and returns resized bytes.
func GenerateImageThumbnail(srcPath string, maxDim int) ([]byte, string, string, error) {
	st, err := os.Stat(srcPath)
	if err != nil {
		return nil, "", "", err
	}
	if st.Size() <= 0 {
		return nil, "", "", fmt.Errorf("图片文件为空")
	}
	if st.Size() > MaxThumbSourceBytes {
		return nil, "", "", fmt.Errorf("图片体积超过缩略图处理上限")
	}

	fCfg, err := os.Open(srcPath)
	if err != nil {
		return nil, "", "", err
	}
	cfg, format, err := image.DecodeConfig(fCfg)
	_ = fCfg.Close()
	if err != nil {
		return nil, "", "", fmt.Errorf("解析图片尺寸失败: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxThumbPixels {
		return nil, "", "", fmt.Errorf("图片像素分辨率超出安全限制 (%dx%d)", cfg.Width, cfg.Height)
	}

	fImg, err := os.Open(srcPath)
	if err != nil {
		return nil, "", "", err
	}
	defer fImg.Close()

	var decoded image.Image
	switch strings.ToLower(format) {
	case "jpeg":
		decoded, err = jpeg.Decode(fImg)
	case "png":
		decoded, err = png.Decode(fImg)
	case "gif":
		decoded, err = gif.Decode(fImg)
	default:
		decoded, _, err = image.Decode(fImg)
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("解码图片失败: %w", err)
	}

	targetMax := ClampThumbWidth(maxDim)
	dstW, dstH := fitBoxDimensions(decoded.Bounds().Dx(), decoded.Bounds().Dy(), targetMax)
	nrgba := toNRGBA(decoded)
	resized := downscaleBox(nrgba, dstW, dstH)

	var buf bytes.Buffer
	if (format == "png" || format == "gif") && hasAlphaTransparency(resized) {
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&buf, resized); err != nil {
			return nil, "", "", fmt.Errorf("编码 PNG 缩略图失败: %w", err)
		}
		return buf.Bytes(), "image/png", "png", nil
	}

	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 82}); err != nil {
		return nil, "", "", fmt.Errorf("编码 JPEG 缩略图失败: %w", err)
	}
	return buf.Bytes(), "image/jpeg", "jpg", nil
}

type thumbLockEntry struct {
	mu   sync.Mutex
	refs int
}

// ThumbCache persists generated thumbnails atomically on disk with per-key single-flight locks.
type ThumbCache struct {
	dir     string
	locksMu sync.Mutex
	locks   map[string]*thumbLockEntry
}

func NewThumbCache(dir string) *ThumbCache {
	return &ThumbCache{
		dir:   dir,
		locks: make(map[string]*thumbLockEntry),
	}
}

func sanitizeThumbCacheKey(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func (c *ThumbCache) acquireKeyLock(key string) func() {
	c.locksMu.Lock()
	entry, ok := c.locks[key]
	if !ok {
		entry = &thumbLockEntry{}
		c.locks[key] = entry
	}
	entry.refs++
	c.locksMu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		c.locksMu.Lock()
		entry.refs--
		if entry.refs <= 0 {
			delete(c.locks, key)
		}
		c.locksMu.Unlock()
	}
}

func (c *ThumbCache) lookupExisting(safeKey string) (string, string, bool) {
	jpgPath := filepath.Join(c.dir, safeKey+".jpg")
	if st, err := os.Stat(jpgPath); err == nil && st.Size() > 0 {
		return jpgPath, "image/jpeg", true
	}
	pngPath := filepath.Join(c.dir, safeKey+".png")
	if st, err := os.Stat(pngPath); err == nil && st.Size() > 0 {
		return pngPath, "image/png", true
	}
	return "", "", false
}

func randomThumbHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *ThumbCache) GetOrBuild(cacheKey, srcPath string, maxDim int) (string, string, error) {
	safeKey := sanitizeThumbCacheKey(cacheKey)
	if safeKey == "" {
		return "", "", fmt.Errorf("invalid thumbnail cache key")
	}
	if fp, ct, ok := c.lookupExisting(safeKey); ok {
		return fp, ct, nil
	}

	unlock := c.acquireKeyLock(safeKey)
	defer unlock()

	if fp, ct, ok := c.lookupExisting(safeKey); ok {
		return fp, ct, nil
	}

	data, contentType, outExt, err := GenerateImageThumbnail(srcPath, maxDim)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return "", "", err
	}

	targetPath := filepath.Join(c.dir, safeKey+"."+outExt)
	tmpPath := targetPath + ".tmp." + randomThumbHex(6)
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	return targetPath, contentType, nil
}
