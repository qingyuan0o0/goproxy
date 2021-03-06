package proxy

import (
	"io"
	"net/http"
	"strings"

	logging "github.com/op/go-logging"
	"github.com/shell909090/goproxy/netutil"
)

var logger = logging.MustGetLogger("logger")

var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te", // canonicalized version of "TE"
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

type Proxy struct {
	transport http.Transport
	dialer    netutil.Dialer
	username  string
	password  string
}

func NewProxy(dialer netutil.Dialer, username string, password string) (p *Proxy) {
	p = &Proxy{
		username:  username,
		password:  password,
		dialer:    dialer,
		transport: http.Transport{Dial: dialer.Dial},
	}
	if username != "" && password != "" {
		logger.Info("proxy-auth required")
	}
	return
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	logger.Infof("http: %s %s", req.Method, req.URL)

	if p.username != "" && p.password != "" {
		if !BasicAuth(w, req, p.username, p.password) {
			logger.Error("Http Auth Required")
			w.Header().Set("Proxy-Authenticate", "Basic realm=\"GoProxy\"")
			http.Error(w, http.StatusText(407), 407)
			return
		}
	}

	if req.Method == "CONNECT" {
		p.Connect(w, req)
		return
	}

	req.RequestURI = ""
	for _, h := range hopHeaders {
		if req.Header.Get(h) != "" {
			req.Header.Del(h)
		}
	}

	resp, err := p.transport.RoundTrip(req)
	if err != nil {
		logger.Error(err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	buf := netutil.BufferPool.Get().([]byte)
	defer netutil.BufferPool.Put(buf)
	_, err = io.CopyBuffer(w, resp.Body, buf)
	if err != nil {
		logger.Error(err.Error())
		return
	}
	return
}

func (p *Proxy) Connect(w http.ResponseWriter, r *http.Request) {
	hij, ok := w.(http.Hijacker)
	if !ok {
		logger.Error("httpserver does not support hijacking")
		return
	}
	srcconn, _, err := hij.Hijack()
	if err != nil {
		logger.Errorf("Cannot hijack connection: %s", err.Error())
		return
	}
	defer srcconn.Close()

	host := r.URL.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	dstconn, err := p.dialer.Dial("tcp", host)
	if err != nil {
		logger.Errorf("dial failed: %s", err.Error())
		srcconn.Write([]byte("HTTP/1.0 502 OK\r\n\r\n"))
		return
	}
	srcconn.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))

	netutil.CopyLink(srcconn, dstconn)
	return
}
