// Package server 提供仪表盘的 HTTP 静态服务、WebSocket 实时推送与服务调用。
package server

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Yonsm/MiHex/core"
)

//go:generate sh gen.sh
//go:embed wwwgz
var www embed.FS

const (
	jsonType     = "application/json; charset=utf-8"
	writeTimeout = 5 * time.Second
)

var (
	hub   *Hub
	token string
)

type service func(args any) (any, error)

var services = map[string]service{
	"states":  states,
	"objects": objects,
}

func init() {
	core.RegisterModule("server", setup)
}

func setup(sec core.Section) ([]core.Object, error) {
	conf := sec.Map()
	token = core.Str(conf["token"])
	hub = &Hub{conns: map[*conn]struct{}{}, slots: make(chan struct{}, maxConns)}
	core.SetBus(hub)

	bind := core.Str(conf["bind"])
	port := core.Int(conf["port"], 88)
	cert, key := core.Str(conf["cert"]), core.Str(conf["key"])
	scheme := "http"
	var tlsConfig *tls.Config
	if cert != "" || key != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("加载 TLS 证书和私钥: %w", err)
		}
		scheme = "https"
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{pair}, NextProtos: []string{"http/1.1"}}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}

	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
	}
	srv := &http.Server{Handler: http.HandlerFunc(serve), ReadHeaderTimeout: 10 * time.Second}
	core.OnShutdown(func(ctx context.Context) error {
		hub.closeAll()
		return srv.Shutdown(ctx)
	})
	sec.AfterLoad(func() {
		go func() {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTP 服务退出", "err", err)
			}
		}()
	})

	addr := net.JoinHostPort(advertiseHost(conf, bind), strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	url := fmt.Sprintf("%s://%s/", scheme, addr)
	slog.Warn("仪表盘", "url", url)

	return links(sec), nil
}

// serve 的路由与 Python 版一致：静态资源公开，WebSocket 与服务接口需要 token。
func serve(w http.ResponseWriter, r *http.Request) {
	if websocketUpgrade(r) {
		if !authed(r) {
			unauthorized(w)
			return
		}
		hub.handle(w, r)
		return
	}
	if fn, ok := services[strings.TrimPrefix(r.URL.Path, "/")]; ok {
		if !authed(r) {
			unauthorized(w)
			return
		}
		if wantsHTML(r) {
			list := r.Clone(r.Context())
			list.URL.Path = "/list.htm"
			serveStatic(w, list)
			return
		}
		go core.Track(context.Background())
		r, err := fn(nil)
		writeJSON(w, r, err)
		return
	}
	serveStatic(w, r)
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="MiHex"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func authed(r *http.Request) bool {
	if token == "" {
		return true
	}
	if r.URL.RawQuery == token {
		return true
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(auth[6:])
	if err != nil {
		return false
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		pass = user
	}
	return subtle.ConstantTimeCompare([]byte(pass), []byte(token)) == 1
}

// wantsHTML 判断是否为浏览器导航请求；是则把服务接口渲染成列表页，API 调用仍返回 JSON。
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", jsonType)
	w.Write(data)
}

func states(any) (any, error) {
	out := []map[string]any{}
	for _, o := range core.Objects() {
		if !o.IsEntity() {
			continue
		}
		if s := o.State(); s != nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func objects(any) (any, error) {
	out := []map[string]any{}
	for _, o := range core.Objects() {
		if s := o.State(); s != nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func execCommand(args []any) (any, error) {
	return core.Exec(context.Background(), args)
}

func links(sec core.Section) []core.Object {
	conf := sec.Map()
	var objs []core.Object
	for _, k := range sec.Keys {
		for _, c := range core.List(conf[k]) {
			objs = append(objs, core.NewEntity(core.Map(c), k))
		}
	}
	return objs
}

func advertiseHost(conf map[string]any, bind string) string {
	if h := core.Str(conf["host"]); h != "" {
		return h
	}
	if bind != "" && bind != "0.0.0.0" && bind != "::" {
		return bind
	}
	c, err := net.Dial("udp", "10.255.255.1:80")
	if err != nil {
		return "localhost"
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return "localhost"
}

var mimes = map[string]string{
	".htm":   "text/html; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "application/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".woff2": "font/woff2",
	".png":   "image/png",
}

func serveStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "dash.htm"
	}
	data, err := fs.ReadFile(www, "wwwgz/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sum := sha1.Sum(data)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", mimeOf(name))
	if gzipped(name) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

// gzipped 判断该资源在嵌入时是否已 gzip 压缩。
func gzipped(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".htm", ".html", ".css", ".js", ".json":
		return true
	}
	return false
}

func mimeOf(name string) string {
	if m, ok := mimes[strings.ToLower(path.Ext(name))]; ok {
		return m
	}
	return "application/octet-stream"
}

func websocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}
