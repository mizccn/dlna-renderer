package main

import (
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modU32 = syscall.NewLazyDLL("user32.dll")
	modG32 = syscall.NewLazyDLL("gdi32.dll")
	modK32 = syscall.NewLazyDLL("kernel32.dll")

	procMsgBox        = modU32.NewProc("MessageBoxW")
	procRegClass      = modU32.NewProc("RegisterClassExW")
	procCreateWin     = modU32.NewProc("CreateWindowExW")
	procDefWndProc    = modU32.NewProc("DefWindowProcW")
	procGetMsg        = modU32.NewProc("GetMessageW")
	procTransMsg      = modU32.NewProc("TranslateMessage")
	procDispMsg       = modU32.NewProc("DispatchMessageW")
	procPostQuit      = modU32.NewProc("PostQuitMessage")
	procDestroy       = modU32.NewProc("DestroyWindow")
	procSendMsg       = modU32.NewProc("SendMessageW")
	procSetFocus      = modU32.NewProc("SetFocus")
	procGetTextLen    = modU32.NewProc("GetWindowTextLengthW")
	procGetText       = modU32.NewProc("GetWindowTextW")
	procSetText       = modU32.NewProc("SetWindowTextW")
	procShowWin       = modU32.NewProc("ShowWindow")
	procUpdateWin     = modU32.NewProc("UpdateWindow")
	procLoadCursor    = modU32.NewProc("LoadCursorW")
	procMoveWin       = modU32.NewProc("MoveWindow")
	procGetSysMetrics = modU32.NewProc("GetSystemMetrics")
	procGetModHandle  = modK32.NewProc("GetModuleHandleW")
	procGetStockObj   = modG32.NewProc("GetStockObject")
)

type wndClassExW struct {
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
	ptX     int32
	ptY     int32
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func appHInst() uintptr {
	h, _, _ := procGetModHandle.Call(0)
	return h
}

func defaultFont() uintptr {
	f, _, _ := procGetStockObj.Call(17) // DEFAULT_GUI_FONT
	return f
}

func screenCenterPos(w, h int32) (int32, int32) {
	sw, _, _ := procGetSysMetrics.Call(0)
	sh, _, _ := procGetSysMetrics.Call(1)
	return (int32(sw) - w) / 2, (int32(sh) - h) / 2
}

func runMsgLoop() {
	var m winMsg
	for {
		r, _, _ := procGetMsg.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			return
		}
		procTransMsg.Call(uintptr(unsafe.Pointer(&m)))
		procDispMsg.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func addCtrl(parent uintptr, cls, text string, style uintptr, x, y, w, h int32, id uintptr) uintptr {
	inst := appHInst()
	c, _, _ := procCreateWin.Call(
		0,
		uintptr(unsafe.Pointer(u16(cls))),
		uintptr(unsafe.Pointer(u16(text))),
		0x50000000|style, // WS_CHILD | WS_VISIBLE | style
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, id, inst, 0,
	)
	procSendMsg.Call(c, 0x0030, defaultFont(), 1) // WM_SETFONT
	return c
}

// ══════════════════════════════════════════
//  MessageBox
// ══════════════════════════════════════════

func msgBox(title, text string) {
	procMsgBox.Call(
		0,
		uintptr(unsafe.Pointer(u16(text))),
		uintptr(unsafe.Pointer(u16(title))),
		0x40, // MB_ICONINFORMATION
	)
}

// ══════════════════════════════════════════
//  输入对话框
// ══════════════════════════════════════════

type inputReq struct {
	title, prompt, def string
	ret                chan string
}

var inputCh = make(chan inputReq)

// inputDialogUTF8 从任意 goroutine 调用，发送请求到专用 OS 线程
func inputDialogUTF8(title, prompt, def string) string {
	req := inputReq{
		title:  title,
		prompt: prompt,
		def:    def,
		ret:    make(chan string, 1),
	}
	inputCh <- req
	return <-req.ret
}

// runInputWorker 专用 OS 线程，启动后永久 LockOSThread
func runInputWorker() {
	runtime.LockOSThread()
	for req := range inputCh {
		req.ret <- doInputDialog(req.title, req.prompt, req.def)
	}
}

// 对话框状态（只在 runInputWorker 所在线程访问，无竞争）
var (
	dlgEdit      uintptr
	dlgText      string
	dlgConfirmed bool
	inputClsReg  bool
)

func inputWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case 0x0111: // WM_COMMAND
		switch wp & 0xFFFF {
		case 1: // 确定
			n, _, _ := procGetTextLen.Call(dlgEdit)
			buf := make([]uint16, n+2)
			procGetText.Call(dlgEdit, uintptr(unsafe.Pointer(&buf[0])), n+1)
			dlgText = syscall.UTF16ToString(buf)
			dlgConfirmed = true
			procDestroy.Call(hwnd)
		case 2: // 取消
			dlgConfirmed = false
			procDestroy.Call(hwnd)
		}
		return 0
	case 0x0010: // WM_CLOSE
		dlgConfirmed = false
		procDestroy.Call(hwnd)
		return 0
	case 0x0002: // WM_DESTROY
		procPostQuit.Call(0)
		return 0
	}
	r, _, _ := procDefWndProc.Call(hwnd, msg, wp, lp)
	return r
}

