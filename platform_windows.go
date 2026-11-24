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

	procGetLastInputInfo          = user32.NewProc("GetLastInputInfo")
	procGetTickCount              = kernel32.NewProc("GetTickCount")
	procOpenInputDesktop          = user32.NewProc("OpenInputDesktop")
	procCloseDesktop              = user32.NewProc("CloseDesktop")
	procGetUserObjectInformationW = user32.NewProc("GetUserObjectInformationW")
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
// jiggle moves the pointer 1px right and straight back in one atomic
// SendInput call, so nothing can land in between and the pointer ends where
// it started. A zero-distance move is ignored by current Windows builds: it
// neither resets the idle timer (Teams goes Away) nor wakes monitors.
func jiggle() error { return sendMoves([2]int32{1, 0}, [2]int32{-1, 0}) }

// zeroJiggle is the old zero-distance move, kept only so `jiggler test` can
// show whether this machine honors it.
func zeroJiggle() error { return sendMoves([2]int32{0, 0}) }

// idleSeconds is the system idle time Teams and the screen timeout use.
func idleSeconds() uint32 {
	var lii struct{ cbSize, dwTime uint32 }
	lii.cbSize = 8
	procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	now, _, _ := procGetTickCount.Call()
	return (uint32(now) - lii.dwTime) / 1000
}

func sendMoves(moves ...[2]int32) error {
	ins := make([]input, len(moves))
	for i, m := range moves {
		ins[i] = input{typ: inputMouse, mi: mouseInput{dx: m[0], dy: m[1], dwFlags: mouseeventfMove}}
	}
	n, _, err := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
	if int(n) != len(ins) {
		return fmt.Errorf("SendInput: %v", err)
	}
	return nil
}

// sessionLocked reports whether the input desktop is the lock screen.
// Windows silently drops injected input there (SendInput still reports
// success), so this is the only way to know the jiggle was blocked.
func sessionLocked() bool {
	h, _, _ := procOpenInputDesktop.Call(0, 0, 0x0001 /* DESKTOP_READOBJECTS */)
	if h == 0 {
		return true // the secure desktop can't be opened from a user process
	}
	defer procCloseDesktop.Call(h)
	var buf [64]uint16
	var needed uint32
	r, _, _ := procGetUserObjectInformationW.Call(h, 2 /* UOI_NAME */, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)*2), uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		return false // can't tell; assume unlocked rather than spam the log
	}
	return !strings.EqualFold(syscall.UTF16ToString(buf[:]), "Default")
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
