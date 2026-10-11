// Sixter tunnel client — connects out to the tunnel server and forwards
// HTTP + WebSocket traffic to a local port.
//
// Credentials come from ~/.sixter/config.yaml (created by "sixter login").
// Flags override config values.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"sixter.in/tunnel/internal/config"
	"sixter.in/tunnel/internal/tunnel"
)

type LocalWS struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// localClient is shared across all forwarded requests so connections to the
// local app are POOLED and reused. A dev server like Vite fires hundreds of
// small module requests; a fresh client per request (the old behaviour) opened
// a new TCP connection every time and was the main cause of slow loads.
var localClient = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 256, // keep many warm connections to the local port
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   false,
	},
}

var (
	tunnelWriteMu sync.Mutex
	wsMutex       sync.RWMutex
	wsConns       = make(map[string]*LocalWS)
)

// localHost is the address the app listens on. Package-level because every
// proxy helper needs it and threading it through each signature would touch
// code that has nothing else to do with addressing.
var localHost = "127.0.0.1"

func main() {
	sub := flag.String("subdomain", "", "subdomain (default: your tunnel subdomain from `sixter login`)")
	port := flag.Int("port", 5173, "local port to forward")
	// Where the app actually listens. Defaults to loopback, which is right for
	// a developer's laptop and for host-networked containers. With pod
	// networking the app has its OWN address, and forwarding to 127.0.0.1
	// would find nothing there.
	host := flag.String("host", "127.0.0.1", "local address to forward to")
	server := flag.String("server", "", "server ws url (default: from config, else ws://sixter.in:8008)")
	token := flag.String("token", "", "API token (default: from config)")
	flag.Parse()

	// Fill defaults from ~/.sixter/config.yaml
	cfg, err := config.Load()
	if err != nil {
		log.Printf("warning: could not read config: %v", err)
	}
	if cfg != nil {
		if *sub == "" {
			*sub = cfg.TunnelSub
		}
		if *server == "" {
			*server = cfg.Server
		}
		if *token == "" {
			*token = cfg.Token
		}
	}
	if *server == "" {
		*server = "ws://sixter.in:8008"
	}
	if *sub == "" {
		log.Fatal("no subdomain — run `sixter login` first, or pass -subdomain")
	}
	if *token == "" {
		log.Println("warning: no API token — the server will reject this tunnel unless it runs in -open mode. Run `sixter login`.")
	}

	u, err := url.Parse(*server)
	if err != nil {
		log.Fatal("bad server url: ", err)
	}
	u.Path = "/connect"
	q := url.Values{}
	q.Set("subdomain", *sub)
	if *token != "" {
		q.Set("token", *token)
	}
	u.RawQuery = q.Encode()

	dialer := *websocket.DefaultDialer // copy so we don't mutate the global
	dialer.EnableCompression = true    // compress the tunnel pipe (Vite/JS is very compressible)
	dialer.ReadBufferSize = 64 * 1024
	dialer.WriteBufferSize = 64 * 1024

	conn, resp, err := dialer.Dial(u.String(), nil)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			log.Fatal("unauthorized — run `sixter login` and use one of your assigned subdomains")
		}
		log.Fatal("dial error: ", err)
	}
	defer conn.Close()

	log.Printf("Tunnel live → https://%s.sixter.in", *sub)
	localHost = *host
	log.Printf("Forwarding all traffic to → http://%s:%d", localHost, *port)

	// Keepalive
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			tunnelWriteMu.Lock()
			_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			tunnelWriteMu.Unlock()
		}
	}()

	conn.SetReadLimit(1020 << 20)
	conn.SetPongHandler(func(string) error { return nil })

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Tunnel closed: %v", err)
			return
		}

		var tmsg tunnel.Message
		if err := json.Unmarshal(msg, &tmsg); err != nil {
			continue
		}

		switch tmsg.Type {
		case "http_request":
			go handleHTTPRequest(conn, tmsg, *port)
		case "ws_upgrade":
			go handleWSUpgrade(conn, tmsg, *port)
		case "ws_message":
			go handleWSMessage(tmsg)
		case "ws_close":
			go handleWSClose(tmsg)
		}
	}
}