func doInputDialog(title, prompt, def string) string {
	inst := appHInst()
	clsName := u16("_DlnaInputWnd")

	if !inputClsReg {
		cur, _, _ := procLoadCursor.Call(0, 32512)
		wc := wndClassExW{
			lpfnWndProc:   syscall.NewCallback(inputWndProc),
			hInstance:     inst,
			hbrBackground: 6, // COLOR_WINDOW+1
			hCursor:       cur,
			lpszClassName: clsName,
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		procRegClass.Call(uintptr(unsafe.Pointer(&wc)))
		inputClsReg = true
	}

	const (
		WS_OVERLAPPED  = 0x00000000
		WS_CAPTION     = 0x00C00000
		WS_SYSMENU     = 0x00080000
		WS_VISIBLE     = 0x10000000
		WS_EX_TOPMOST  = 0x00000008
		WS_EX_DLGFRAME = 0x00000001
	)

	w, h := int32(440), int32(185)
	x, y := screenCenterPos(w, h)

	hwnd, _, _ := procCreateWin.Call(
		WS_EX_TOPMOST|WS_EX_DLGFRAME,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(u16(title))),
		WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU|WS_VISIBLE,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, inst, 0,
	)
	if hwnd == 0 {
		return ""
	}

	addCtrl(hwnd, "STATIC", prompt, 0, 12, 14, 408, 22, 0)

	// 编辑框单独创建，带 WS_EX_CLIENTEDGE 边框
	dlgEdit, _, _ = procCreateWin.Call(
		0x00000200, // WS_EX_CLIENTEDGE
		uintptr(unsafe.Pointer(u16("EDIT"))),
		uintptr(unsafe.Pointer(u16(def))),
		0x50800080, // WS_CHILD|WS_VISIBLE|WS_BORDER|ES_AUTOHSCROLL
		12, 46, 408, 26,
		hwnd, 0, inst, 0,
	)
	procSendMsg.Call(dlgEdit, 0x0030, defaultFont(), 1)

	addCtrl(hwnd, "BUTTON", "确  定", 0, 118, 105, 88, 30, 1)
	addCtrl(hwnd, "BUTTON", "取  消", 0, 226, 105, 88, 30, 2)

	dlgText, dlgConfirmed = "", false

	procShowWin.Call(hwnd, 5) // SW_SHOW
	procUpdateWin.Call(hwnd)
	procSetFocus.Call(dlgEdit)
	procSendMsg.Call(dlgEdit, 0x00B1, 0x7FFF, 0x7FFF) // EM_SETSEL 全选

	runMsgLoop()

	if dlgConfirmed {
		return dlgText
	}
	return ""
}

// ══════════════════════════════════════════
//  日志窗口
// ══════════════════════════════════════════

type logReq struct{ done chan struct{} }

var logCh = make(chan logReq, 1)

func showLogDialog() {
	req := logReq{done: make(chan struct{})}
	select {
	case logCh <- req:
	default: // 已有日志窗口打开，忽略
	}
}

func runLogWorker() {
	runtime.LockOSThread()
	for range logCh {
		doLogWindow()
	}
}

var (
	logEditCtrl uintptr
	logClsReg   bool
)

func logWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case 0x0005: // WM_SIZE
		cw := int32(lp & 0xFFFF)
		ch := int32((lp >> 16) & 0xFFFF)
		if logEditCtrl != 0 {
			procMoveWin.Call(logEditCtrl, 0, 0, uintptr(cw), uintptr(ch-52), 1)
		}
		return 0
	case 0x0111: // WM_COMMAND
		if wp&0xFFFF == 100 {
			refreshLog()
		}
		return 0
	case 0x0010: // WM_CLOSE
		procDestroy.Call(hwnd)
		return 0
	case 0x0002: // WM_DESTROY
		procPostQuit.Call(0)
		return 0
	}
	r, _, _ := procDefWndProc.Call(hwnd, msg, wp, lp)
	return r
}

