package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const maxLogLines = 500

var (
	logMu      sync.Mutex
	logEntries []string
	logFileH   *os.File
)

func initLogger() {
	// exeDir 必须在此之前已由 initExeDir() 初始化
	var err error
	logFileH, err = os.OpenFile(logFilePath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法打开日志文件: %v\n", err)
	}
}

func closeLogger() {
	if logFileH != nil {
		logFileH.Close()
	}
}

func appLog(level, msg string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	entry := fmt.Sprintf("[%s] [%s] %s", ts, level, msg)

	logMu.Lock()
	logEntries = append(logEntries, entry)
	if len(logEntries) > maxLogLines {
		logEntries = logEntries[len(logEntries)-maxLogLines:]
	}
	logMu.Unlock()

	if logFileH != nil {
		fmt.Fprintln(logFileH, entry)
	}
}

func getLogEntries() []string {
	logMu.Lock()
	defer logMu.Unlock()
	result := make([]string, len(logEntries))
	copy(result, logEntries)
	return result
}

func getLogText() string {
	entries := getLogEntries()
	return strings.Join(entries, "\n")
}
