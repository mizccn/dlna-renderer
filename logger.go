package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	maxLogLines   = 500
	maxLogBytes   = 1 * 1024 * 1024 // 1MB，超过则轮转
	keepLogLines  = 300              // 轮转后保留最新的行数
)

var (
	logMu      sync.Mutex
	logEntries []string
	logFileH   *os.File
)

func initLogger() {
	rotateLogIfNeeded()
	var err error
	logFileH, err = os.OpenFile(logFilePath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法打开日志文件: %v\n", err)
	}
}

// rotateLogIfNeeded 若日志文件超过 maxLogBytes，截断只保留最新的 keepLogLines 行
func rotateLogIfNeeded() {
	path := logFilePath()
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogBytes {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	// 过滤空行
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}

	// 保留最新的 keepLogLines 行
	if len(nonEmpty) > keepLogLines {
		nonEmpty = nonEmpty[len(nonEmpty)-keepLogLines:]
	}

	newContent := strings.Join(nonEmpty, "\n") + "\n"
	// 加旋转标记
	header := fmt.Sprintf("=== 日志轮转于 %s，已清理旧记录 ===\n",
		time.Now().Format("2006-01-02 15:04:05"))
	os.WriteFile(path, []byte(header+newContent), 0644)
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
		// 每写100条检查一次文件大小
		checkLogRotate()
	}
}

var logWriteCount int

func checkLogRotate() {
	logWriteCount++
	if logWriteCount%100 != 0 {
		return
	}
	info, err := os.Stat(logFilePath())
	if err != nil || info.Size() < maxLogBytes {
		return
	}
	// 关闭当前句柄，轮转，重新打开
	logFileH.Close()
	logFileH = nil
	rotateLogIfNeeded()
	var e error
	logFileH, e = os.OpenFile(logFilePath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if e != nil {
		fmt.Fprintf(os.Stderr, "重新打开日志文件失败: %v\n", e)
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
	// 优先从文件读取完整日志（比内存里的更全）
	data, err := os.ReadFile(logFilePath())
	if err == nil && len(data) > 0 {
		return string(data)
	}
	entries := getLogEntries()
	return strings.Join(entries, "\n")
}