func refreshLog() {
	// Win32 EDIT 控件必须用 \r\n 换行
	raw := getLogText()
	txt := strings.ReplaceAll(raw, "\r\n", "\n")
	txt = strings.ReplaceAll(txt, "\n", "\r\n")
	procSetText.Call(logEditCtrl, uintptr(unsafe.Pointer(u16(txt))))
	// 滚动到末尾
	n, _, _ := procGetTextLen.Call(logEditCtrl)
	procSendMsg.Call(logEditCtrl, 0x00B1, n, n) // EM_SETSEL
	procSendMsg.Call(logEditCtrl, 0x00B7, 0, 0) // EM_SCROLLCARET
}

func doLogWindow() {
	inst := appHInst()
	clsName := u16("_DlnaLogWnd")

	if !logClsReg {
		cur, _, _ := procLoadCursor.Call(0, 32512)
		wc := wndClassExW{
			lpfnWndProc:   syscall.NewCallback(logWndProc),
			hInstance:     inst,
			hbrBackground: 6,
			hCursor:       cur,
			lpszClassName: clsName,
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		procRegClass.Call(uintptr(unsafe.Pointer(&wc)))
		logClsReg = true
	}

	w, h := int32(920), int32(660)
	x, y := screenCenterPos(w, h)

	const (
		WS_OVERLAPPEDWINDOW = 0x00CF0000
		WS_VISIBLE          = 0x10000000
		WS_EX_TOPMOST       = 0x00000008
	)

	hwnd, _, _ := procCreateWin.Call(
		WS_EX_TOPMOST,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(u16("DLNA投屏接收器 - 运行日志"))),
		WS_OVERLAPPEDWINDOW|WS_VISIBLE,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, inst, 0,
	)
	if hwnd == 0 {
		return
	}

	const (
		WS_CHILD       = 0x40000000
		WS_VSCROLL     = 0x00200000
		WS_HSCROLL     = 0x00100000
		ES_MULTILINE   = 0x0004
		ES_AUTOVSCROLL = 0x0040
		ES_READONLY    = 0x0800
		WS_EX_CLREDGE  = 0x00000200
	)

	logEditCtrl, _, _ = procCreateWin.Call(
		WS_EX_CLREDGE,
		uintptr(unsafe.Pointer(u16("EDIT"))),
		0,
		WS_CHILD|WS_VISIBLE|WS_VSCROLL|WS_HSCROLL|ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY,
		0, 0, uintptr(w-16), uintptr(h-90),
		hwnd, 0, inst, 0,
	)
	procSendMsg.Call(logEditCtrl, 0x0030, defaultFont(), 1)

	refreshLog()

	bw, bh := int32(120), int32(30)
	btn := addCtrl(hwnd, "BUTTON", "刷 新 日 志",
		0, (w-bw)/2, h-66, bw, bh, 100)
	_ = btn

	procShowWin.Call(hwnd, 5)
	procUpdateWin.Call(hwnd)

	runMsgLoop()
}

func gbkToUTF8(b []byte) string { return string(b) }

// ══════════════════════════════════════════
//  剪贴板
// ══════════════════════════════════════════

var (
	procOpenClipboard   = modU32.NewProc("OpenClipboard")
	procCloseClipboard  = modU32.NewProc("CloseClipboard")
	procEmptyClipboard  = modU32.NewProc("EmptyClipboard")
	procSetClipboard    = modU32.NewProc("SetClipboardData")
	procGlobalAlloc     = modK32.NewProc("GlobalAlloc")
	procGlobalLock      = modK32.NewProc("GlobalLock")
	procGlobalUnlock    = modK32.NewProc("GlobalUnlock")
)

// setClipboard 将文本写入 Windows 剪贴板
func setClipboard(text string) error {
	// 转为 UTF-16
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	byteLen := uintptr(len(utf16) * 2)

	// GMEM_MOVEABLE = 0x0002
	hMem, _, e := procGlobalAlloc.Call(0x0002, byteLen)
	if hMem == 0 {
		return e
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return syscall.EINVAL
	}
	// 把 UTF-16 数据拷贝进去
	dst := (*[1 << 20]uint16)(unsafe.Pointer(ptr))
	copy(dst[:], utf16)
	procGlobalUnlock.Call(hMem)

	r, _, e2 := procOpenClipboard.Call(0)
	if r == 0 {
		return e2
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	// CF_UNICODETEXT = 13
	r2, _, e3 := procSetClipboard.Call(13, hMem)
	if r2 == 0 {
		return e3
	}
	return nil
}
