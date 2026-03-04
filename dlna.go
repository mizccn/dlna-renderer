package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// transportState 记录当前播放状态，供 GetTransportInfo 返回
// 取值: STOPPED / PLAYING / PAUSED_PLAYBACK / TRANSITIONING
var transportState = "STOPPED"

func startDLNA() {
	// 先发一轮 byebye + alive，让局域网设备刷新
	go func() {
		sendNotifyByebyes()
		time.Sleep(200 * time.Millisecond)
		sendNotifyAlives()
	}()

	// 定时保活
	go func() {
		ticker := time.NewTicker(20 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			sendNotifyAlives()
		}
	}()

	// 监听 M-SEARCH
	go listenSSDP()

	// HTTP 服务
	http.HandleFunc("/description.xml", descriptionHandler)
	http.HandleFunc("/AVTransport/control", avTransportHandler)
	http.HandleFunc("/AVTransport/scpd.xml", scpdHandler)
	http.HandleFunc("/AVTransport/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
	})
	http.HandleFunc("/RenderingControl/control", renderingControlHandler)
	http.HandleFunc("/RenderingControl/scpd.xml", rcScpdHandler)
	http.HandleFunc("/RenderingControl/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
	})
	http.HandleFunc("/ConnectionManager/control", connectionManagerHandler)
	http.HandleFunc("/ConnectionManager/scpd.xml", cmScpdHandler)
	http.HandleFunc("/ConnectionManager/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
	})

	appLog("INFO", fmt.Sprintf("HTTP Renderer 服务启动: http://%s:%d", localIP, httpPort))
	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", httpPort), nil); err != nil {
			appLog("ERROR", "HTTP 服务启动失败: "+err.Error())
		}
	}()
}

func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return strings.Split(conn.LocalAddr().String(), ":")[0]
}

// ssdpTypes 返回所有需要通告的 NT 类型
func ssdpTypes() []string {
	return []string{
		"upnp:rootdevice",
		deviceUUID,
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"urn:schemas-upnp-org:service:AVTransport:1",
		"urn:schemas-upnp-org:service:RenderingControl:1",
		"urn:schemas-upnp-org:service:ConnectionManager:1",
	}
}

func buildUSN(nt string) string {
	switch nt {
	case "upnp:rootdevice":
		return deviceUUID + "::upnp:rootdevice"
	case deviceUUID:
		return deviceUUID
	default:
		return deviceUUID + "::" + nt
	}
}

// sendNotifyAlives 向多播组发送 ssdp:alive
func sendNotifyAlives() {
	location := fmt.Sprintf("http://%s:%d/description.xml", localIP, httpPort)
	server := "Microsoft-Windows-NT/10.0 UPnP/1.0 GoRenderer/1.0"

	for _, nt := range ssdpTypes() {
		msg := fmt.Sprintf(
			"NOTIFY * HTTP/1.1\r\n"+
				"HOST: 239.255.255.250:1900\r\n"+
				"CACHE-CONTROL: max-age=1800\r\n"+
				"LOCATION: %s\r\n"+
				"NT: %s\r\n"+
				"NTS: ssdp:alive\r\n"+
				"SERVER: %s\r\n"+
				"USN: %s\r\n"+
				"\r\n",
			location, nt, server, buildUSN(nt),
		)
		sendMulticast(msg)
		time.Sleep(50 * time.Millisecond)
	}
	appLog("INFO", "已发送 NOTIFY alive")
}

// sendNotifyByebyes 发送 ssdp:byebye，让客户端清除旧缓存
func sendNotifyByebyes() {
	for _, nt := range ssdpTypes() {
		msg := fmt.Sprintf(
			"NOTIFY * HTTP/1.1\r\n"+
				"HOST: 239.255.255.250:1900\r\n"+
				"NT: %s\r\n"+
				"NTS: ssdp:byebye\r\n"+
				"USN: %s\r\n"+
				"\r\n",
			nt, buildUSN(nt),
		)
		sendMulticast(msg)
		time.Sleep(30 * time.Millisecond)
	}
	appLog("INFO", "已发送 NOTIFY byebye")
}

