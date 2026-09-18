package proxy

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

// HAR 1.2, the interchange format Proxyman, Charles and the browser dev tools all read and write.
type har struct {
	Log harLog `json:"log"`
}

type harLog struct {
	Version string     `json:"version"`
	Creator harCreator `json:"creator"`
	Entries []harEntry `json:"entries"`
}

type harCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type harEntry struct {
	Started  string      `json:"startedDateTime"`
	Time     float64     `json:"time"`
	Request  harRequest  `json:"request"`
	Response harResponse `json:"response"`
	Cache    struct{}    `json:"cache"`
	Timings  harTimings  `json:"timings"`
	Comment  string      `json:"comment,omitempty"`
}

type harNV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harRequest struct {
	Method      string       `json:"method"`
	URL         string       `json:"url"`
	HTTPVersion string       `json:"httpVersion"`
	Cookies     []harNV      `json:"cookies"`
	Headers     []harNV      `json:"headers"`
	Query       []harNV      `json:"queryString"`
	PostData    *harPostData `json:"postData,omitempty"`
	HeadersSize int          `json:"headersSize"`
	BodySize    int64        `json:"bodySize"`
}

type harPostData struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

type harResponse struct {
	Status      int        `json:"status"`
	StatusText  string     `json:"statusText"`
	HTTPVersion string     `json:"httpVersion"`
	Cookies     []harNV    `json:"cookies"`
	Headers     []harNV    `json:"headers"`
	Content     harContent `json:"content"`
	RedirectURL string     `json:"redirectURL"`
	HeadersSize int        `json:"headersSize"`
	BodySize    int64      `json:"bodySize"`
}

type harContent struct {
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

type harTimings struct {
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
}

// WriteHAR writes the HTTP flows (tunnels carry no messages and are skipped) as a HAR file.
func WriteHAR(w io.Writer, version string, flows []Flow) error {
	out := har{Log: harLog{Version: "1.2", Creator: harCreator{Name: "sims", Version: version}, Entries: []harEntry{}}}
	for _, f := range flows {
		if f.Kind == KindTunnel || !f.Done {
			continue
		}
		e := harEntry{
			Started: f.Start.UTC().Format(time.RFC3339Nano),
			Time:    float64(f.Duration) / float64(time.Millisecond),
			Request: harRequest{
				Method: f.Method, URL: f.URL, HTTPVersion: "HTTP/1.1",
				Cookies: []harNV{}, Headers: headersNV(f.ReqHeader), Query: queryNV(f.URL),
				HeadersSize: -1, BodySize: f.ReqSize,
			},
			Response: harResponse{
				Status: f.Status, StatusText: f.StatusText, HTTPVersion: "HTTP/1.1",
				Cookies: []harNV{}, Headers: headersNV(f.RespHeader),
				RedirectURL: f.RespHeader.Get("Location"), HeadersSize: -1, BodySize: f.RespSize,
			},
			Timings: harTimings{Send: 0, Wait: float64(f.Duration) / float64(time.Millisecond), Receive: 0},
			Comment: f.Error,
		}
		if len(f.ReqBody) > 0 {
			e.Request.PostData = &harPostData{MimeType: f.ReqHeader.Get("Content-Type"), Text: string(DecodeBody(f.ReqHeader, f.ReqBody))}
		}
		body := DecodeBody(f.RespHeader, f.RespBody)
		e.Response.Content = harContent{Size: int64(len(body)), MimeType: f.RespHeader.Get("Content-Type")}
		if IsText(body) {
			e.Response.Content.Text = string(body)
		} else {
			e.Response.Content.Text = base64.StdEncoding.EncodeToString(body)
			e.Response.Content.Encoding = "base64"
		}
		out.Log.Entries = append(out.Log.Entries, e)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func headersNV(h http.Header) []harNV {
	out := []harNV{}
	for k, vs := range h {
		for _, v := range vs {
			out = append(out, harNV{Name: k, Value: v})
		}
	}
	return out
}

func queryNV(raw string) []harNV {
	out := []harNV{}
	u, err := url.Parse(raw)
	if err != nil {
		return out
	}
	for k, vs := range u.Query() {
		for _, v := range vs {
			out = append(out, harNV{Name: k, Value: v})
		}
	}
	return out
}
