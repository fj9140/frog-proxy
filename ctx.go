package frogproxy

import (
	"crypto/tls"
	"net/http"
)

type CertStorage interface {
	Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error)
}

type ProxyCtx struct {
	Req       *http.Request
	Resp      *http.Response
	Session   int64
	Proxy     *ProxyHttpServer
	certStore CertStorage
	Error     error
}

func (ctx *ProxyCtx) printf(msg string, argv ...interface{}) {
	ctx.Proxy.Logger.Printf("[%03d] "+msg+"\n", append([]interface{}{ctx.Session & 0xFF}, argv...)...)
}

func (ctx *ProxyCtx) Logf(msg string, argv ...interface{}) {
	if ctx.Proxy.Verbose {
		ctx.printf("INFO: "+msg, argv...)
	}
}

func (ctx *ProxyCtx) Warnf(msg string, argv ...interface{}) {
	ctx.printf("WARN: "+msg, argv...)
}
