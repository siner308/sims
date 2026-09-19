package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
)

// DecodeBody undoes the content encoding for display; an encoding sims does not know comes back as
// is. Brotli is here because most HTTPS sites now default to it, and an undecoded body reads as
// "(binary)" in the flow detail, which looks like the proxy failed.
func DecodeBody(h http.Header, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var r io.Reader
	switch strings.ToLower(strings.TrimSpace(h.Get("Content-Encoding"))) {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return body
		}
		r = zr
	case "deflate":
		r = flate.NewReader(bytes.NewReader(body))
	case "br":
		r = brotli.NewReader(bytes.NewReader(body))
	default:
		return body
	}
	out, err := io.ReadAll(r)
	if err != nil && len(out) == 0 {
		return body
	}
	return out
}

// IsText reports whether body reads as text: valid UTF-8 with no control bytes other than whitespace.
func IsText(body []byte) bool {
	if len(body) == 0 {
		return true
	}
	if !utf8.Valid(body) {
		return false
	}
	for _, b := range body {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			return false
		}
	}
	return true
}

// Pretty renders a decoded body for reading: JSON is indented, other text is returned as is, and
// binary is summarised instead of dumped.
func Pretty(h http.Header, body []byte) string {
	body = DecodeBody(h, body)
	if len(body) == 0 {
		return ""
	}
	if !IsText(body) {
		return "(binary " + describeType(h) + ", " + sizeString(int64(len(body))) + ")"
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		var buf bytes.Buffer
		if err := json.Indent(&buf, trimmed, "", "  "); err == nil {
			return buf.String()
		}
	}
	return string(body)
}

func describeType(h http.Header) string {
	ct := h.Get("Content-Type")
	if ct == "" {
		return "unknown type"
	}
	if t, _, err := mime.ParseMediaType(ct); err == nil {
		return t
	}
	return ct
}

func sizeString(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
}

// SizeString formats a byte count the way the table shows it.
func SizeString(n int64) string { return sizeString(n) }