// sendMulticast 发送 UDP 多播包，绑定本地 IP 确保走正确网卡
func sendMulticast(msg string) {
	localAddr := &net.UDPAddr{IP: net.ParseIP(localIP), Port: 0}
	remoteAddr := &net.UDPAddr{IP: net.ParseIP("239.255.255.250"), Port: 1900}
	conn, err := net.DialUDP("udp", localAddr, remoteAddr)
	if err != nil {
		appLog("ERROR", "发送多播失败(dial): "+err.Error())
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	conn.Write([]byte(msg))
}

// listenSSDP 正确加入多播组接收 M-SEARCH
func listenSSDP() {
	// 解析多播组地址
	groupAddr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		appLog("ERROR", "解析多播地址失败: "+err.Error())
		return
	}

	// 找到本机绑定的网络接口
	iface := getInterfaceByIP(localIP)

	var conn *net.UDPConn
	if iface != nil {
		conn, err = net.ListenMulticastUDP("udp4", iface, groupAddr)
	} else {
		conn, err = net.ListenMulticastUDP("udp4", nil, groupAddr)
	}
	if err != nil {
		// 回退：直接监听 1900
		appLog("WARN", "加入多播组失败，回退监听 0.0.0.0:1900: "+err.Error())
		listenSSDPFallback()
		return
	}
	defer conn.Close()

	// 增大接收缓冲区
	conn.SetReadBuffer(65535)

	appLog("INFO", fmt.Sprintf("已加入 SSDP 多播组 (iface: %v)", ifaceName(iface)))

	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			appLog("WARN", "SSDP 读取错误: "+err.Error())
			time.Sleep(500 * time.Millisecond)
			continue
		}
		go handleSSDPPacket(conn, src, string(buf[:n]))
	}
}

// listenSSDPFallback 监听所有接口的 1900 端口（备用）
func listenSSDPFallback() {
	addr, _ := net.ResolveUDPAddr("udp4", "0.0.0.0:1900")
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		appLog("ERROR", "监听 SSDP 1900 端口失败: "+err.Error())
		return
	}
	defer conn.Close()
	appLog("INFO", "SSDP fallback: 监听 0.0.0.0:1900")

	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		go handleSSDPPacket(conn, src, string(buf[:n]))
	}
}

func handleSSDPPacket(conn *net.UDPConn, src *net.UDPAddr, msg string) {
	if !strings.HasPrefix(msg, "M-SEARCH * HTTP/1.1") {
		return
	}

	st := getHeader(msg, "ST")
	man := getHeader(msg, "MAN")

	// MAN 头去掉引号后判断
	manClean := strings.Trim(man, `"`)
	if manClean != "ssdp:discover" {
		return
	}

	appLog("INFO", fmt.Sprintf("收到 M-SEARCH from %s | ST: %s", src, st))

	switch st {
	case "ssdp:all":
		// 全部类型都响应
		for _, nt := range ssdpTypes() {
			sendMSearchResponse(conn, src, nt)
			time.Sleep(30 * time.Millisecond)
		}
	case "upnp:rootdevice",
		deviceUUID,
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"urn:schemas-upnp-org:service:AVTransport:1",
		"urn:schemas-upnp-org:service:RenderingControl:1",
		"urn:schemas-upnp-org:service:ConnectionManager:1":
		sendMSearchResponse(conn, src, st)
	default:
		// 不响应
	}
}

func getHeader(msg, key string) string {
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(strings.ToUpper(line), strings.ToUpper(key)+":") {
			return strings.TrimSpace(line[len(key)+1:])
		}
	}
	return ""
}

