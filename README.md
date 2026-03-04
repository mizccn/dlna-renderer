dlna-renderer/<br>
├── main.go          # 程序入口，初始化与信号处理 | Program entry, initialization and signal handling/<br>
├── config.go        # 配置加载、保存、路径管理 | Configuration loading, saving and path management/<br>
├── dlna.go          # SSDP 发现、HTTP 服务、SOAP 处理 | SSDP discovery, HTTP service, SOAP processing/<br>
├── mpv.go           # MPV 进程管理与 IPC 通信 | MPV process management and IPC communication/<br>
├── tray.go          # 系统托盘、对话框、开机启动 | System tray, dialog boxes, auto-start on boot/<br>
├── logger.go        # 日志记录 | Logging/<br>
├── notify.go        # Windows Toast 通知 | Windows Toast notifications/<br>
├── go.mod           # Go 模块依赖管理 | Go module dependency management/<br>
├── icon.ico         # 托盘图标 | Tray icon/<br>
└── icon.syso        # 编译嵌入的程序图标 | Compiled embedded program icon/<br>
