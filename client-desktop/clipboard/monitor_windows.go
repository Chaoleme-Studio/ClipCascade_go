//go:build windows

package clipboard

import (
	"bytes"
	"image/png"
	"log/slog"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	user32                         = syscall.NewLazyDLL("user32.dll")
	shell32                        = syscall.NewLazyDLL("shell32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatA   = user32.NewProc("RegisterClipboardFormatA")
	procGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalFree                 = kernel32.NewProc("GlobalFree")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory              = kernel32.NewProc("RtlMoveMemory")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procDragQueryFileW             = shell32.NewProc("DragQueryFileW")
)

const (
	CF_UNICODETEXT = 13
	CF_DIB         = 8
	CF_DIBV5       = 17
	CF_HDROP       = 15

	gmemMoveable = 0x0002
)

// getPlatformChangeCount 返回 Windows 剪贴板的序列号。
func getPlatformChangeCount() int64 {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return int64(r)
}

// getPlatformFilePaths 查询 Windows CF_HDROP 剪贴板。
func getPlatformFilePaths() ([]string, error) {
	// OpenClipboard/CloseClipboard must be called on the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return nil, nil
	}
	defer procCloseClipboard.Call()

	avail, _, _ := procIsClipboardFormatAvailable.Call(CF_HDROP)
	if avail == 0 {
		return nil, nil
	}

	hDrop, _, _ := procGetClipboardData.Call(CF_HDROP)
	if hDrop == 0 {
		return nil, nil
	}

	ptr, _, _ := procGlobalLock.Call(hDrop)
	if ptr == 0 {
		return nil, nil
	}
	defer procGlobalUnlock.Call(hDrop)

	count, _, _ := procDragQueryFileW.Call(hDrop, 0xFFFFFFFF, 0, 0)
	if count == 0 || count > 1000 {
		return nil, nil
	}

	var paths []string
	for i := uintptr(0); i < count; i++ {
		size, _, _ := procDragQueryFileW.Call(hDrop, i, 0, 0)
		if size == 0 {
			continue
		}

		buf := make([]uint16, size+1)
		procDragQueryFileW.Call(hDrop, i, uintptr(unsafe.Pointer(&buf[0])), size+1)

		path := windows.UTF16ToString(buf)
		if path != "" {
			paths = append(paths, path)
		}
	}

	return paths, nil
}

