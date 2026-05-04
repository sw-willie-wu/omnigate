//go:build windows

package iconext

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Cache: bounded by total bytes (~32 MB).
const maxCacheBytes = 32 * 1024 * 1024

type cacheEntry struct {
	mtime int64
	data  []byte
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
	cached  int // total bytes currently held
)

func extractImpl(exePath string) ([]byte, error) {
	st, err := os.Stat(exePath)
	if err != nil {
		return nil, err
	}
	mtime := st.ModTime().UnixNano()

	cacheMu.Lock()
	if e, ok := cache[exePath]; ok && e.mtime == mtime {
		out := e.data
		cacheMu.Unlock()
		return out, nil
	}
	cacheMu.Unlock()

	bytesOut, err := extractFromExe(exePath)
	if err != nil {
		return nil, err
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()
	// Evict oldest entries (random map ordering is fine for M2; a real LRU
	// would need a list — out of M2 scope) until the new entry fits.
	for cached+len(bytesOut) > maxCacheBytes && len(cache) > 0 {
		for k, v := range cache {
			cached -= len(v.data)
			delete(cache, k)
			break
		}
	}
	cache[exePath] = cacheEntry{mtime: mtime, data: bytesOut}
	cached += len(bytesOut)
	return bytesOut, nil
}

// extractFromExe loads the largest icon from the .exe via ExtractIconExW,
// converts the HICON to RGBA pixels via GetIconInfo + GetDIBits, and PNG-
// encodes the result.
func extractFromExe(exePath string) ([]byte, error) {
	pathPtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return nil, fmt.Errorf("utf16 path: %w", err)
	}

	var large windows.Handle
	// ExtractIconExW(path, 0, &large, nil, 1) -> count of icons in the file
	// (returns >0 on success; the returned HICON is in `large`).
	r, _, _ := procExtractIconExW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		uintptr(unsafe.Pointer(&large)),
		0,
		1,
	)
	if r == 0 || r == ^uintptr(0) || large == 0 {
		return nil, fmt.Errorf("%w: no icons in %s", ErrUnsupported, exePath)
	}
	defer procDestroyIcon.Call(uintptr(large))

	return iconToPNG(large)
}

// iconToPNG converts an HICON to a PNG byte slice via GetIconInfo + GetDIBits.
func iconToPNG(hIcon windows.Handle) ([]byte, error) {
	var info iconInfo
	if r, _, _ := procGetIconInfo.Call(uintptr(hIcon), uintptr(unsafe.Pointer(&info))); r == 0 {
		return nil, fmt.Errorf("%w: GetIconInfo failed", ErrUnsupported)
	}
	defer procDeleteObject.Call(uintptr(info.hbmColor))
	defer procDeleteObject.Call(uintptr(info.hbmMask))

	// Inspect the color bitmap dimensions.
	var bm bitmap
	if r, _, _ := procGetObjectW.Call(
		uintptr(info.hbmColor),
		unsafe.Sizeof(bm),
		uintptr(unsafe.Pointer(&bm)),
	); r == 0 {
		return nil, fmt.Errorf("%w: GetObject failed", ErrUnsupported)
	}
	width, height := int(bm.width), int(bm.height)
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("%w: zero icon dimensions", ErrUnsupported)
	}

	hdc, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, hdc)

	// BITMAPINFOHEADER for 32bpp top-down BGRA.
	bi := bitmapInfo{}
	bi.header.biSize = uint32(unsafe.Sizeof(bi.header))
	bi.header.biWidth = int32(width)
	bi.header.biHeight = -int32(height) // top-down
	bi.header.biPlanes = 1
	bi.header.biBitCount = 32
	bi.header.biCompression = 0 // BI_RGB

	pixels := make([]byte, width*height*4)
	r, _, _ := procGetDIBits.Call(
		hdc,
		uintptr(info.hbmColor),
		0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bi)),
		0, // DIB_RGB_COLORS
	)
	if r == 0 {
		return nil, fmt.Errorf("%w: GetDIBits failed", ErrUnsupported)
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			b, g, r, a := pixels[i], pixels[i+1], pixels[i+2], pixels[i+3]
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("png encode: %w", err)
	}
	return buf.Bytes(), nil
}

// Win32 plumbing -------------------------------------------------------------

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procExtractIconExW = shell32.NewProc("ExtractIconExW")
	procDestroyIcon    = user32.NewProc("DestroyIcon")
	procGetIconInfo    = user32.NewProc("GetIconInfo")
	procGetDC          = user32.NewProc("GetDC")
	procReleaseDC      = user32.NewProc("ReleaseDC")
	procGetObjectW     = gdi32.NewProc("GetObjectW")
	procGetDIBits      = gdi32.NewProc("GetDIBits")
	procDeleteObject   = gdi32.NewProc("DeleteObject")
)

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

type bitmap struct {
	bmType       int32
	width        int32
	height       int32
	widthBytes   int32
	planes       uint16
	bitsPerPixel uint16
	bits         uintptr
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	header   bitmapInfoHeader
	colors   [256]uint32 // unused for 32bpp, but keeps the alloc one-shot
}