func sendMSearchResponse(conn *net.UDPConn, to *net.UDPAddr, st string) {
	location := fmt.Sprintf("http://%s:%d/description.xml", localIP, httpPort)
	response := fmt.Sprintf(
		"HTTP/1.1 200 OK\r\n"+
			"CACHE-CONTROL: max-age=1800\r\n"+
			"DATE: %s\r\n"+
			"EXT:\r\n"+
			"LOCATION: %s\r\n"+
			"SERVER: Microsoft-Windows-NT/10.0 UPnP/1.0 GoRenderer/1.0\r\n"+
			"ST: %s\r\n"+
			"USN: %s\r\n"+
			"\r\n",
		time.Now().UTC().Format(http.TimeFormat),
		location, st, buildUSN(st),
	)

	// 单播回复给发起方
	_, err := conn.WriteToUDP([]byte(response), to)
	if err != nil {
		// 某些系统 ListenMulticastUDP 的 conn 不能用 WriteToUDP，改用 DialUDP 单播
		uc, e2 := net.DialUDP("udp4", nil, to)
		if e2 == nil {
			uc.Write([]byte(response))
			uc.Close()
		} else {
			appLog("ERROR", fmt.Sprintf("回复 M-SEARCH 失败: %v / %v", err, e2))
			return
		}
	}
	appLog("INFO", fmt.Sprintf("已回复 M-SEARCH → %s | ST: %s", to, st))
}

// getInterfaceByIP 找到拥有指定 IP 的网络接口
func getInterfaceByIP(ip string) *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ipStr string
			switch v := addr.(type) {
			case *net.IPNet:
				ipStr = v.IP.String()
			case *net.IPAddr:
				ipStr = v.IP.String()
			}
			if ipStr == ip {
				ifaceCopy := iface
				return &ifaceCopy
			}
		}
	}
	return nil
}

func ifaceName(iface *net.Interface) string {
	if iface == nil {
		return "nil(default)"
	}
	return iface.Name
}

// ========== HTTP Handlers ==========

// scpdHandler 返回 AVTransport 服务描述文档，部分 App 会请求此文件
func scpdHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<scpd xmlns="urn:schemas-upnp-org:service-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <actionList>
    <action><name>SetAVTransportURI</name>
      <argumentList>
        <argument><name>InstanceID</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
        <argument><name>CurrentURI</name><direction>in</direction><relatedStateVariable>AVTransportURI</relatedStateVariable></argument>
        <argument><name>CurrentURIMetaData</name><direction>in</direction><relatedStateVariable>AVTransportURIMetaData</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><name>Play</name>
      <argumentList>
        <argument><name>InstanceID</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
        <argument><name>Speed</name><direction>in</direction><relatedStateVariable>TransportPlaySpeed</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><name>Stop</name>
      <argumentList>
        <argument><name>InstanceID</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><name>Pause</name>
      <argumentList>
        <argument><name>InstanceID</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><name>GetTransportInfo</name>
      <argumentList>
        <argument><name>InstanceID</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
        <argument><name>CurrentTransportState</name><direction>out</direction><relatedStateVariable>TransportState</relatedStateVariable></argument>
        <argument><name>CurrentTransportStatus</name><direction>out</direction><relatedStateVariable>TransportStatus</relatedStateVariable></argument>
        <argument><name>CurrentSpeed</name><direction>out</direction><relatedStateVariable>TransportPlaySpeed</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><name>GetPositionInfo</name>
      <argumentList>
        <argument><name>InstanceID</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
        <argument><name>Track</name><direction>out</direction><relatedStateVariable>CurrentTrack</relatedStateVariable></argument>
        <argument><name>TrackDuration</name><direction>out</direction><relatedStateVariable>CurrentTrackDuration</relatedStateVariable></argument>
        <argument><name>TrackMetaData</name><direction>out</direction><relatedStateVariable>CurrentTrackMetaData</relatedStateVariable></argument>
        <argument><name>TrackURI</name><direction>out</direction><relatedStateVariable>CurrentTrackURI</relatedStateVariable></argument>
        <argument><name>RelTime</name><direction>out</direction><relatedStateVariable>RelativeTimePosition</relatedStateVariable></argument>
        <argument><name>AbsTime</name><direction>out</direction><relatedStateVariable>AbsoluteTimePosition</relatedStateVariable></argument>
        <argument><name>RelCount</name><direction>out</direction><relatedStateVariable>RelativeCounterPosition</relatedStateVariable></argument>
        <argument><name>AbsCount</name><direction>out</direction><relatedStateVariable>AbsoluteCounterPosition</relatedStateVariable></argument>
      </argumentList>
    </action>
  </actionList>
  <serviceStateTable>
    <stateVariable><name>TransportState</name><sendEventsAttribute>yes</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>TransportStatus</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>TransportPlaySpeed</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>AVTransportURI</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>AVTransportURIMetaData</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>CurrentTrack</name><sendEventsAttribute>no</sendEventsAttribute><dataType>ui4</dataType></stateVariable>
    <stateVariable><name>CurrentTrackDuration</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>CurrentTrackMetaData</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>CurrentTrackURI</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>RelativeTimePosition</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>AbsoluteTimePosition</name><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><name>RelativeCounterPosition</name><sendEventsAttribute>no</sendEventsAttribute><dataType>i4</dataType></stateVariable>
    <stateVariable><name>AbsoluteCounterPosition</name><sendEventsAttribute>no</sendEventsAttribute><dataType>i4</dataType></stateVariable>
    <stateVariable><name>A_ARG_TYPE_InstanceID</name><sendEventsAttribute>no</sendEventsAttribute><dataType>ui4</dataType></stateVariable>
  </serviceStateTable>
