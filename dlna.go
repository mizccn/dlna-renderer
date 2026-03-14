package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var transportState = "STOPPED"

func startDLNA() {
	go sendNotifyByebyes()
	time.Sleep(100 * time.Millisecond)
	go sendNotifyAlives()
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			sendNotifyAlives()
		}
	}()
	go listenSSDP()

	http.HandleFunc("/description.xml", descriptionHandler)
	http.HandleFunc("/AVTransport/control", avTransportHandler)
	http.HandleFunc("/AVTransport/scpd.xml", scpdHandler)
	http.HandleFunc("/AVTransport/event", eventHandler)
	http.HandleFunc("/RenderingControl/control", renderingControlHandler)
	http.HandleFunc("/RenderingControl/scpd.xml", rcScpdHandler)
	http.HandleFunc("/RenderingControl/event", eventHandler)
	http.HandleFunc("/ConnectionManager/control", connectionManagerHandler)
	http.HandleFunc("/ConnectionManager/scpd.xml", cmScpdHandler)
	http.HandleFunc("/ConnectionManager/event", eventHandler)

	appLog("INFO", fmt.Sprintf("HTTP 服务启动: http://%s:%d", localIP, httpPort))
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
		conn, _ := net.Dial("udp", multicastAddr)
		if conn != nil {
			conn.Write([]byte(msg))
			conn.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	appLog("INFO", "已发送 NOTIFY alive")
}

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
		conn, _ := net.Dial("udp", multicastAddr)
		if conn != nil {
			conn.Write([]byte(msg))
			conn.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func listenSSDP() {
	iface := getInterfaceByIP(localIP)
	maddr, _ := net.ResolveUDPAddr("udp4", multicastAddr)
	var conn *net.UDPConn
	var err error
	if iface != nil {
		conn, err = net.ListenMulticastUDP("udp4", iface, maddr)
	}
	if conn == nil {
		addr, _ := net.ResolveUDPAddr("udp4", "0.0.0.0:1900")
		conn, err = net.ListenUDP("udp4", addr)
		if err != nil {
			conn, err = net.ListenMulticastUDP("udp4", nil, maddr)
			if err != nil {
				appLog("ERROR", "监听 SSDP 失败: "+err.Error())
				return
			}
		}
	}
	defer conn.Close()
	appLog("INFO", "已监听 SSDP 多播 (1900)")
	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		go handleSSDPPacket(conn, src, string(buf[:n]))
	}
}

func getInterfaceByIP(ip string) *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if strings.Contains(addr.String(), ip) {
				return &iface
			}
		}
	}
	return nil
}

