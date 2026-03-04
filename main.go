package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// 第一步：初始化 exe 目录（必须最先执行，后续所有文件操作依赖此路径）
	initExeDir()

	// 初始化日志（依赖 exeDir）
	initLogger()
	defer closeLogger()

	appLog("INFO", fmt.Sprintf("程序启动，工作目录: %s", exeDir))

	// 加载配置（依赖 exeDir）
	loadConfig()

	// 初始化MPV
	initMpv()

	// 获取本机IP
	localIP = getLocalIP()
	if localIP == "" {
		appLog("ERROR", "无法获取本机局域网 IP，请检查网络")
		os.Exit(1)
	}
	appLog("INFO", fmt.Sprintf("本机局域网 IP: %s", localIP))

	// 启动DLNA服务
	startDLNA()

	// 启动系统托盘（在新 goroutine 里，因为 systray.Run 会阻塞）
	go runTray()

	// 等待退出信号
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	cleanup()
}
