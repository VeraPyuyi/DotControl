//go:build windows

package activity

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var lastPhysical atomic.Int64
var generation atomic.Uint64

type winClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	IconSm                             uintptr
}
type winMsg struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}
type rawDevice struct {
	Page, Usage uint16
	Flags       uint32
	Target      uintptr
}
type lastInput struct{ Size, Time uint32 }

type systemInputTracker struct {
	lastActivity *atomic.Int64
	generation   *atomic.Uint64
	initialized  bool
	lastTick     uint32
}

func (s *systemInputTracker) merge(nowMillis int64, tickNow, lastTick uint32, available bool) bool {
	idle := tickNow - lastTick // unsigned subtraction handles the 32-bit tick wrap.
	if !available || idle > 1<<31 {
		// Missing or implausible timing is activity uncertainty, never evidence
		// of idleness. Preserve this conservative timestamp after recovery.
		s.raiseLastActivity(nowMillis)
		s.generation.Add(1)
		return false
	}
	if s.initialized && s.lastTick != lastTick {
		// The system timestamp is not guaranteed to increase. Any change counts,
		// including input not classified by our mouse/keyboard Raw Input sink.
		// Injected input may also advance it; conservative self-pauses are allowed.
		s.raiseLastActivity(nowMillis)
		s.generation.Add(1)
	}
	s.initialized = true
	s.lastTick = lastTick
	// Reconcile continuously so a wall-clock adjustment cannot manufacture a
	// longer idle interval. A newer Raw Input event always wins this merge.
	s.raiseLastActivity(nowMillis - int64(idle))
	return true
}

func (s *systemInputTracker) raiseLastActivity(value int64) {
	for previous := s.lastActivity.Load(); value > previous; previous = s.lastActivity.Load() {
		if s.lastActivity.CompareAndSwap(previous, value) {
			return
		}
	}
}

func proc(name string, args ...uintptr) uintptr {
	r, _, _ := user32.NewProc(name).Call(args...)
	return r
}

func Start(ctx context.Context, _ string) (*Monitor, error) {
	m := &Monitor{}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.close = func() { cancel(); <-done }
	ready := make(chan error, 1)
	// Raw input is delivered to a hidden message-only window even when our app
	// isn't focused. We never read or persist RAWINPUT payloads.
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		name, _ := windows.UTF16PtrFromString("DotControlActivityWindow")
		callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
			if msg == 0x00ff {
				lastPhysical.Store(time.Now().UnixMilli())
				generation.Add(1)
			}
			return proc("DefWindowProcW", hwnd, uintptr(msg), w, l)
		})
		wc := winClass{Proc: callback, ClassName: name}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if proc("RegisterClassExW", uintptr(unsafe.Pointer(&wc))) == 0 {
			ready <- fmt.Errorf("activity window registration failed")
			return
		}
		defer proc("UnregisterClassW", uintptr(unsafe.Pointer(name)), 0)
		hwnd := proc("CreateWindowExW", 0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
		if hwnd == 0 {
			ready <- fmt.Errorf("activity window creation failed")
			return
		}
		defer proc("DestroyWindow", hwnd)
		devices := []rawDevice{{Page: 1, Usage: 2, Flags: 0x100, Target: hwnd}, {Page: 1, Usage: 6, Flags: 0x100, Target: hwnd}}
		if proc("RegisterRawInputDevices", uintptr(unsafe.Pointer(&devices[0])), 2, unsafe.Sizeof(devices[0])) == 0 {
			ready <- fmt.Errorf("raw input unavailable")
			return
		}
		// Raw Input covers mouse/keyboard; continuously merge the session's last
		// input timestamp for unclassified input such as touch/pen. Do not try to
		// subtract injected input: false busy states are safer than missed humans.
		systemInput := &systemInputTracker{lastActivity: &lastPhysical, generation: &generation}
		getTickCount := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")
		sampleSystemInput := func() bool {
			li := lastInput{Size: uint32(unsafe.Sizeof(lastInput{}))}
			available := proc("GetLastInputInfo", uintptr(unsafe.Pointer(&li))) != 0
			tick, _, _ := getTickCount.Call()
			return systemInput.merge(time.Now().UnixMilli(), uint32(tick), li.Time, available)
		}
		sampleSystemInput()
		ready <- nil
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			var msg winMsg
			for proc("PeekMessageW", uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1) != 0 {
				proc("TranslateMessage", uintptr(unsafe.Pointer(&msg)))
				proc("DispatchMessageW", uintptr(unsafe.Pointer(&msg)))
			}
			systemAvailable := sampleSystemInput()
			fg := proc("GetForegroundWindow")
			var pid uint32
			proc("GetWindowThreadProcessId", fg, uintptr(unsafe.Pointer(&pid)))
			// The input desktop must be Default; Winlogon/secure desktops fail closed.
			desktop := proc("OpenInputDesktop", 0, 0, 1)
			unlocked := false
			if desktop != 0 {
				var buf [128]uint16
				var needed uint32
				if proc("GetUserObjectInformationW", desktop, 2, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Sizeof(buf)), uintptr(unsafe.Pointer(&needed))) != 0 {
					unlocked = windows.UTF16ToString(buf[:]) == "Default"
				}
				proc("CloseDesktop", desktop)
			}
			m.update(model.Activity{Available: fg != 0 && systemAvailable, Locked: !unlocked, IdleMillis: time.Now().UnixMilli() - lastPhysical.Load(), Generation: generation.Load(), ForegroundPID: int(pid), ForegroundWindow: strconv.FormatUint(uint64(fg), 10)})
		}
	}()
	if err := <-ready; err != nil {
		cancel()
		return m, err
	}
	return m, nil
}