func handleHTTPRequest(tunnelConn *websocket.Conn, tmsg tunnel.Message, port int) {
	reqData, _ := json.Marshal(tmsg.Data)
	var req tunnel.HTTPRequest
	if err := json.Unmarshal(reqData, &req); err != nil {
		return
	}

	log.Printf("→ %s %s [Body: %d bytes]", req.Method, req.Path, len(req.Body))

	targetURL := "http://" + localHost + ":" + strconv.Itoa(port) + req.Path
	if req.Query != "" {
		targetURL += "?" + req.Query
	}

	httpReq, err := http.NewRequest(req.Method, targetURL, bytes.NewReader(req.Body))
	if err != nil {
		sendHTTPResponse(tunnelConn, tmsg.RequestID, 500, http.Header{}, []byte("Internal Server Error"))
		return
	}

	httpReq.Host = req.Host
	httpReq.Header.Set("Host", req.Host)

	for key, values := range req.Headers {
		lk := strings.ToLower(key)
		if lk == "connection" || lk == "upgrade" ||
			lk == "sec-websocket-key" || lk == "sec-websocket-version" ||
			lk == "sec-websocket-extensions" || lk == "sec-websocket-protocol" {
			continue
		}
		for _, v := range values {
			httpReq.Header.Add(key, v)
		}
	}

	resp, err := localClient.Do(httpReq)
	if err != nil {
		sendHTTPResponse(tunnelConn, tmsg.RequestID, 502, http.Header{}, []byte("Bad Gateway"))
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("← %d %s [%d bytes]", resp.StatusCode, http.StatusText(resp.StatusCode), len(respBody))

	respHeaders := http.Header{}
	for key, values := range resp.Header {
		lk := strings.ToLower(key)
		if lk == "connection" || lk == "keep-alive" ||
			lk == "transfer-encoding" || lk == "upgrade" {
			continue
		}
		respHeaders[key] = values
	}

	sendHTTPResponse(tunnelConn, tmsg.RequestID, resp.StatusCode, respHeaders, respBody)
}

func handleWSUpgrade(tunnelConn *websocket.Conn, tmsg tunnel.Message, port int) {
	reqData, _ := json.Marshal(tmsg.Data)
	var wsReq tunnel.WSUpgrade
	if err := json.Unmarshal(reqData, &wsReq); err != nil {
		return
	}

	log.Printf("→ WS %s [hdrfix4]", wsReq.Path)

	targetURL := "ws://" + localHost + ":" + strconv.Itoa(port) + wsReq.Path
	if wsReq.Query != "" {
		targetURL += "?" + wsReq.Query
	}

	dialer := websocket.Dialer{
		EnableCompression: false,
		ReadBufferSize:    64 * 1024,
		WriteBufferSize:   64 * 1024,
	}

	// Copy only safe headers, then forcibly remove every handshake header the
	// gorilla dialer regenerates. Both a skip (denylist) AND an explicit Del,
	// so nothing can leak through and cause "duplicate header not allowed".
	headers := http.Header{}
	for key, values := range wsReq.Headers {
		lk := strings.ToLower(key)
		if lk == "connection" || lk == "upgrade" ||
			strings.HasPrefix(lk, "sec-websocket") || lk == "host" {
			continue
		}
		for _, v := range values {
			headers.Add(key, v)
		}
	}
	for _, h := range []string{
		"Upgrade", "Connection",
		"Sec-Websocket-Key", "Sec-Websocket-Version",
		"Sec-Websocket-Extensions", "Sec-Websocket-Accept", "Sec-Websocket-Protocol",
	} {
		headers.Del(h)
	}

	headers.Set("Host", wsReq.Host)

	// diagnostic: show exactly which header keys we pass to the dialer
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	log.Printf("WS dial headers: %v", keys)

	localConn, _, err := dialer.Dial(targetURL, headers)
	if err != nil {
		log.Printf("WS dial failed: %v", err)
		sendWSClose(tunnelConn, tmsg.RequestID, "local ws dial failed")
		return
	}

	lws := &LocalWS{conn: localConn}

	wsMutex.Lock()
	wsConns[tmsg.RequestID] = lws
	wsMutex.Unlock()

	log.Printf("WS connected for %s", wsReq.Path)

	// Local → tunnel
	go func() {
		defer func() {
			wsMutex.Lock()
			delete(wsConns, tmsg.RequestID)
			wsMutex.Unlock()
			_ = localConn.Close()
			sendWSClose(tunnelConn, tmsg.RequestID, "local ws closed")
		}()

		for {
			mt, md, err := localConn.ReadMessage()
			if err != nil {
				return
			}

			msg := tunnel.Message{
				Type:      "ws_message",
				RequestID: tmsg.RequestID,
				Data: tunnel.WSMessageData{
					MessageType: mt,
					Data:        md,
				},
			}
			data, _ := json.Marshal(msg)

			tunnelWriteMu.Lock()
			err = tunnelConn.WriteMessage(websocket.BinaryMessage, data)
			tunnelWriteMu.Unlock()

			if err != nil {
				return
			}
		}
	}()
}

func handleWSMessage(tmsg tunnel.Message) {
	wsMutex.RLock()
	lws, ok := wsConns[tmsg.RequestID]
	wsMutex.RUnlock()
	if !ok {
		return
	}

	msgData, _ := json.Marshal(tmsg.Data)
	var wsData tunnel.WSMessageData
	if err := json.Unmarshal(msgData, &wsData); err != nil {
		return
	}

	lws.mu.Lock()
	_ = lws.conn.WriteMessage(wsData.MessageType, wsData.Data)
	lws.mu.Unlock()
}

func handleWSClose(tmsg tunnel.Message) {
	wsMutex.RLock()
	lws, ok := wsConns[tmsg.RequestID]
	wsMutex.RUnlock()
	if !ok {
		return
	}

	msgData, _ := json.Marshal(tmsg.Data)
	var c tunnel.WSClose
	_ = json.Unmarshal(msgData, &c)

	lws.mu.Lock()
	_ = lws.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, c.Reason))
	_ = lws.conn.Close()
	lws.mu.Unlock()

	wsMutex.Lock()
	delete(wsConns, tmsg.RequestID)
	wsMutex.Unlock()
}

func sendHTTPResponse(tunnelConn *websocket.Conn, requestID string, status int, headers http.Header, body []byte) {
	msg := tunnel.Message{
		Type:      "http_response",
		RequestID: requestID,
		Data: tunnel.HTTPResponse{
			Status:  status,
			Headers: headers,
			Body:    body,
		},
	}
	data, _ := json.Marshal(msg)

	tunnelWriteMu.Lock()
	_ = tunnelConn.WriteMessage(websocket.BinaryMessage, data)
	tunnelWriteMu.Unlock()
}

func sendWSClose(tunnelConn *websocket.Conn, requestID, reason string) {
	msg := tunnel.Message{
		Type:      "ws_close",
		RequestID: requestID,
		Data:      tunnel.WSClose{Reason: reason},
	}
	data, _ := json.Marshal(msg)

	tunnelWriteMu.Lock()
	_ = tunnelConn.WriteMessage(websocket.BinaryMessage, data)
	tunnelWriteMu.Unlock()
}
