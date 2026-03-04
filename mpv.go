package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

var (
	mpvCmd     *exec.Cmd
	pipeFile   *os.File
	currentURI string
	receiving  bool = true
)

func initMpv() {
	// MPV 在投屏时才启动
}

// restartMpv 关闭旧实例，重新启动 MPV 并重连 pipe
func restartMpv() error {
	// 关闭旧 pipe
	if pipeFile != nil {
		pipeFile.Close()
		pipeFile = nil
	}
	// 杀掉旧进程
	if mpvCmd != nil && mpvCmd.Process != nil {
		mpvCmd.Process.Kill()
		mpvCmd.Wait()
		mpvCmd = nil
	}

	if _, err := os.Stat(mpvPath); os.IsNotExist(err) {
		return fmt.Errorf("mpv.exe 未找到: %s", mpvPath)
	}

	cmd := exec.Command(mpvPath,
		"--idle=yes",
		"--input-ipc-server="+pipeName,
		"--no-terminal",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 mpv 失败: %v", err)
	}
	mpvCmd = cmd
	appLog("INFO", "mpv 已启动")

	// 等待 pipe 就绪，最多等 10 秒
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

// isMpvAlive 检查 mpv 进程和 pipe 是否仍然有效
func isMpvAlive() bool {
	if mpvCmd == nil || mpvCmd.Process == nil || pipeFile == nil {
		return false
	}
	// 尝试发一个空 ping（发送换行，mpv 会忽略空行）
	pipeFile.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	_, err := pipeFile.Write([]byte("\n"))
	pipeFile.SetWriteDeadline(time.Time{}) // 清除 deadline
	if err != nil {
		appLog("INFO", "检测到 mpv pipe 已断开: "+err.Error())
		return false
	}
	return true
}

// sendMpvJSON 发送命令，pipe 断开时自动重连重试一次
func sendMpvJSON(json string) error {
	if !isMpvAlive() {
		appLog("INFO", "mpv 未运行，尝试重启...")
		if err := restartMpv(); err != nil {
			return err
		}
	}
	_, err := pipeFile.Write([]byte(json + "\n"))
	if err != nil {
		// 第一次失败：重启后再试一次
		appLog("WARN", "mpv 命令发送失败，重启 mpv 重试: "+err.Error())
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
	return sendMpvJSON(jsonCmd)
}

func cleanup() {
	if pipeFile != nil {
		sendMpvJSON(`{"command": ["quit"]}`)
		pipeFile.Close()
		pipeFile = nil
	}
	if mpvCmd != nil && mpvCmd.Process != nil {
		mpvCmd.Process.Kill()
		mpvCmd.Wait()
	}
	appLog("INFO", "程序清理完成")
}