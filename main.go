package main

import (
	"fmt"
	"os"

	"github.com/getlantern/systray"
)

func main() {
	initExeDir()
	initLogger()
	defer closeLogger()

	appLog("INFO", fmt.Sprintf("程序启动，工作目录: %s", exeDir))
	loadConfig()

	localIP = getLocalIP()
	if localIP == "" {
		appLog("ERROR", "无法获取本机局域网 IP，请检查网络")
		os.Exit(1)
	}
	appLog("INFO", fmt.Sprintf("本机局域网 IP: %s", localIP))

	// 启动对话框专用 OS 线程（必须在 systray.Run 之前）
	go runInputWorker()
	go runLogWorker()

	startDLNA()

	// systray.Run 必须在主线程
	systray.Run(onTrayReady, func() {
		cleanup()
		os.Exit(0)
	})
}
