//go:build windows

package tunnel

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

// WinHTTP reads the current user's Internet Settings, including bypass and
// automatic configuration, without exposing or modifying them in diagnostics.
func readSystemProxy(ctx context.Context) (systemProxy, error) {
	if err := ctx.Err(); err != nil {
		return systemProxy{}, err
	}
	var cfg struct {
		AutoDetect                        uint32
		AutoConfigURL, Proxy, ProxyBypass *uint16
	}
	proc := windows.NewLazySystemDLL("winhttp.dll").NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&cfg)))
	if ok == 0 {
		return systemProxy{}, errors.New("system_proxy_unavailable")
	}
	free := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")
	for _, p := range []*uint16{cfg.AutoConfigURL, cfg.Proxy, cfg.ProxyBypass} {
		if p != nil {
			defer free.Call(uintptr(unsafe.Pointer(p)))
		}
	}
	return parseWindowsProxy(windows.UTF16PtrToString(cfg.Proxy), windows.UTF16PtrToString(cfg.ProxyBypass), cfg.AutoDetect != 0, windows.UTF16PtrToString(cfg.AutoConfigURL)), nil
}
