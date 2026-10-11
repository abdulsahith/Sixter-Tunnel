package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

/*
Bounded body buffering for WAF inspection.

The firewall has to see POST bodies — that's where most real injection lands —
but a tunnel gateway must not hold a whole upload in RAM. So we read at most
`limit` bytes, hand that prefix to the WAF, then splice the buffered prefix
back in front of the untouched remainder so the proxy still streams the full
body downstream. Large uploads pay only the cost of the first chunk.
*/

// peekBody returns up to limit bytes of the request body for inspection and
// leaves r.Body fully readable. Binary uploads are skipped: scanning a JPEG for
// SQL keywords is pure cost with no signal.
func peekBody(r *http.Request, limit int) (body string, truncated bool) {
	if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return "", false
	}
	if !inspectableContentType(r.Header.Get("Content-Type")) {
		return "", false
	}

	buf := make([]byte, limit)
	n, err := io.ReadFull(r.Body, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		// couldn't read — restore what we got and let the request through
		r.Body = spliceBody(buf[:n], r.Body)
		return "", false
	}
	prefix := buf[:n]

	// full body consumed only when we hit EOF before filling the buffer
	truncated = n == limit
	r.Body = spliceBody(prefix, r.Body)
	return string(prefix), truncated
}

// spliceBody rejoins an already-read prefix with the rest of the stream.
func spliceBody(prefix []byte, rest io.ReadCloser) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{
		Reader: io.MultiReader(bytes.NewReader(prefix), rest),
		Closer: rest,
	}
}

// inspectableContentType reports whether a body is worth scanning. Text-ish
// payloads yes; images, video, archives and octet-streams no.
func inspectableContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return true // unknown → inspect; attackers omit Content-Type deliberately
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case strings.HasPrefix(ct, "text/"),
		ct == "application/x-www-form-urlencoded",
		ct == "application/json",
		ct == "application/ld+json",
		ct == "application/xml",
		ct == "application/graphql",
		strings.HasSuffix(ct, "+json"),
		strings.HasSuffix(ct, "+xml"),
		strings.HasPrefix(ct, "multipart/form-data"):
		return true
	}
	return false
}
