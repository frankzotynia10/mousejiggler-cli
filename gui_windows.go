//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Native Win32 settings window (no third-party deps). It edits the same
// config.json the CLI uses and applies it via install().

var (
	gdi32 = syscall.NewLazyDLL("gdi32.dll")

	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procIsDialogMessageW     = user32.NewProc("IsDialogMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procSetWindowTextW       = user32.NewProc("SetWindowTextW")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procLoadCursorW          = user32.NewProc("LoadCursorW")
	procLoadIconW            = user32.NewProc("LoadIconW")
	procSetProcessDPIAware   = user32.NewProc("SetProcessDPIAware")
	procGetDC                = user32.NewProc("GetDC")
	procReleaseDC            = user32.NewProc("ReleaseDC")
	procAdjustWindowRect     = user32.NewProc("AdjustWindowRect")
	procShowWindow           = user32.NewProc("ShowWindow")
	procSetTimer             = user32.NewProc("SetTimer")
	procGetDeviceCaps        = gdi32.NewProc("GetDeviceCaps")
	procCreateFontW          = gdi32.NewProc("CreateFontW")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procCreateActCtxW        = kernel32.NewProc("CreateActCtxW")
	procActivateActCtx       = kernel32.NewProc("ActivateActCtx")
)

const (
	wmDestroy  = 0x0002
	wmClose    = 0x0010
	wmSetFont  = 0x0030
	wmCommand  = 0x0111
	wmTimer    = 0x0113
	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1

	wsOverlappedWindow = 0x00C00000 | 0x00080000 | 0x00020000 // caption|sysmenu|minimizebox
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsTabStop          = 0x00010000
	wsVScroll          = 0x00200000
	wsExClientEdge     = 0x00000200

	bsPushButton    = 0x0
	bsDefPushButton = 0x1
	bsAutoCheckbox  = 0x3
	esMultiline     = 0x0004
	esAutoVScroll   = 0x0040
	esAutoHScroll   = 0x0080
	esWantReturn    = 0x1000

	mbIconError = 0x10
	mbIconInfo  = 0x40
	mbYesNo     = 0x04
	mbIconQ     = 0x20
	idYes       = 6

	idInstall   = 100
	idUninstall = 101
	idTest      = 102
	idPause     = 103
	idResume    = 104
	idDayBase   = 200 // +0..6 for Mon..Sun
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

type rect struct{ left, top, right, bottom int32 }

type actCtx struct {
	cbSize                 uint32
	dwFlags                uint32
	lpSource               *uint16
	wProcessorArchitecture uint16
	wLangID                uint16
	lpAssemblyDirectory    *uint16
	lpResourceName         *uint16
	lpApplicationName      *uint16
	hModule                uintptr
}

var guiDayOrder = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

type guiState struct {
	hwnd     uintptr
	dpi      int
	font     uintptr
	days     [7]uintptr // Mon..Sun
	windows  uintptr
	interval uintptr
	status   uintptr
}

var g guiState

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func (g *guiState) px(v int) uintptr { return uintptr(v * g.dpi / 96) }

func (g *guiState) ctl(class, text string, style uint32, ex uint32, x, y, w, h, id int) uintptr {
	hwnd, _, _ := procCreateWindowExW.Call(uintptr(ex), uintptr(unsafe.Pointer(u16(class))),
		uintptr(unsafe.Pointer(u16(text))), uintptr(wsChild|wsVisible|style),
		g.px(x), g.px(y), g.px(w), g.px(h), g.hwnd, uintptr(id), 0, 0)
	procSendMessageW.Call(hwnd, wmSetFont, g.font, 1)
	return hwnd
}

func getText(h uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(h)
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func setText(h uintptr, s string) {
	procSetWindowTextW.Call(h, uintptr(unsafe.Pointer(u16(s))))
}

func msgBox(text string, flags uintptr) uintptr {
	r, _, _ := procMessageBoxW.Call(g.hwnd, uintptr(unsafe.Pointer(u16(text))),
		uintptr(unsafe.Pointer(u16("Mouse Jiggler"))), flags)
	return r
}

// enableVisualStyles activates a Common Controls v6 manifest at runtime so
// buttons/checkboxes get the modern look without embedding a resource.
func enableVisualStyles() {
	const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
<dependency><dependentAssembly><assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/></dependentAssembly></dependency>
</assembly>`
	_ = os.MkdirAll(appDir(), 0o755)
	p := filepath.Join(appDir(), "gui.manifest")
	if os.WriteFile(p, []byte(manifest), 0o644) != nil {
		return
	}
	ctx := actCtx{lpSource: u16(p)}
	ctx.cbSize = uint32(unsafe.Sizeof(ctx))
	h, _, _ := procCreateActCtxW.Call(uintptr(unsafe.Pointer(&ctx)))
	if h == 0 || h == ^uintptr(0) {
		return
	}
	var cookie uintptr
	procActivateActCtx.Call(h, uintptr(unsafe.Pointer(&cookie)))
}

func (g *guiState) load(c *config) {
	for i, d := range guiDayOrder {
		v := uintptr(0)
		if c.sched.Days[d] {
			v = 1
		}
		procSendMessageW.Call(g.days[i], bmSetCheck, v, 0)
	}
	setText(g.windows, strings.Join(c.windows, "\r\n"))
	setText(g.interval, strconv.Itoa(int(c.interval/time.Second)))
}

func (g *guiState) read() (*config, error) {
	var names []string
	for i, d := range guiDayOrder {
		if r, _, _ := procSendMessageW.Call(g.days[i], bmGetCheck, 0, 0); r == 1 {
			names = append(names, d.String()[:3])
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("pick at least one day")
	}
	secs, err := strconv.Atoi(strings.TrimSpace(getText(g.interval)))
	if err != nil {
		return nil, fmt.Errorf("interval must be a whole number of seconds")
	}
	lines := strings.FieldsFunc(getText(g.windows), func(r rune) bool { return r == '\r' || r == '\n' || r == ',' })
	return newConfig(strings.Join(names, ","), lines, time.Duration(secs)*time.Second)
}

func (g *guiState) refreshStatus() {
	c, err := loadConfig()
	if err != nil {
		setText(g.status, "Saved config error: "+err.Error())
		return
	}
	now := time.Now()
	state := "outside schedule"
	if task := taskStatus(); task != "not installed" && !loopRunning() {
		state = "STOPPED (not running: click Save & Install)"
	} else if u, ok := pausedUntil(now); ok {
		state = "PAUSED until " + u.Format("Mon 15:04")
	} else if c.sched.Active(now) {
		state = "ACTIVE now"
	}
	next := ""
	if s, e, ok := c.sched.Next(now); ok {
		next = fmt.Sprintf("Next window: %s -> %s", s.Format("Mon Jan 2 15:04"), e.Format("15:04"))
	}
	setText(g.status, fmt.Sprintf("Task: %s   |   %s\r\n%s", taskStatus(), state, next))
}

func wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCommand:
		if wParam>>16 != 0 { // only BN_CLICKED (0)
			break
		}
		switch wParam & 0xFFFF {
		case idInstall:
			c, err := g.read()
			if err != nil {
				msgBox(err.Error(), mbIconError)
				return 0
			}
			out, err := install(c)
			g.refreshStatus()
			if err != nil {
				msgBox(err.Error(), mbIconError)
			} else {
				msgBox(out, mbIconInfo)
			}
		case idUninstall:
			if msgBox("Remove the scheduled task?", mbYesNo|mbIconQ) != idYes {
				return 0
			}
			out, err := uninstall()
			g.refreshStatus()
			if err != nil {
				msgBox(err.Error(), mbIconError)
			} else {
				msgBox(out, mbIconInfo)
			}
		case idPause:
			if err := setPause(endOfDay(time.Now())); err != nil {
				msgBox(err.Error(), mbIconError)
			}
			g.refreshStatus()
		case idResume:
			if err := clearPause(); err != nil {
				msgBox(err.Error(), mbIconError)
			}
			g.refreshStatus()
		case idTest:
			if err := jiggle(); err != nil {
				msgBox(err.Error(), mbIconError)
			} else {
				setText(g.status, "Test jiggle sent at "+time.Now().Format("15:04:05")+" (cursor should not have moved).")
			}
		}
		return 0
	case wmTimer:
		g.refreshStatus()
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

func runGUI() error {
	runtime.LockOSThread() // all window calls must stay on one OS thread
	defer runtime.UnlockOSThread()

	procSetProcessDPIAware.Call()
	enableVisualStyles()

	g.dpi = 96
	if hdc, _, _ := procGetDC.Call(0); hdc != 0 {
		if d, _, _ := procGetDeviceCaps.Call(hdc, 90 /* LOGPIXELSY */); d > 0 {
			g.dpi = int(d)
		}
		procReleaseDC.Call(0, hdc)
	}
	fontHeight := -9 * g.dpi / 72 // negative = character height in px
	g.font, _, _ = procCreateFontW.Call(uintptr(fontHeight), 0, 0, 0, 400, 0, 0, 0,
		1 /* DEFAULT_CHARSET */, 0, 0, 5 /* CLEARTYPE_QUALITY */, 0, uintptr(unsafe.Pointer(u16("Segoe UI"))))

	hInst, _, _ := procGetModuleHandleW.Call(0)
	cursor, _, _ := procLoadCursorW.Call(0, 32512 /* IDC_ARROW */)
	icon, _, _ := procLoadIconW.Call(0, 32512 /* IDI_APPLICATION */)
	className := u16("MouseJigglerCLIWindow")
	wc := wndClassEx{
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hInst,
		hIcon:         icon,
		hIconSm:       icon,
		hCursor:       cursor,
		hbrBackground: 15 + 1, // COLOR_BTNFACE + 1
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassEx: %v", err)
	}

	const clientW, clientH = 400, 366
	rc := rect{0, 0, int32(g.px(clientW)), int32(g.px(clientH))}
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&rc)), wsOverlappedWindow, 0)
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(u16("Mouse Jiggler - Schedule"))), wsOverlappedWindow,
		0x80000000, 0x80000000, uintptr(rc.right-rc.left), uintptr(rc.bottom-rc.top), 0, 0, hInst, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowEx: %v", err)
	}
	g.hwnd = hwnd

	g.ctl("STATIC", "Days", 0, 0, 16, 14, 200, 18, 0)
	for i, d := range guiDayOrder {
		style := uint32(bsAutoCheckbox | wsTabStop)
		g.days[i] = g.ctl("BUTTON", d.String()[:3], style, 0, 16+i*53, 34, 52, 22, idDayBase+i)
	}
	g.ctl("STATIC", "Time windows (24h, one per line, e.g. 13:30-17:30)", 0, 0, 16, 68, 370, 18, 0)
	g.windows = g.ctl("EDIT", "", esMultiline|esAutoVScroll|esWantReturn|wsVScroll|wsTabStop, wsExClientEdge, 16, 88, 368, 92, 0)
	g.ctl("STATIC", "Jiggle every (seconds)", 0, 0, 16, 194, 150, 18, 0)
	g.interval = g.ctl("EDIT", "", esAutoHScroll|wsTabStop, wsExClientEdge, 170, 191, 70, 23, 0)

	g.ctl("BUTTON", "Save && Install", bsDefPushButton|wsTabStop, 0, 16, 228, 120, 30, idInstall)
	g.ctl("BUTTON", "Uninstall", bsPushButton|wsTabStop, 0, 144, 228, 110, 30, idUninstall)
	g.ctl("BUTTON", "Test jiggle", bsPushButton|wsTabStop, 0, 262, 228, 122, 30, idTest)
	g.ctl("BUTTON", "Pause rest of today", bsPushButton|wsTabStop, 0, 16, 266, 184, 30, idPause)
	g.ctl("BUTTON", "Resume", bsPushButton|wsTabStop, 0, 208, 266, 176, 30, idResume)
	g.status = g.ctl("STATIC", "", 0, 0, 16, 310, 370, 48, 0)

	c, err := loadConfig()
	if err != nil {
		msgBox(err.Error()+"\n\nUsing defaults.", mbIconError)
		c = defaultConfig()
	}
	g.load(c)
	g.refreshStatus()
	procSetTimer.Call(hwnd, 1, 30000, 0)
	procShowWindow.Call(hwnd, 5 /* SW_SHOW */)

	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return nil
		}
		if d, _, _ := procIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&m))); d != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
