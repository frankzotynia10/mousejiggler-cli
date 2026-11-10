//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procSendInput               = user32.NewProc("SendInput")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
	procCreateMutexW            = kernel32.NewProc("CreateMutexW")
	procOpenMutexW              = kernel32.NewProc("OpenMutexW")
	procAttachConsole           = kernel32.NewProc("AttachConsole")
)

const (
	inputMouse      = 0
	mouseeventfMove = 0x0001

	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002

	errorAlreadyExists = 183
	attachParent       = ^uintptr(0) // ATTACH_PARENT_PROCESS (-1)

	createNoWindow = 0x08000000
)

type mouseInput struct {
	dx, dy      int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// input mirrors the Win32 INPUT struct for the mouse case (40 bytes on x64).
type input struct {
	typ uint32
	mi  mouseInput
}

// jiggle sends a relative mouse move of 0,0. Windows treats it as user input
// (resets idle timer, wakes displays) but the cursor does not move, so it
// never interferes with real mouse use.
func jiggle() error {
	in := input{typ: inputMouse, mi: mouseInput{dwFlags: mouseeventfMove}}
	n, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	if n != 1 {
		return fmt.Errorf("SendInput: %v", err)
	}
	return nil
}

// awakeKeeper owns a locked OS thread, because SetThreadExecutionState's
// ES_CONTINUOUS request is per-thread and goroutines otherwise migrate.
type awakeKeeper struct{ ch chan bool }

func newAwakeKeeper() *awakeKeeper {
	k := &awakeKeeper{ch: make(chan bool)}
	go func() {
		runtime.LockOSThread()
		for on := range k.ch {
			flags := uintptr(esContinuous)
			if on {
				flags |= esDisplayRequired | esSystemRequired
			}
			procSetThreadExecutionState.Call(flags)
		}
		procSetThreadExecutionState.Call(esContinuous)
	}()
	return k
}

func (k *awakeKeeper) Set(on bool) { k.ch <- on }
func (k *awakeKeeper) Close()      { close(k.ch) }

var mutexHandle uintptr

const mutexName = `Local\` + appName

// loopRunning reports whether a `jiggler run` loop is alive in this logon
// session: it holds the named mutex for its whole lifetime, and the OS
// releases it if the process dies. No output parsing, locale-independent.
func loopRunning() bool {
	name, _ := syscall.UTF16PtrFromString(mutexName)
	h, _, _ := procOpenMutexW.Call(0x00100000 /* SYNCHRONIZE */, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return false
	}
	syscall.CloseHandle(syscall.Handle(h))
	return true
}

func singleInstance() (func(), error) {
	name, _ := syscall.UTF16PtrFromString(mutexName)
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, err
	}
	if errno, ok := err.(syscall.Errno); ok && errno == errorAlreadyExists {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, errors.New("already running")
	}
	mutexHandle = h
	return func() { syscall.CloseHandle(syscall.Handle(mutexHandle)) }, nil
}

// attachParentConsole: the exe is built as a GUI-subsystem binary so the
// scheduled task never flashes a console window. When run from a terminal,
// reattach to that terminal so CLI output is visible.
func attachParentConsole() {
	if r, _, _ := procAttachConsole.Call(attachParent); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = f
		os.Stderr = f
		fmt.Println()
	}
}

func installDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = filepath.Dir(appDir())
	}
	return filepath.Join(base, "Programs", appName)
}

func schtasks(args ...string) (string, error) {
	cmd := exec.Command("schtasks.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func install(cfg *config) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(dir, "jiggler.exe")

	// Stop any running copy so the exe can be replaced and picks up changes.
	schtasks("/End", "/TN", taskName)
	if !strings.EqualFold(filepath.Clean(self), filepath.Clean(target)) {
		if err := copyFile(self, target); err != nil {
			return "", fmt.Errorf("copy to %s: %w", target, err)
		}
	}

	u, err := user.Current()
	if err != nil {
		return "", err
	}
	xmlText := taskXML(u.Username, target, "run", cfg.sched)

	// schtasks /XML expects UTF-16 LE with BOM.
	xmlPath := filepath.Join(appDir(), "task.xml")
	_ = os.MkdirAll(appDir(), 0o755)
	u16 := utf16.Encode([]rune(xmlText))
	buf := make([]byte, 2+2*len(u16))
	buf[0], buf[1] = 0xFF, 0xFE
	for i, c := range u16 {
		buf[2+2*i] = byte(c)
		buf[3+2*i] = byte(c >> 8)
	}
	if err := os.WriteFile(xmlPath, buf, 0o644); err != nil {
		return "", err
	}

	if out, err := schtasks("/Create", "/TN", taskName, "/XML", xmlPath, "/F"); err != nil {
		return "", fmt.Errorf("schtasks /Create failed (%v): %s", err, out)
	}
	// Save only once the task exists; the task's `run` reads this file.
	if err := cfg.save(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	if out, err := schtasks("/Run", "/TN", taskName); err != nil {
		return "", fmt.Errorf("task created but failed to start: %s", out)
	}
	return fmt.Sprintf("Installed and started.\nDays: %s\nWindows: %s\nInterval: %s\nExe: %s",
		cfg.days, strings.Join(cfg.windows, ", "), cfg.interval, target), nil
}

func uninstall() (string, error) {
	schtasks("/End", "/TN", taskName)
	if out, err := schtasks("/Delete", "/TN", taskName, "/F"); err != nil {
		return "", fmt.Errorf("schtasks /Delete failed: %s", out)
	}
	return fmt.Sprintf("Task %q removed. Files in %s and %s are safe to delete.", taskName, installDir(), appDir()), nil
}

func taskStatus() string {
	out, err := schtasks("/Query", "/TN", taskName, "/FO", "LIST")
	if err != nil {
		return "not installed"
	}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "Status" {
			return strings.TrimSpace(v)
		}
	}
	return "installed"
}
