package frogproxy

import (
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"sync/atomic"
)

type ProxyHttpServer struct {
	sess                   int64
	KeepDestinationHeaders bool
	CertStore              CertStorage
	Verbose                bool
	Logger                 Logger
	httpsHandlers          []HttpsHandler
	ConnectDialWithReq     func(req *http.Request, network string, addr string) (net.Conn, error)
	ConnectDial            func(network string, addr string) (net.Conn, error)
	Tr                     *http.Transport
	reqHandlers            []ReqHandler
	respHandlers           []RespHandler
}

type flushWriter struct {
	w io.Writer
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if f, ok := fw.w.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}

var hasPort = regexp.MustCompile(`:\d+$`)

func copyHeaders(dst, src http.Header, keepDestHeaders bool) {
	if !keepDestHeaders {
		for k := range dst {
			dst.Del(k)
		}
	}

	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func (proxy *ProxyHttpServer) filterRequest(r *http.Request, ctx *ProxyCtx) (req *http.Request, resp *http.Response) {
	req = r
	for _, h := range proxy.reqHandlers {
		req, resp = h.Handle(req, ctx)
		if req != nil {
			break
		}
	}
	return
}

func (proxy *ProxyHttpServer) filterResponse(respOrig *http.Response, ctx *ProxyCtx) (resp *http.Response) {
	resp = respOrig
	for _, h := range proxy.respHandlers {
		ctx.Resp = resp
		resp = h.Handle(resp, ctx)
	}
	return
}

func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "CONNECT" {
		proxy.handleHttps(w, r)
	} else {
		ctx := &ProxyCtx{Req: r, Session: atomic.AddInt64(&proxy.sess, 1), Proxy: proxy}
		var err error
		ctx.Logf("Got request %v %v %v %v", r.URL.Path, r.Host, r.Method, r.URL.String())
		if !r.URL.IsAbs() {
			return
		}
		r, resp := proxy.filterRequest(r, ctx)

		if resp == nil {

		}

		var origBody io.ReadCloser
		if resp != nil {
			origBody = resp.Body
			defer origBody.Close()
		}

		resp = proxy.filterResponse(resp, ctx)
		if resp == nil {
			var errorString string
			if ctx.Error != nil {
				errorString = "error read response " + r.URL.Host + " : " + ctx.Error.Error()
				ctx.Logf(errorString)
				http.Error(w, ctx.Error.Error(), 500)
			} else {
				errorString = "error read response " + r.URL.Host + " : response is nil"
				ctx.Logf(errorString)
				http.Error(w, errorString, 500)
			}
			return
		}
		ctx.Logf("Copying response to client %v [%d]", resp.Status, resp.StatusCode)
		if origBody != resp.Body {
			resp.Header.Del("Content-Length")
		}

		copyHeaders(w.Header(), resp.Header, proxy.KeepDestinationHeaders)
		w.WriteHeader(resp.StatusCode)
		var copyWriter io.Writer = w
		if w.Header().Get("content-type") == "text/event-stream" {
			copyWriter = &flushWriter{w: w}
		}
		nr, err := io.Copy(copyWriter, resp.Body)
		if err := resp.Body.Close(); err != nil {
			ctx.Warnf("error close response body %v", err)
		}
		ctx.Logf("Copied %d bytes to client error=%v", nr, err)
	}
}

func NewProxyHttpServer() *ProxyHttpServer {
	proxy := ProxyHttpServer{
		Tr:     &http.Transport{},
		Logger: log.New(os.Stderr, "", log.LstdFlags),
	}

	return &proxy
}
