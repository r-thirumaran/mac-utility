//go:build windows

package main

import (
	"fmt"
	"math"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

// Master volume through Core Audio (IMMDeviceEnumerator →
// IAudioEndpointVolume) with raw vtable calls. Methods taking a FLOAT by
// value cannot be called through syscall on x64 (floats travel in XMM
// registers), so volume.set walks VolumeStepUp/Down towards the target.
var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	iidIAudioEndpointVolume = windows.GUID{Data1: 0x5cdf2c82, Data2: 0x841e, Data3: 0x4546, Data4: [8]byte{0x97, 0x22, 0x0c, 0xf7, 0x40, 0x78, 0x22, 0x9a}}
)

const (
	clsctxAll = 0x17
	eRender   = 0
	eConsole  = 0

	// vtable slots (IUnknown: 0 QueryInterface, 1 AddRef, 2 Release)
	vtRelease                 = 2
	vtGetDefaultAudioEndpoint = 4 // IMMDeviceEnumerator
	vtActivate                = 3 // IMMDevice
	vtGetMasterVolumeScalar   = 9 // IAudioEndpointVolume
	vtSetMute                 = 14
	vtGetMute                 = 15
	vtGetVolumeStepInfo       = 16
	vtVolumeStepUp            = 17
	vtVolumeStepDown          = 18
)

type comObject struct{ vtbl *[32]uintptr }

func (o *comObject) call(slot int, args ...uintptr) uintptr {
	all := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[slot], all...)
	return r
}

func (o *comObject) release() { o.call(vtRelease) }

func hresult(r uintptr, what string) error {
	if r != 0 {
		return fmt.Errorf("%s: HRESULT 0x%08x", what, uint32(r))
	}
	return nil
}

// withEndpointVolume opens the default render endpoint's volume control on
// a COM-initialised thread and runs fn with it.
func withEndpointVolume(fn func(vol *comObject) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil || isSFalse(err) {
		defer windows.CoUninitialize()
	}
	var enum *comObject
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll, uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enum)))
	if err := hresult(r, "CoCreateInstance(MMDeviceEnumerator)"); err != nil {
		return err
	}
	defer enum.release()
	var dev *comObject
	if err := hresult(enum.call(vtGetDefaultAudioEndpoint, eRender, eConsole, uintptr(unsafe.Pointer(&dev))), "GetDefaultAudioEndpoint"); err != nil {
		return err
	}
	defer dev.release()
	var vol *comObject
	if err := hresult(dev.call(vtActivate, uintptr(unsafe.Pointer(&iidIAudioEndpointVolume)), clsctxAll, 0, uintptr(unsafe.Pointer(&vol))), "Activate(IAudioEndpointVolume)"); err != nil {
		return err
	}
	defer vol.release()
	return fn(vol)
}

func masterScalar(vol *comObject) (float32, error) {
	var level float32
	if err := hresult(vol.call(vtGetMasterVolumeScalar, uintptr(unsafe.Pointer(&level))), "GetMasterVolumeLevelScalar"); err != nil {
		return 0, err
	}
	return level, nil
}

func notSupported(err error) error {
	return bridge.Errorf(bridge.CodeNotSupported, "master volume unavailable: %v", err)
}

func (w *winBackend) VolumeGet() (int, bool, error) {
	var level float32
	var muted int32
	err := withEndpointVolume(func(vol *comObject) error {
		l, err := masterScalar(vol)
		if err != nil {
			return err
		}
		level = l
		return hresult(vol.call(vtGetMute, uintptr(unsafe.Pointer(&muted))), "GetMute")
	})
	if err != nil {
		return 0, false, notSupported(err)
	}
	return int(math.Round(float64(level) * 100)), muted != 0, nil
}

// VolumeSet steps the master volume towards level (0..100) and unmutes
// when level > 0.
func (w *winBackend) VolumeSet(level int) error {
	target := float32(clampI(level, 0, 100)) / 100
	err := withEndpointVolume(func(vol *comObject) error {
		if level > 0 {
			_ = vol.call(vtSetMute, 0, 0)
		}
		var step, steps uint32
		eps := float32(0.005)
		if hresult(vol.call(vtGetVolumeStepInfo, uintptr(unsafe.Pointer(&step)), uintptr(unsafe.Pointer(&steps))), "") == nil && steps > 1 {
			eps = 0.5 / float32(steps-1)
		}
		last := float32(-1)
		for i := 0; i < 256; i++ {
			cur, err := masterScalar(vol)
			if err != nil {
				return err
			}
			if cur == last && i > 0 {
				return nil // stuck at a bound
			}
			last = cur
			switch {
			case cur < target-eps:
				if err := hresult(vol.call(vtVolumeStepUp, 0), "VolumeStepUp"); err != nil {
					return err
				}
			case cur > target+eps:
				if err := hresult(vol.call(vtVolumeStepDown, 0), "VolumeStepDown"); err != nil {
					return err
				}
			default:
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return notSupported(err)
	}
	return nil
}