func setPlatformFilePaths(paths []string) error {
	payload := buildWindowsHDropPayload(paths)
	if len(payload) == 0 {
		return nil
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hDrop, err := allocGlobalMem(payload)
	if err != nil {
		return err
	}

	if err := openClipboardForWrite(); err != nil {
		procGlobalFree.Call(hDrop)
		return err
	}
	defer procCloseClipboard.Call()

	r, _, err := procEmptyClipboard.Call()
	if r == 0 {
		procGlobalFree.Call(hDrop)
		return err
	}

	if r, _, err := procSetClipboardData.Call(CF_HDROP, hDrop); r == 0 {
		procGlobalFree.Call(hDrop)
		return err
	}

	slog.Info("剪贴板：Windows 已写入文件剪贴板", "文件数", len(paths))
	return nil
}

func getPlatformImage() ([]byte, error) {
	if pngFormat := getRegisteredClipboardFormat("PNG"); pngFormat != 0 {
		data, err := readClipboardBytes(pngFormat)
		if err == nil && len(data) > 0 {
			if _, err := png.Decode(bytes.NewReader(data)); err == nil {
				slog.Debug("剪贴板：Windows 原生读取 PNG 注册格式")
				return data, nil
			}
			slog.Debug("剪贴板：Windows PNG 注册格式不是有效 PNG，继续尝试 DIB", "错误", err)
		}
	}

	data, err := readClipboardBytes(CF_DIBV5)
	if err != nil || len(data) == 0 {
		data, err = readClipboardBytes(CF_DIB)
		if err != nil || len(data) == 0 {
			return nil, err
		}
	}

	img, err := decodeDIB(data)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func getPlatformText() ([]byte, error) {
	data, err := readClipboardBytes(CF_UNICODETEXT)
	if err != nil || len(data) == 0 {
		return nil, err
	}

	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	u16 := unsafe.Slice((*uint16)(unsafe.Pointer(&data[0])), len(data)/2)
	text := windows.UTF16ToString(u16)
	if text == "" {
		return nil, nil
	}
	return []byte(text), nil
}

func setPlatformText(text string) error {
	return nil
}

func setPlatformImage(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	formats, err := encodeWindowsImageClipboardFormats(data)
	if err != nil {
		return err
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 先分配全部内存，再打开剪贴板。
	// 这样即使分配失败，EmptyClipboard 也不会被调用，剪贴板原有内容得以保留。
	hDib, err := allocGlobalMem(formats.dib)
	if err != nil {
		return err
	}

	hDibV5, err := allocGlobalMem(formats.dibV5)
	if err != nil {
		procGlobalFree.Call(hDib)
		return err
	}

	pngFormat := getRegisteredClipboardFormat("PNG")
	var hPng uintptr
	if pngFormat != 0 {
		hPng, _ = allocGlobalMem(formats.png) // PNG 是可选格式，忽略分配失败
	}

	if err := openClipboardForWrite(); err != nil {
		procGlobalFree.Call(hDib)
		procGlobalFree.Call(hDibV5)
		if hPng != 0 {
			procGlobalFree.Call(hPng)
		}
		return err
	}
	defer procCloseClipboard.Call()

	r, _, err := procEmptyClipboard.Call()
	if r == 0 {
		procGlobalFree.Call(hDib)
		procGlobalFree.Call(hDibV5)
		if hPng != 0 {
			procGlobalFree.Call(hPng)
		}
		return err
	}

	// SetClipboardData 成功后，句柄所有权转移给系统，不能再 free。
	if r, _, e := procSetClipboardData.Call(CF_DIB, hDib); r == 0 {
		procGlobalFree.Call(hDib)
		procGlobalFree.Call(hDibV5)
		if hPng != 0 {
			procGlobalFree.Call(hPng)
		}
		return e
	}

	if r, _, _ := procSetClipboardData.Call(CF_DIBV5, hDibV5); r == 0 {
		procGlobalFree.Call(hDibV5)
		slog.Warn("剪贴板：Windows 写入 CF_DIBV5 失败")
	}

	if hPng != 0 {
		if r, _, _ := procSetClipboardData.Call(pngFormat, hPng); r == 0 {
			procGlobalFree.Call(hPng)
			slog.Warn("剪贴板：Windows 写入 PNG 注册格式失败")
		}
	}

	return nil
}

func readClipboardBytes(format uintptr) ([]byte, error) {
	// OpenClipboard/CloseClipboard must be called on the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	r, _, err := procOpenClipboard.Call(0)
	if r == 0 {
		return nil, err
	}
	defer procCloseClipboard.Call()

	avail, _, _ := procIsClipboardFormatAvailable.Call(format)
	if avail == 0 {
		return nil, nil
	}

	handle, _, _ := procGetClipboardData.Call(format)
	if handle == 0 {
		return nil, nil
	}

	ptr, _, _ := procGlobalLock.Call(handle)
	if ptr == 0 {
		return nil, nil
	}
	defer procGlobalUnlock.Call(handle)

	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 {
		return nil, nil
	}

	buf := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(size))
	out := make([]byte, len(buf))
	copy(out, buf)
	return out, nil
}

func openClipboardForWrite() error {
	for i := 0; i < 20; i++ {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		if err == windows.ERROR_ACCESS_DENIED {
			windows.SleepEx(10, false)
			continue
		}
	}
	// 最后一次尝试：必须检查返回值 r 而非 err。
	// Windows 的 GetLastError 在成功调用后不会自动清零，err 可能残留上一次
	// 的 ERROR_ACCESS_DENIED，导致误报失败并使剪贴板处于锁定状态。
	r, _, err := procOpenClipboard.Call(0)
	if r != 0 {
		return nil
	}
	return err
}

func getRegisteredClipboardFormat(name string) uintptr {
	ptr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipboardFormatA.Call(uintptr(unsafe.Pointer(ptr)))
	return r
}

// allocGlobalMem 分配可移动全局内存并填充 data，返回句柄。
// 调用方负责在不再需要时调用 GlobalFree，但若将句柄传给 SetClipboardData 成功，
// 则所有权转移给系统，不可再 free。
func allocGlobalMem(data []byte) (uintptr, error) {
	if len(data) == 0 {
		return 0, syscall.EINVAL
	}
	hMem, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if hMem == 0 {
		return 0, err
	}
	ptr, _, err := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0, err
	}
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	procGlobalUnlock.Call(hMem)
	return hMem, nil
}

func setClipboardDataBytes(format uintptr, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	hMem, err := allocGlobalMem(data)
	if err != nil {
		return err
	}
	r, _, err := procSetClipboardData.Call(format, hMem)
	if r == 0 {
		procGlobalFree.Call(hMem)
		return err
	}
	return nil
}