</scpd>`)
}

func descriptionHandler(w http.ResponseWriter, r *http.Request) {
	xmlContent := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>%s</friendlyName>
    <manufacturer>CustomDLNA</manufacturer>
    <modelName>Go MPV Renderer</modelName>
    <UDN>%s</UDN>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
        <controlURL>/AVTransport/control</controlURL>
        <eventSubURL>/AVTransport/event</eventSubURL>
        <SCPDURL>/AVTransport/scpd.xml</SCPDURL>
      </service>
      <service>
        <serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
        <controlURL>/RenderingControl/control</controlURL>
        <eventSubURL>/RenderingControl/event</eventSubURL>
        <SCPDURL>/RenderingControl/scpd.xml</SCPDURL>
      </service>
      <service>
        <serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId>
        <controlURL>/ConnectionManager/control</controlURL>
        <eventSubURL>/ConnectionManager/event</eventSubURL>
        <SCPDURL>/ConnectionManager/scpd.xml</SCPDURL>
      </service>
    </serviceList>
  </device>
</root>`, friendlyName, deviceUUID)
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, xmlContent)
	appLog("INFO", fmt.Sprintf("返回 description.xml 给 %s", r.RemoteAddr))
}

func avTransportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !receiving {
		appLog("INFO", "投屏已暂停，拒绝请求")
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	body, _ := io.ReadAll(r.Body)
	bodyStr := string(body)

	// 从 SOAPACTION header 取动作名
	soapAction := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
	action := ""
	if idx := strings.LastIndex(soapAction, "#"); idx > 0 {
		action = soapAction[idx+1:]
	}
	// 兜底：从 body 里匹配动作名（部分 App 不带 header）
	if action == "" {
		action = extractSOAPAction(bodyStr)
	}

	appLog("INFO", fmt.Sprintf("[SOAP] action=%s from %s", action, r.RemoteAddr))

	var resp string
	switch action {
	case "SetAVTransportURI":
		uri := extractXMLValue(bodyStr, "CurrentURI")
		if uri == "" {
			// 备用标签名
			uri = extractXMLValue(bodyStr, "CurrentURIMetaData")
		}
		if uri != "" {
			currentURI = uri
			appLog("INFO", fmt.Sprintf("[SetURI] %s", currentURI))
		} else {
			appLog("WARN", "[SetURI] 未能解析 CurrentURI，body: "+bodyStr)
		}
		// 无论是否解析到 URI，都返回成功，让 App 继续发 Play
		resp = soapResp("SetAVTransportURIResponse")

	case "Play":
		if currentURI == "" {
			errMsg := "没有待播放的 URI，请先投屏"
			appLog("ERROR", errMsg)
			showNotification("投屏失败", errMsg)
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		appLog("INFO", fmt.Sprintf("[Play] %s", currentURI))
		if err := playURI(currentURI); err != nil {
			appLog("ERROR", "播放失败: "+err.Error())
			showNotification("投屏失败", err.Error())
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		transportState = "PLAYING"
		resp = soapResp("PlayResponse")

	case "Stop":
		appLog("INFO", "[Stop] 收到停止命令")
		sendMpvJSON(`{"command": ["stop"]}`)
		transportState = "STOPPED"
		currentURI = ""
		resp = soapResp("StopResponse")

	case "Pause":
		appLog("INFO", "[Pause] 收到暂停命令")
		sendMpvJSON(`{"command": ["cycle", "pause"]}`)
		if transportState == "PLAYING" {
			transportState = "PAUSED_PLAYBACK"
		} else {
			transportState = "PLAYING"
		}
		resp = soapResp("PauseResponse")

	case "GetTransportInfo":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
			`<CurrentTransportState>` + transportState + `</CurrentTransportState>` +
			`<CurrentTransportStatus>OK</CurrentTransportStatus>` +
			`<CurrentSpeed>1</CurrentSpeed>` +
			`</u:GetTransportInfoResponse></s:Body></s:Envelope>`

	case "GetCurrentTransportActions":
		actions := "Play,Stop"
		if transportState == "PLAYING" {
			actions = "Pause,Stop,Seek"
		} else if transportState == "PAUSED_PLAYBACK" {
			actions = "Play,Stop,Seek"
		}
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetCurrentTransportActionsResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
			`<Actions>` + actions + `</Actions>` +
			`</u:GetCurrentTransportActionsResponse></s:Body></s:Envelope>`

	case "GetPositionInfo":
		trackURI := currentURI
		track := "1"
		if transportState == "STOPPED" {
			trackURI = ""
			track = "0"
		}
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetPositionInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
			`<Track>` + track + `</Track><TrackDuration>00:00:00</TrackDuration>` +
			`<TrackMetaData></TrackMetaData><TrackURI>` + trackURI + `</TrackURI>` +
			`<RelTime>00:00:00</RelTime><AbsTime>00:00:00</AbsTime>` +
			`<RelCount>0</RelCount><AbsCount>0</AbsCount>` +
			`</u:GetPositionInfoResponse></s:Body></s:Envelope>`

	default:
		// 未知动作：记录日志但返回成功，避免 App 因报错中断流程
		appLog("WARN", fmt.Sprintf("[SOAP] 未支持的动作: %s，返回空成功响应", action))
		resp = soapResp(action + "Response")
	}

	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, resp)
}

// soapResp 生成标准 AVTransport SOAP 成功响应
func soapResp(actionResp string) string {
	return fmt.Sprintf(
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:%s xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"/></s:Body></s:Envelope>`,
		actionResp)
}

// extractXMLValue 用字符串匹配从 SOAP body 里提取标签值
// 兼容带 namespace 前缀的标签，如 <u:CurrentURI> 和 <CurrentURI>
// 并自动反转义 XML 实体（&amp; → & 等）
func extractXMLValue(body, tag string) string {
	patterns := []string{
		"<" + tag + ">",
		":" + tag + ">",
	}
	for _, open := range patterns {
		start := strings.Index(body, open)
		if start == -1 {
			continue
		}
		start += len(open)
		end := strings.Index(body[start:], "</")
		if end == -1 {
			continue
		}
		val := strings.TrimSpace(body[start : start+end])
		if val != "" {
			return xmlUnescape(val)
		}
	}
	return ""
}

// xmlUnescape 反转义常见 XML 实体
func xmlUnescape(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&apos;", "'")
	return s
}

// extractSOAPAction 从 SOAP body 中提取动作名（兜底方案）
func extractSOAPAction(body string) string {
	// SOAP body 里动作元素形如 <u:Play ...> 或 <Play ...>
	actions := []string{
		"SetAVTransportURI", "Play", "Stop", "Pause",
		"GetTransportInfo", "GetPositionInfo", "GetMediaInfo",
		"Seek", "Next", "Previous", "SetPlayMode",
	}
	for _, a := range actions {
		if strings.Contains(body, a) {
			return a
		}
	}
	return ""
}

// ========== RenderingControl ==========

func renderingControlHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	bodyStr := string(body)
	action := extractSOAPAction(bodyStr)
	if action == "" {
		soapAction := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
		if idx := strings.LastIndex(soapAction, "#"); idx > 0 {
			action = soapAction[idx+1:]
		}
	}
	appLog("INFO", fmt.Sprintf("[RC] action=%s", action))

	var resp string
	switch action {
	case "GetVolume":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetVolumeResponse xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">` +
			`<CurrentVolume>50</CurrentVolume>` +
			`</u:GetVolumeResponse></s:Body></s:Envelope>`
	case "SetVolume":
		// 可以后续扩展控制 mpv 音量
		resp = rcSoapResp("SetVolumeResponse")
	case "GetMute":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetMuteResponse xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">` +
			`<CurrentMute>0</CurrentMute>` +
			`</u:GetMuteResponse></s:Body></s:Envelope>`
	case "SetMute":
		resp = rcSoapResp("SetMuteResponse")
	default:
		resp = rcSoapResp(action + "Response")
	}
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, resp)
}

