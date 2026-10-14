//go:build windows

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackagedGUIChildHasRealInteractiveConsole(t *testing.T) {
	// Compile the actual implementation with the installer's GUI subsystem flag.
	// A normal console go-test executable does not expose the packaged failure.
	source, err := os.ReadFile("interactive_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(source), "package desktop", "package main", 1)
	text += `\nfunc main(){
 if len(os.Args)>1 && os.Args[1]=="child" {
  err:=PrepareInteractiveIO();var a,b uint32
  ie:=syscall.GetConsoleMode(syscall.Handle(os.Stdin.Fd()),&a);oe:=syscall.GetConsoleMode(syscall.Handle(os.Stdout.Fd()),&b)
  window,_,_:=syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call();syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow").Call(window,0)
  result:="failed";if err==nil && ie==nil && oe==nil{result="console_ready"};os.WriteFile(os.Args[2],[]byte(result),0600);return
 }
 exe,_:=os.Executable();cmd:=exec.Command(exe,"child",os.Args[1]);cmd.SysProcAttr=&syscall.SysProcAttr{CreationFlags:0x10,HideWindow:true};cmd.Run()
}`
	text = strings.ReplaceAll(text, `\nfunc main`, "\nfunc main")
	dir := t.TempDir()
	file := filepath.Join(dir, "probe.go")
	binary := filepath.Join(dir, "probe.exe")
	result := filepath.Join(dir, "result.txt")
	if err = os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go.exe"), "build", "-ldflags", "-H=windowsgui", "-o", binary, file)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cmd := exec.Command(binary, result)
	HideConsole(cmd)
	if err = cmd.Run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result)
	if err != nil || string(data) != "console_ready" {
		t.Fatalf("GUI console rejected: %s %v", data, err)
	}
}
