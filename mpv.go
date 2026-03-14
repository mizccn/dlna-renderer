package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	mpvCmd       *exec.Cmd
	pipeFile     *os.File
	mpvMu        sync.Mutex
	currentURI   string
	receiving    bool = true
	playStartTime time.Time
)

func initMpv() {}

func restartMpv() error {
	if pipeFile != nil {
		pipeFile.Close()
		pipeFile = nil
	}
	if mpvCmd != nil && mpvCmd.Process != nil {
		mpvCmd.Process.Kill()
		mpvCmd.Wait()
		mpvCmd = nil
	}
	if _, err := os.Stat(mpvPath); os.IsNotExist(err) {
		return fmt.Errorf("mpv.exe 未找到: %s", mpvPath)
	}
	cmd := exec.Command(mpvPath, "--idle=yes", "--input-ipc-server="+pipeName, "--no-terminal")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 mpv 失败: %v", err)
	}
	mpvCmd = cmd
	appLog("INFO", "mpv 已启动")
	var err error
	for i := 0; i < 20; i++ {
		pipeFile, err = os.OpenFile(pipeName, os.O_WRONLY, 0666)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("连接 mpv IPC pipe 失败: %v", err)
	}
	appLog("INFO", "mpv IPC pipe 已连接")
	return nil
}

// isMpvAlive 用 Windows API 检查进程是否存活，避免 pipe deadline 挂起
func isMpvAlive() bool {
	if mpvCmd == nil || mpvCmd.Process == nil || pipeFile == nil {
		return false
	}
	return isProcAliveWin(mpvCmd.Process.Pid)
}

func isProcAliveWin(pid int) bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	waitForSingle := kernel32.NewProc("WaitForSingleObject")
	closeHandle := kernel32.NewProc("CloseHandle")
	handle, _, _ := openProcess.Call(0x1000, 0, uintptr(pid))
	if handle == 0 {
		return false
	}
	defer closeHandle.Call(handle)
	ret, _, _ := waitForSingle.Call(handle, 0)
	return ret == 0x102 // WAIT_TIMEOUT = 进程仍在运行
}

func sendMpvJSON(json string) error {
	mpvMu.Lock()
	defer mpvMu.Unlock()
	if !isMpvAlive() {
		appLog("INFO", "mpv 未运行，尝试重启...")
		if err := restartMpv(); err != nil {
			return err
		}
	}
	_, err := pipeFile.Write([]byte(json + "\n"))
	if err != nil {
		appLog("WARN", "mpv 命令发送失败，重启重试: "+err.Error())
		if err2 := restartMpv(); err2 != nil {
			return fmt.Errorf("重启 mpv 失败: %v", err2)
		}
		_, err3 := pipeFile.Write([]byte(json + "\n"))
		if err3 != nil {
			return fmt.Errorf("重启后仍发送失败: %v", err3)
		}
	}
	appLog("INFO", "mpv 命令: "+json)
	return nil
}

func playURI(uri string) error {
	jsonCmd := fmt.Sprintf(`{"command": ["loadfile", "%s", "replace"]}`,
		strings.ReplaceAll(uri, `"`, `\"`))
	if err := sendMpvJSON(jsonCmd); err != nil {
		return err
	}
	playStartTime = time.Now()
	return nil
}

// getMpvTimePos 返回估算播放进度，让 App 知道播放在进行中
func getMpvTimePos() string {
	if playStartTime.IsZero() {
		return ""
	}
	secs := int(time.Since(playStartTime).Seconds())
	if secs < 0 {
		secs = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d", secs/3600, (secs%3600)/60, secs%60)
}

func cleanup() {
	mpvMu.Lock()
	defer mpvMu.Unlock()
	if pipeFile != nil {
		pipeFile.Write([]byte(`{"command": ["quit"]}` + "\n"))
		pipeFile.Close()
		pipeFile = nil
	}
	if mpvCmd != nil && mpvCmd.Process != nil {
		mpvCmd.Process.Kill()
		mpvCmd.Wait()
	}
	appLog("INFO", "程序清理完成")
}
