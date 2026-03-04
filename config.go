package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultMpvPath = `C:\Program Files\mpv\mpv.exe`
	defaultPort    = 9000
	defaultName    = "Go投屏接收器"
	pipeName       = `\\.\pipe\go-dlna-renderer`
	deviceUUID     = "uuid:550e8400-e29b-41d4-a716-446655440000"
	multicastAddr  = "239.255.255.250:1900"
)

var (
	mpvPath      string
	httpPort     int
	friendlyName string
	localIP      string
	exeDir       string // exe 所在目录，所有配置文件都基于此路径
)

// initExeDir 获取 exe 所在目录，必须在所有文件操作之前调用
func initExeDir() {
	exe, err := os.Executable()
	if err != nil {
		// 回退：使用当前工作目录
		exeDir, _ = os.Getwd()
		return
	}
	// os.Executable() 在某些情况下返回的是 symlink，用 EvalSymlinks 解析真实路径
	realExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		realExe = exe
	}
	exeDir = filepath.Dir(realExe)

	// 把工作目录切换到 exe 目录，确保相对路径全部正确
	os.Chdir(exeDir)
}

// exePath 返回基于 exe 目录的文件绝对路径
func exePath(name string) string {
	return filepath.Join(exeDir, name)
}

func configFilePath() string { return exePath("config.ini") }
func logFilePath() string    { return exePath("dlna-renderer.log") }
func iconIcoPath() string    { return exePath("icon.ico") }

func loadConfig() {
	cfgPath := configFilePath()
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		saveDefaultConfig()
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		appLog("ERROR", "读取 config.ini 失败: "+err.Error())
		setDefaults()
		return
	}
	cfg := parseINI(string(data))

	mpvPath = cfg["mpv_path"]
	if mpvPath == "" {
		mpvPath = defaultMpvPath
	}
	if p, err := strconv.Atoi(cfg["http_port"]); err == nil && p > 0 {
		httpPort = p
	} else {
		httpPort = defaultPort
	}
	friendlyName = cfg["friendly_name"]
	if friendlyName == "" {
		friendlyName = defaultName
	}
	appLog("INFO", fmt.Sprintf("配置加载成功: exeDir=%s mpv=%s port=%d name=%s",
		exeDir, mpvPath, httpPort, friendlyName))
}

func saveDefaultConfig() {
	content := fmt.Sprintf("[config]\nmpv_path = %s\nhttp_port = %d\nfriendly_name = %s\n",
		defaultMpvPath, defaultPort, defaultName)
	os.WriteFile(configFilePath(), []byte(content), 0644)
}

func saveConfig() {
	content := fmt.Sprintf("[config]\nmpv_path = %s\nhttp_port = %d\nfriendly_name = %s\n",
		mpvPath, httpPort, friendlyName)
	if err := os.WriteFile(configFilePath(), []byte(content), 0644); err != nil {
		appLog("ERROR", "保存配置失败: "+err.Error())
	} else {
		appLog("INFO", "配置已保存")
	}
}

func setDefaults() {
	mpvPath = defaultMpvPath
	httpPort = defaultPort
	friendlyName = defaultName
}

func parseINI(s string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") ||
			strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return m
}

// getExePath 返回当前 exe 的完整路径（供注册表开机启动使用）
func getExePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		return real
	}
	return abs
}

// openExplorer 打开 exe 所在目录（调试用）
func openExplorer() {
	exec.Command("explorer", exeDir).Start()
}
