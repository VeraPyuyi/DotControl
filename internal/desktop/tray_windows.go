//go:build windows

package desktop

import (
	"context"
	"runtime"
	"syscall"
	"unsafe"
)

type point struct{ X, Y int32 }
type message struct {
	Window         uintptr
	ID             uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type windowClass struct {
	Size, Style                        uint32
	Procedure                          uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type notifyIcon struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	Title               [64]uint16
	InfoFlags           uint32
	GUID                [16]byte
	BalloonIcon         uintptr
}

// Tray runs its Win32 message loop on one thread; actions execute outside the
// callback so a slow health check never blocks the notification area.
func Tray(ctx context.Context, action func(string)) {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		user := syscall.NewLazyDLL("user32.dll")
		shell := syscall.NewLazyDLL("shell32.dll")
		kernel := syscall.NewLazyDLL("kernel32.dll")
		def := user.NewProc("DefWindowProcW")
		destroy := user.NewProc("DestroyWindow")
		post := user.NewProc("PostMessageW")
		quit := user.NewProc("PostQuitMessage")
		label, _ := syscall.UTF16PtrFromString("DotControlTray")
		var hwnd uintptr
		proc := syscall.NewCallback(func(window uintptr, id uint32, w, l uintptr) uintptr {
			if id == 0x8001 && (uint32(l) == 0x0205 || uint32(l) == 0x0203) {
				if uint32(l) == 0x0203 {
					go action("onboard")
					return 0
				}
				menu, _, _ := user.NewProc("CreatePopupMenu").Call()
				defer user.NewProc("DestroyMenu").Call(menu)
				items := []struct{ title, command string }{{"DotControl · 状态与连接", "onboard"}, {"暂停桌面任务", "pause"}, {"继续桌面任务", "resume"}, {"修复连接", "repair"}, {"退出 DotControl", "quit"}}
				for i, item := range items {
					s, _ := syscall.UTF16PtrFromString(item.title)
					user.NewProc("AppendMenuW").Call(menu, 0, uintptr(i+1), uintptr(unsafe.Pointer(s)))
				}
				var p point
				user.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&p)))
				user.NewProc("SetForegroundWindow").Call(window)
				chosen, _, _ := user.NewProc("TrackPopupMenu").Call(menu, 0x100|0x2, uintptr(p.X), uintptr(p.Y), 0, window, 0)
				if chosen > 0 && int(chosen) <= len(items) {
					go action(items[chosen-1].command)
				}
				post.Call(window, 0, 0, 0)
				return 0
			}
			if id == 0x0010 {
				destroy.Call(window)
				return 0
			}
			if id == 0x0002 {
				quit.Call(0)
				return 0
			}
			r, _, _ := def.Call(window, uintptr(id), w, l)
			return r
		})
		instance, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
		wc := windowClass{Procedure: proc, Instance: instance, Name: label}
		wc.Size = uint32(unsafe.Sizeof(wc))
		atom, _, _ := user.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			return
		}
		defer user.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(label)), instance)
		hwnd, _, _ = user.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(label)), uintptr(unsafe.Pointer(label)), 0, 0, 0, 0, 0, ^uintptr(2), 0, instance, 0)
		if hwnd == 0 {
			return
		}
		icon, _, _ := user.NewProc("LoadIconW").Call(0, 32512)
		nid := notifyIcon{Window: hwnd, ID: 1, Flags: 1 | 2 | 4, Callback: 0x8001, Icon: icon}
		nid.Size = uint32(unsafe.Sizeof(nid))
		tip, _ := syscall.UTF16FromString("DotControl · 右键查看状态")
		copy(nid.Tip[:], tip)
		ok, _, _ := shell.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&nid)))
		if ok == 0 {
			destroy.Call(hwnd)
			return
		}
		defer shell.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&nid)))
		go func() { <-ctx.Done(); post.Call(hwnd, 0x0010, 0, 0) }()
		var msg message
		for {
			r, _, _ := user.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if r == 0 || int32(r) == -1 {
				return
			}
			user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
			user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
}