func rcSoapResp(actionResp string) string {
	return fmt.Sprintf(
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`+
			`<s:Body><u:%s xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"/></s:Body></s:Envelope>`,
		actionResp)
}

func rcScpdHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<scpd xmlns="urn:schemas-upnp-org:service-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <actionList>
    <action><n>GetVolume</n>
      <argumentList>
        <argument><n>InstanceID</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
        <argument><n>Channel</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_Channel</relatedStateVariable></argument>
        <argument><n>CurrentVolume</n><direction>out</direction><relatedStateVariable>Volume</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><n>SetVolume</n>
      <argumentList>
        <argument><n>InstanceID</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
        <argument><n>Channel</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_Channel</relatedStateVariable></argument>
        <argument><n>DesiredVolume</n><direction>in</direction><relatedStateVariable>Volume</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><n>GetMute</n>
      <argumentList>
        <argument><n>InstanceID</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
        <argument><n>Channel</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_Channel</relatedStateVariable></argument>
        <argument><n>CurrentMute</n><direction>out</direction><relatedStateVariable>Mute</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><n>SetMute</n>
      <argumentList>
        <argument><n>InstanceID</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_InstanceID</relatedStateVariable></argument>
        <argument><n>Channel</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_Channel</relatedStateVariable></argument>
        <argument><n>DesiredMute</n><direction>in</direction><relatedStateVariable>Mute</relatedStateVariable></argument>
      </argumentList>
    </action>
  </actionList>
  <serviceStateTable>
    <stateVariable><n>Volume</n><sendEventsAttribute>yes</sendEventsAttribute><dataType>ui2</dataType><allowedValueRange><minimum>0</minimum><maximum>100</maximum><step>1</step></allowedValueRange></stateVariable>
    <stateVariable><n>Mute</n><sendEventsAttribute>yes</sendEventsAttribute><dataType>boolean</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_InstanceID</n><sendEventsAttribute>no</sendEventsAttribute><dataType>ui4</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_Channel</n><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
  </serviceStateTable>
