package proxy_test

import (
	"net/http"
	"testing"

	"github.com/siner308/sims/internal/proxy"
)

func TestResourceBucketsFollowTheResponseThenThePath(t *testing.T) {
	flow := func(kind proxy.Kind, path, contentType, accept string, status int) proxy.Flow {
		f := proxy.Flow{Kind: kind, Path: path, Status: status, ReqHeader: http.Header{}, RespHeader: http.Header{}}
		if contentType != "" {
			f.RespHeader.Set("Content-Type", contentType)
		}
		if accept != "" {
			f.ReqHeader.Set("Accept", accept)
		}
		return f
	}
	cases := map[string]struct {
		f    proxy.Flow
		want proxy.Resource
	}{
		"json api":                 {flow(proxy.KindHTTP, "/v1/me", "application/json; charset=utf-8", "", 200), proxy.ResourceXHR},
		"protobuf api":             {flow(proxy.KindHTTP, "/rpc", "application/x-protobuf", "", 200), proxy.ResourceXHR},
		"html page":                {flow(proxy.KindHTTP, "/", "text/html; charset=utf-8", "", 200), proxy.ResourceDoc},
		"script":                   {flow(proxy.KindHTTP, "/app.js", "application/javascript", "", 200), proxy.ResourceJS},
		"stylesheet":               {flow(proxy.KindHTTP, "/a.css", "text/css", "", 200), proxy.ResourceCSS},
		"image":                    {flow(proxy.KindHTTP, "/logo", "image/png", "", 200), proxy.ResourceImg},
		"font":                     {flow(proxy.KindHTTP, "/f", "font/woff2", "", 200), proxy.ResourceFont},
		"legacy font type":         {flow(proxy.KindHTTP, "/f.woff", "application/font-woff", "", 200), proxy.ResourceFont},
		"video":                    {flow(proxy.KindHTTP, "/v", "video/mp4", "", 200), proxy.ResourceMedia},
		"hls playlist":             {flow(proxy.KindHTTP, "/v", "application/vnd.apple.mpegurl", "", 200), proxy.ResourceMedia},
		"websocket kind":           {flow(proxy.KindWebSocket, "/ws", "", "", 0), proxy.ResourceWS},
		"websocket upgrade":        {flow(proxy.KindHTTP, "/ws", "", "", 101), proxy.ResourceWS},
		"tunnel":                   {flow(proxy.KindTunnel, "", "", "", 0), proxy.ResourceOther},
		"pending image by path":    {flow(proxy.KindHTTP, "/img/hero.webp?w=200", "", "", 0), proxy.ResourceImg},
		"octet stream with ext":    {flow(proxy.KindHTTP, "/bundle.js", "application/octet-stream", "", 200), proxy.ResourceJS},
		"octet stream without ext": {flow(proxy.KindHTTP, "/blob", "application/octet-stream", "", 200), proxy.ResourceOther},
		"pending page by accept":   {flow(proxy.KindHTTP, "/", "", "text/html,application/xhtml+xml", 0), proxy.ResourceDoc},
		"pending api":              {flow(proxy.KindHTTP, "/v1/items", "", "*/*", 0), proxy.ResourceXHR},
		"unknown binary type":      {flow(proxy.KindHTTP, "/x", "application/x-unknown", "", 200), proxy.ResourceOther},
	}
	for name, c := range cases {
		if got := c.f.Resource(); got != c.want {
			t.Errorf("%s: Resource() = %q, want %q", name, got, c.want)
		}
	}
}
