package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/fj9140/frogproxy"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()
	proxy := frogproxy.NewProxyHttpServer()
	proxy.OnRequest().HandleConnect(frogproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *frogproxy.ProxyCtx) (*http.Request, *http.Response) {
		if req.URL.Scheme == "https" {
			req.URL.Scheme = "http"
		}
		return req, nil
	})

	proxy.Verbose = *verbose
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