</scpd>`)
}

// ========== ConnectionManager ==========

func connectionManagerHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	bodyStr := string(body)
	action := extractSOAPAction(bodyStr)
	if action == "" {
		soapAction := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
		if idx := strings.LastIndex(soapAction, "#"); idx > 0 {
			action = soapAction[idx+1:]
		}
	}
	appLog("INFO", fmt.Sprintf("[CM] action=%s", action))

	var resp string
	switch action {
	case "GetProtocolInfo":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetProtocolInfoResponse xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1">` +
			`<Source></Source>` +
			`<Sink>http-get:*:video/mp4:*,http-get:*:video/mpeg:*,http-get:*:video/x-msvideo:*,` +
			`http-get:*:video/x-matroska:*,http-get:*:video/x-flv:*,http-get:*:video/quicktime:*,` +
			`http-get:*:video/x-ms-wmv:*,http-get:*:audio/mpeg:*,http-get:*:audio/mp4:*,` +
			`http-get:*:audio/x-flac:*,http-get:*:audio/wav:*,http-get:*:image/jpeg:*,http-get:*:*:*</Sink>` +
			`</u:GetProtocolInfoResponse></s:Body></s:Envelope>`
	case "GetCurrentConnectionIDs":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetCurrentConnectionIDsResponse xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1">` +
			`<ConnectionIDs>0</ConnectionIDs>` +
			`</u:GetCurrentConnectionIDsResponse></s:Body></s:Envelope>`
	case "GetCurrentConnectionInfo":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetCurrentConnectionInfoResponse xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1">` +
			`<RcsID>0</RcsID><AVTransportID>0</AVTransportID><ProtocolInfo></ProtocolInfo>` +
			`<PeerConnectionManager></PeerConnectionManager><PeerConnectionID>-1</PeerConnectionID>` +
			`<Direction>Input</Direction><Status>OK</Status>` +
			`</u:GetCurrentConnectionInfoResponse></s:Body></s:Envelope>`
	default:
		resp = fmt.Sprintf(
			`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`+
				`<s:Body><u:%sResponse xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1"/></s:Body></s:Envelope>`,
			action)
	}
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, resp)
}

func cmScpdHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<scpd xmlns="urn:schemas-upnp-org:service-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <actionList>
    <action><n>GetProtocolInfo</n>
      <argumentList>
        <argument><n>Source</n><direction>out</direction><relatedStateVariable>SourceProtocolInfo</relatedStateVariable></argument>
        <argument><n>Sink</n><direction>out</direction><relatedStateVariable>SinkProtocolInfo</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><n>GetCurrentConnectionIDs</n>
      <argumentList>
        <argument><n>ConnectionIDs</n><direction>out</direction><relatedStateVariable>CurrentConnectionIDs</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action><n>GetCurrentConnectionInfo</n>
      <argumentList>
        <argument><n>ConnectionID</n><direction>in</direction><relatedStateVariable>A_ARG_TYPE_ConnectionID</relatedStateVariable></argument>
        <argument><n>RcsID</n><direction>out</direction><relatedStateVariable>A_ARG_TYPE_RcsID</relatedStateVariable></argument>
        <argument><n>AVTransportID</n><direction>out</direction><relatedStateVariable>A_ARG_TYPE_AVTransportID</relatedStateVariable></argument>
        <argument><n>ProtocolInfo</n><direction>out</direction><relatedStateVariable>A_ARG_TYPE_ProtocolInfo</relatedStateVariable></argument>
        <argument><n>PeerConnectionManager</n><direction>out</direction><relatedStateVariable>A_ARG_TYPE_ConnectionManager</relatedStateVariable></argument>
        <argument><n>PeerConnectionID</n><direction>out</direction><relatedStateVariable>A_ARG_TYPE_ConnectionID</relatedStateVariable></argument>
        <argument><n>Direction</n><direction>out</direction><relatedStateVariable>A_ARG_TYPE_Direction</relatedStateVariable></argument>
        <argument><n>Status</n><direction>out</direction><relatedStateVariable>A_ARG_TYPE_ConnectionStatus</relatedStateVariable></argument>
      </argumentList>
    </action>
  </actionList>
  <serviceStateTable>
    <stateVariable><n>SourceProtocolInfo</n><sendEventsAttribute>yes</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><n>SinkProtocolInfo</n><sendEventsAttribute>yes</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><n>CurrentConnectionIDs</n><sendEventsAttribute>yes</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_ConnectionID</n><sendEventsAttribute>no</sendEventsAttribute><dataType>i4</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_RcsID</n><sendEventsAttribute>no</sendEventsAttribute><dataType>i4</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_AVTransportID</n><sendEventsAttribute>no</sendEventsAttribute><dataType>i4</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_ProtocolInfo</n><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_ConnectionManager</n><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_Direction</n><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
    <stateVariable><n>A_ARG_TYPE_ConnectionStatus</n><sendEventsAttribute>no</sendEventsAttribute><dataType>string</dataType></stateVariable>
  </serviceStateTable>
</scpd>`)
}
