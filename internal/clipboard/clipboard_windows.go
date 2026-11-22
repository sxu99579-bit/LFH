//go:build windows

package clipboard

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	defaultPollInterval = 800 * time.Millisecond
	cfDIB               = 8
	cfUnicodeText       = 13
	biRGB               = 0
	biBitfields         = 3
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetClipboardSeqNum  = user32.NewProc("GetClipboardSequenceNumber")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procIsClipboardFmtAvail = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData    = user32.NewProc("GetClipboardData")
	procGlobalLock          = kernel32.NewProc("GlobalLock")
	procGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	procGlobalSize          = kernel32.NewProc("GlobalSize")
)

func clipboardSequence() uint32 {
	r, _, _ := procGetClipboardSeqNum.Call()
	return uint32(r)
}

func openClipboardWithRetry() bool {
	for i := 0; i < 3; i++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			return true
		}
		time.Sleep(15 * time.Millisecond)
	}
	return false
}

func readClipboardBytes(format uintptr) ([]byte, bool) {
	avail, _, _ := procIsClipboardFmtAvail.Call(format)
	if avail == 0 {
		return nil, false
	}
	hMem, _, _ := procGetClipboardData.Call(format)
	if hMem == 0 {
		return nil, false
	}
	szPtr, _, _ := procGlobalSize.Call(hMem)
	sz := int(szPtr)
	if sz <= 0 || sz > MaxCaptureBytes*2 {
		return nil, false
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(hMem)

	src := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), sz)
	cp := make([]byte, len(src))
	copy(cp, src)
	return cp, true
}

func decodeUTF16Clipboard(raw []byte) (string, bool) {
	n := len(raw) / 2
	if n <= 0 {
		return "", false
	}
	u16 := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		v := binary.LittleEndian.Uint16(raw[i*2:])
		if v == 0 {
			break
		}
		u16 = append(u16, v)
	}
	if len(u16) == 0 {
		return "", false
	}
	text := string(utf16.Decode(u16))
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func encodeDIBToImage(dib []byte) (ext string, data []byte, ok bool) {
	if len(dib) < 40 {
		return "", nil, false
	}
	headerSize := int(binary.LittleEndian.Uint32(dib[0:4]))
	if headerSize < 40 || headerSize > len(dib) {
		return "", nil, false
	}
	width := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	heightSigned := int(int32(binary.LittleEndian.Uint32(dib[8:12])))
	planes := binary.LittleEndian.Uint16(dib[12:14])
	bitCount := binary.LittleEndian.Uint16(dib[14:16])
	compression := binary.LittleEndian.Uint32(dib[16:20])
	clrUsed := int(binary.LittleEndian.Uint32(dib[32:36]))

	topDown := heightSigned < 0
	height := heightSigned
	if height < 0 {
		height = -height
	}
	if planes != 1 || width <= 0 || height <= 0 || width > 16384 || height > 16384 {
		return "", nil, false
	}

	paletteOffset := headerSize
	maskBytes := 0
	if compression == biBitfields && headerSize == 40 {
		maskBytes = 12
	}
	paletteEntries := 0
	if bitCount <= 8 {
		if clrUsed > 0 {
			paletteEntries = clrUsed
		} else {
			paletteEntries = 1 << bitCount
		}
	} else if clrUsed > 0 {
		paletteEntries = clrUsed
	}
	pixelOffset := paletteOffset + maskBytes + paletteEntries*4
	if pixelOffset > len(dib) {
		return "", nil, false
	}

	if (compression == biRGB && (bitCount == 24 || bitCount == 32)) || (compression == biBitfields && bitCount == 32) {
		stride := ((width*int(bitCount) + 31) / 32) * 4
		needed := pixelOffset + stride*height
		if needed <= len(dib) {
			img := image.NewNRGBA(image.Rect(0, 0, width, height))
			hasNonZeroAlpha := false
			for y := 0; y < height; y++ {
				srcY := y
				if !topDown {
					srcY = height - 1 - y
				}
				row := dib[pixelOffset+srcY*stride:]
				dstOff := y * img.Stride
				if bitCount == 24 {
					for x := 0; x < width; x++ {
						i := x * 3
						img.Pix[dstOff+x*4+0] = row[i+2] // R
						img.Pix[dstOff+x*4+1] = row[i+1] // G
						img.Pix[dstOff+x*4+2] = row[i+0] // B
						img.Pix[dstOff+x*4+3] = 255
					}
					hasNonZeroAlpha = true
				} else {
					for x := 0; x < width; x++ {
						i := x * 4
						a := row[i+3]
						if a != 0 {
							hasNonZeroAlpha = true
						}
						img.Pix[dstOff+x*4+0] = row[i+2] // R
						img.Pix[dstOff+x*4+1] = row[i+1] // G
						img.Pix[dstOff+x*4+2] = row[i+0] // B
						img.Pix[dstOff+x*4+3] = a
					}
				}
			}
			// Windows screenshot DIBs frequently leave alpha=0 on opaque 32-bit pixels.
			if bitCount == 32 && !hasNonZeroAlpha {
				for i := 3; i < len(img.Pix); i += 4 {
					img.Pix[i] = 255
				}
			}
			var pngBuf bytes.Buffer
			if err := png.Encode(&pngBuf, img); err == nil && pngBuf.Len() <= MaxCaptureBytes {
				return ".png", pngBuf.Bytes(), true
			}
		}
	}

	// Fallback: prepend 14-byte BITMAPFILEHEADER and emit BMP.
	if 14+len(dib) > MaxCaptureBytes {
		return "", nil, false
	}
	bmp := make([]byte, 14+len(dib))
	bmp[0] = 'B'
	bmp[1] = 'M'
	binary.LittleEndian.PutUint32(bmp[2:6], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[10:14], uint32(14+pixelOffset))
	copy(bmp[14:], dib)
	return ".bmp", bmp, true
}

func readClipboard() (Item, bool) {
	if !openClipboardWithRetry() {
		return Item{}, false
	}
	defer procCloseClipboard.Call()

	if rawText, ok := readClipboardBytes(cfUnicodeText); ok {
		if text, valid := decodeUTF16Clipboard(rawText); valid && len(text) <= MaxCaptureBytes {
			data := []byte(text)
			return Item{
				Kind:      "text",
				Ext:       ".txt",
				Data:      data,
				Hash:      hashBytesHex(data),
				CreatedAt: time.Now(),
			}, true
		}
	}

	if rawDIB, ok := readClipboardBytes(cfDIB); ok {
		if ext, imgData, valid := encodeDIBToImage(rawDIB); valid {
			return Item{
				Kind:      "image",
				Ext:       ext,
				Data:      imgData,
				Hash:      hashBytesHex(imgData),
				CreatedAt: time.Now(),
			}, true
		}
	}

	return Item{}, false
}