func handleSSDPPacket(conn *net.UDPConn, src *net.UDPAddr, msg string) {
	if !strings.HasPrefix(msg, "M-SEARCH * HTTP/1.1") {
		return
	}
	st := getHeader(msg, "ST")
	man := getHeader(msg, "MAN")
	if strings.Trim(man, `"`) != "ssdp:discover" {
		return
	}
	appLog("INFO", fmt.Sprintf("收到 M-SEARCH from %s | ST: %s", src, st))
	switch st {
	case "ssdp:all":
		for _, nt := range ssdpTypes() {
			sendMSearchResponse(conn, src, nt)
			time.Sleep(30 * time.Millisecond)
		}
	case "upnp:rootdevice", deviceUUID,
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"urn:schemas-upnp-org:service:AVTransport:1",
		"urn:schemas-upnp-org:service:RenderingControl:1",
		"urn:schemas-upnp-org:service:ConnectionManager:1":
		sendMSearchResponse(conn, src, st)
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
	_, err := conn.WriteToUDP([]byte(response), to)
	if err != nil {
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

func descriptionHandler(w http.ResponseWriter, r *http.Request) {
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
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
	fmt.Fprint(w, xml)
	appLog("INFO", fmt.Sprintf("返回 description.xml 给 %s", r.RemoteAddr))
}

func eventHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func soapResp(action string) string {
	return `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body><u:` + action + `Response xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"/></s:Body></s:Envelope>`
}

func extractXMLValue(body, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(body, open)
	if start < 0 {
		open = "<" + tag + " "
		start = strings.Index(body, open)
		if start < 0 {
			return ""
		}
		gtIdx := strings.Index(body[start:], ">")
		if gtIdx < 0 {
			return ""
		}
		start += gtIdx + 1
	} else {
		start += len(open)
	}
	end := strings.Index(body[start:], close)
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}

func xmlUnescape(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&apos;", "'")
	return s
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

func avTransportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !receiving {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	body, _ := io.ReadAll(r.Body)
	bodyStr := string(body)

	soapAction := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
	action := ""
	if idx := strings.LastIndex(soapAction, "#"); idx >= 0 {
		action = soapAction[idx+1:]
	}
	appLog("INFO", fmt.Sprintf("[SOAP] action=%s from %s", action, r.RemoteAddr))

	var resp string
	switch action {
	case "SetAVTransportURI":
		uri := xmlUnescape(extractXMLValue(bodyStr, "CurrentURI"))
		if uri != "" {
			currentURI = uri
			appLog("INFO", fmt.Sprintf("[SetURI] %s", currentURI))
		} else {
			appLog("WARN", "[SetURI] 未能解析 CurrentURI，body: "+bodyStr)
		}
		resp = soapResp("SetAVTransportURI")

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
		resp = soapResp("Play")

	case "Stop":
		appLog("INFO", "[Stop] 收到停止命令")
		sendMpvJSON(`{"command": ["stop"]}`)
		transportState = "STOPPED"
		currentURI = ""
		playStartTime = time.Time{}
		resp = soapResp("Stop")

	case "Pause":
		appLog("INFO", "[Pause] 收到暂停命令")
		sendMpvJSON(`{"command": ["cycle", "pause"]}`)
		if transportState == "PLAYING" {
			transportState = "PAUSED_PLAYBACK"
		} else {
			transportState = "PLAYING"
		}
		resp = soapResp("Pause")

	case "Seek":
		resp = soapResp("Seek")

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
		relTime := "00:00:01"
		if transportState == "STOPPED" {
			trackURI = ""
			track = "0"
			relTime = "00:00:00"
		} else if t := getMpvTimePos(); t != "" {
			relTime = t
		}
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetPositionInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
			`<Track>` + track + `</Track><TrackDuration>00:30:00</TrackDuration>` +
			`<TrackMetaData></TrackMetaData><TrackURI>` + xmlEscape(trackURI) + `</TrackURI>` +
			`<RelTime>` + relTime + `</RelTime><AbsTime>` + relTime + `</AbsTime>` +
			`<RelCount>0</RelCount><AbsCount>0</AbsCount>` +
			`</u:GetPositionInfoResponse></s:Body></s:Envelope>`

	case "GetMediaInfo":
		nrTracks := "1"
		mediaURI := currentURI
		if transportState == "STOPPED" {
			nrTracks = "0"
			mediaURI = ""
		}
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetMediaInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
			`<NrTracks>` + nrTracks + `</NrTracks>` +
			`<MediaDuration>00:30:00</MediaDuration>` +
			`<CurrentURI>` + xmlEscape(mediaURI) + `</CurrentURI>` +
			`<CurrentURIMetaData></CurrentURIMetaData>` +
			`<NextURI></NextURI><NextURIMetaData></NextURIMetaData>` +
			`<PlayMedium>NETWORK</PlayMedium>` +
			`<RecordMedium>NOT_IMPLEMENTED</RecordMedium>` +
			`<WriteStatus>NOT_IMPLEMENTED</WriteStatus>` +
			`</u:GetMediaInfoResponse></s:Body></s:Envelope>`

	default:
		appLog("WARN", fmt.Sprintf("[SOAP] 未支持的动作: %s，返回空成功响应", action))
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body></s:Body></s:Envelope>`
	}

	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, resp)
}

func scpdHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<scpd xmlns="urn:schemas-upnp-org:service-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <actionList>
    <action><name>SetAVTransportURI</name></action>
    <action><name>Play</name></action>
    <action><name>Stop</name></action>
    <action><name>Pause</name></action>
    <action><name>Seek</name></action>
    <action><name>GetTransportInfo</name></action>
    <action><name>GetPositionInfo</name></action>
    <action><name>GetMediaInfo</name></action>
    <action><name>GetCurrentTransportActions</name></action>
  </actionList>
  <serviceStateTable>
    <stateVariable><name>TransportState</name><dataType>string</dataType></stateVariable>
    <stateVariable><name>CurrentURI</name><dataType>string</dataType></stateVariable>
    <stateVariable><name>TransportPlaySpeed</name><dataType>string</dataType></stateVariable>
  </serviceStateTable>
</scpd>`)
}

func renderingControlHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	soapAction := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
	action := ""
	if idx := strings.LastIndex(soapAction, "#"); idx >= 0 {
		action = soapAction[idx+1:]
	}
	_ = body
	var resp string
	switch action {
	case "GetVolume":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetVolumeResponse xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">` +
			`<CurrentVolume>50</CurrentVolume>` +
			`</u:GetVolumeResponse></s:Body></s:Envelope>`
	case "SetVolume":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:SetVolumeResponse xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"/></s:Body></s:Envelope>`
	default:
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body></s:Body></s:Envelope>`
	}
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, resp)
}

func rcScpdHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<scpd xmlns="urn:schemas-upnp-org:service-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <actionList>
    <action><name>GetVolume</name></action>
    <action><name>SetVolume</name></action>
  </actionList>
  <serviceStateTable>
    <stateVariable><name>Volume</name><dataType>ui2</dataType></stateVariable>
  </serviceStateTable>
</scpd>`)
}

func connectionManagerHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	soapAction := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
	action := ""
	if idx := strings.LastIndex(soapAction, "#"); idx >= 0 {
		action = soapAction[idx+1:]
	}
	_ = body
	var resp string
	switch action {
	case "GetProtocolInfo":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetProtocolInfoResponse xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1">` +
			`<Source></Source>` +
			`<Sink>http-get:*:*:*</Sink>` +
			`</u:GetProtocolInfoResponse></s:Body></s:Envelope>`
	case "GetCurrentConnectionIDs":
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:GetCurrentConnectionIDsResponse xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1">` +
			`<ConnectionIDs>0</ConnectionIDs>` +
			`</u:GetCurrentConnectionIDsResponse></s:Body></s:Envelope>`
	default:
		resp = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body></s:Body></s:Envelope>`
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
    <action><name>GetProtocolInfo</name></action>
    <action><name>GetCurrentConnectionIDs</name></action>
  </actionList>
  <serviceStateTable>
    <stateVariable><name>SourceProtocolInfo</name><dataType>string</dataType></stateVariable>
    <stateVariable><name>SinkProtocolInfo</name><dataType>string</dataType></stateVariable>
  </serviceStateTable>
</scpd>`)
}
