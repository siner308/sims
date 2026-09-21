package proxy

import (
	"mime"
	"path"
	"strings"
)

// Resource is the kind of thing an exchange fetched, in the buckets a browser's network panel uses.
type Resource string

const (
	ResourceXHR   Resource = "xhr"
	ResourceDoc   Resource = "doc"
	ResourceJS    Resource = "js"
	ResourceCSS   Resource = "css"
	ResourceImg   Resource = "img"
	ResourceFont  Resource = "font"
	ResourceMedia Resource = "media"
	ResourceWS    Resource = "ws"
	ResourceOther Resource = "other"
)

var Resources = []Resource{ResourceXHR, ResourceDoc, ResourceJS, ResourceCSS, ResourceImg, ResourceFont, ResourceMedia, ResourceWS, ResourceOther}

var extensions = map[string]Resource{
	".js": ResourceJS, ".mjs": ResourceJS, ".cjs": ResourceJS,
	".css":  ResourceCSS,
	".html": ResourceDoc, ".htm": ResourceDoc,
	".png": ResourceImg, ".jpg": ResourceImg, ".jpeg": ResourceImg, ".gif": ResourceImg, ".webp": ResourceImg,
	".svg": ResourceImg, ".ico": ResourceImg, ".avif": ResourceImg, ".heic": ResourceImg, ".bmp": ResourceImg,
	".woff": ResourceFont, ".woff2": ResourceFont, ".ttf": ResourceFont, ".otf": ResourceFont, ".eot": ResourceFont,
	".mp4": ResourceMedia, ".m4v": ResourceMedia, ".mov": ResourceMedia, ".webm": ResourceMedia, ".m3u8": ResourceMedia,
	".ts": ResourceMedia, ".mp3": ResourceMedia, ".m4a": ResourceMedia, ".aac": ResourceMedia, ".wav": ResourceMedia, ".ogg": ResourceMedia,
	".json": ResourceXHR, ".xml": ResourceXHR,
}

// Resource defaults to xhr when nothing says otherwise: most API calls carry no telling type, and xhr is the bucket a reader keeps when hiding the rest.
func (f Flow) Resource() Resource {
	switch f.Kind {
	case KindWebSocket:
		return ResourceWS
	case KindTunnel:
		return ResourceOther
	}
	if strings.EqualFold(f.RespHeader.Get("Upgrade"), "websocket") || f.Status == 101 {
		return ResourceWS
	}
	if r, ok := byContentType(f.RespHeader.Get("Content-Type")); ok {
		return r
	}
	if r, ok := extensions[strings.ToLower(path.Ext(strings.SplitN(f.Path, "?", 2)[0]))]; ok {
		return r
	}
	if accept := strings.ToLower(f.ReqHeader.Get("Accept")); strings.HasPrefix(accept, "text/html") {
		return ResourceDoc
	}
	if strings.Contains(strings.ToLower(f.RespHeader.Get("Content-Type")), "octet-stream") {
		return ResourceOther
	}
	return ResourceXHR
}

// application/octet-stream is left undecided on purpose: the path's extension usually knows better than a server that did not bother
func byContentType(header string) (Resource, bool) {
	if header == "" {
		return "", false
	}
	mt, _, err := mime.ParseMediaType(header)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(header, ";", 2)[0]))
	}
	mt = strings.ToLower(mt)
	switch {
	case mt == "text/html", mt == "application/xhtml+xml":
		return ResourceDoc, true
	case mt == "text/css":
		return ResourceCSS, true
	case strings.Contains(mt, "javascript"), strings.Contains(mt, "ecmascript"):
		return ResourceJS, true
	case strings.HasPrefix(mt, "image/"):
		return ResourceImg, true
	case strings.HasPrefix(mt, "font/"), strings.Contains(mt, "font"):
		return ResourceFont, true
	case strings.HasPrefix(mt, "audio/"), strings.HasPrefix(mt, "video/"), mt == "application/vnd.apple.mpegurl", mt == "application/x-mpegurl":
		return ResourceMedia, true
	case mt == "application/octet-stream":
		return "", false
	case strings.HasPrefix(mt, "text/"), strings.Contains(mt, "json"), strings.Contains(mt, "xml"),
		strings.Contains(mt, "protobuf"), strings.Contains(mt, "grpc"), strings.Contains(mt, "form"), strings.Contains(mt, "msgpack"):
		return ResourceXHR, true
	}
	return ResourceOther, true
}
