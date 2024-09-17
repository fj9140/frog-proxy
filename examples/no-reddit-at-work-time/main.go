package main

import (
	"log"
	"net/http"

	"github.com/fj9140/frogproxy"
)

func main() {
	proxy := frogproxy.NewProxyHttpServer()
	proxy.OnRequest(frogproxy.DstHostIs("www.reddit.com")).DoFunc(
		func(r *http.Request, ctx *frogproxy.ProxyCtx) (*http.Request, *http.Response) {
			return r, frogproxy.NewResponse(r, frogproxy.ContentTypeText, http.StatusForbidden, "No Reddit at work time")
		})

	log.Fatalln(http.ListenAndServe(":8080", proxy))
}
