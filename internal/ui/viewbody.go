package ui

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/siner308/sims/internal/proxy"
)

// viewBody opens the response body in whatever this machine shows that kind of file with. An image
// or a video is the part of an exchange a reader most wants to see, and a terminal can only say how
// many bytes it was.
func (v *logsView) viewBody() {
	f, ok := v.selectedFlow()
	if !ok {
		v.app.flash("no exchange selected; press down first")
		return
	}
	v.app.openBody(f)
}

func (a *App) openBody(f proxy.Flow) {
	body := proxy.DecodeBody(f.RespHeader, f.RespBody)
	if len(body) == 0 {
		if f.RespSize > 0 {
			// the store drops the oldest bodies once it is full, so an exchange can outlive its own
			a.flash(fmt.Sprintf("the %s body of this exchange was dropped to make room for newer ones",
				proxy.SizeString(f.RespSize)))
			return
		}
		a.flash("this exchange has no response body to open")
		return
	}
	path, err := writeBodyFile(f, body)
	if err != nil {
		a.flashErr(err)
		return
	}
	if f.RespTruncated {
		a.flash(fmt.Sprintf("opening the first %s of the body; the rest was not kept", proxy.SizeString(int64(len(body)))))
	}
	a.openWithDefault(path)
}

// writeBody puts the body in a file named for what it is, because what opens it is decided by the
// extension: a png written as .txt opens in a text editor and shows its bytes.
func writeBodyFile(f proxy.Flow, body []byte) (string, error) {
	name := "sims-" + sanitize(f.Method+"-"+hostOf(f)) + "-*" + bodyExtension(f)
	file, err := os.CreateTemp("", name)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return file.Name(), nil
}

// bodyExtension is the file type of the response, taken from its content type and falling back to
// the path the request asked for.
func bodyExtension(f proxy.Flow) string {
	if ext := extensionForType(f.RespHeader); ext != "" {
		return ext
	}
	if i := strings.LastIndex(f.Path, "."); i >= 0 {
		if ext := f.Path[i:]; len(ext) <= 6 && !strings.ContainsAny(ext, "/?&=") {
			return ext
		}
	}
	return ".bin"
}

func extensionForType(h http.Header) string {
	ct := h.Get("Content-Type")
	if ct == "" {
		return ""
	}
	t, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ""
	}
	if ext, ok := wellKnownExtensions[t]; ok {
		return ext
	}
	// anything the table below does not name still opens, under whatever the system calls it
	if exts, err := mime.ExtensionsByType(t); err == nil && len(exts) > 0 {
		return shortest(exts)
	}
	return ""
}

// wellKnownExtensions names the types a capture is full of. The system's own table answers with
// whatever sorts first, which is .jpe for a jpeg and .m4v for an mp4: correct, and not what the
// file should be called when a person is about to look at it.
var wellKnownExtensions = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	"image/svg+xml": ".svg", "image/avif": ".avif", "image/heic": ".heic", "image/bmp": ".bmp",
	"image/x-icon": ".ico", "image/vnd.microsoft.icon": ".ico", "image/tiff": ".tiff",
	"video/mp4": ".mp4", "video/quicktime": ".mov", "video/webm": ".webm", "video/mpeg": ".mpeg",
	"audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/aac": ".aac", "audio/wav": ".wav",
	"audio/ogg": ".ogg", "audio/webm": ".weba", "audio/flac": ".flac",
	"application/pdf": ".pdf", "application/zip": ".zip", "application/gzip": ".gz",
	"application/json": ".json", "application/xml": ".xml", "text/xml": ".xml",
	"text/html": ".html", "text/plain": ".txt", "text/css": ".css",
	"application/javascript": ".js", "text/javascript": ".js", "application/wasm": ".wasm",
	"font/woff": ".woff", "font/woff2": ".woff2", "font/ttf": ".ttf", "font/otf": ".otf",
	"application/octet-stream": ".bin",
}

func shortest(exts []string) string {
	best := exts[0]
	for _, e := range exts[1:] {
		if len(e) < len(best) || (len(e) == len(best) && e < best) {
			best = e
		}
	}
	return best
}
