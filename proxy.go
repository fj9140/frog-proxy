package frogproxy

import (
	"net"
	"net/http"
	"regexp"
)

type ProxyHttpServer struct {
	sess               int64
	CertStore          CertStorage
	Verbose            bool
	Logger             Logger
	httpsHandlers      []HttpsHandler
	ConnectDialWithReq func(req *http.Request, network string, addr string) (net.Conn, error)
	ConnectDial        func(network string, addr string) (net.Conn, error)
	Tr                 *http.Transport
}

var hasPort = regexp.MustCompile(`:\d+$`)

func (proxy *ProxyHttpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "CONNECT" {
		proxy.handleHttps(w, r)
	} else {
		return
	}
}

func NewProxyHttpServer() *ProxyHttpServer {
	proxy := ProxyHttpServer{
		Tr: &http.Transport{},
	}

	return &proxy
}
