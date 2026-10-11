// Package tunnel provides shared message types used by both the tunnel
// client and tunnel server.
package tunnel

import "net/http"

// Message is the envelope for all WebSocket tunnel messages.
type Message struct {
	Type      string      `json:"type"`
	RequestID string      `json:"request_id"`
	Data      interface{} `json:"data"`
}

// HTTPRequest carries an incoming HTTP request from the server to the client.
type HTTPRequest struct {
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Query   string      `json:"query"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"body"`
	Host    string      `json:"host"`
}

// HTTPResponse carries the client's HTTP response back to the server.
type HTTPResponse struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"body"`
}

// WSUpgrade is sent when a WebSocket upgrade is requested.
type WSUpgrade struct {
	Path    string      `json:"path"`
	Query   string      `json:"query"`
	Headers http.Header `json:"headers"`
	Host    string      `json:"host"`
}

// WSMessageData carries a single WebSocket frame payload.
type WSMessageData struct {
	MessageType int    `json:"message_type"`
	Data        []byte `json:"data"`
}

// WSClose signals that a WebSocket connection has closed.
type WSClose struct {
	Reason string `json:"reason"`
}
