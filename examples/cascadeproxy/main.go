package main

import (
	"log"
	"net/http"
	"net/url"

	"github.com/fj9140/frogproxy"
)

func main() {
	middleProxy := frogproxy.NewProxyHttpServer()
	middleProxy.Verbose = true
	middleProxy.Tr.Proxy = func(req *http.Request) (*url.URL, error) {
		return url.Parse("http://127.0.0.1:7899")
	}
	middleProxy.ConnectDial = middleProxy.NewConnectDialToProxy("http://127.0.0.1:7899")
	log.Println("serving middle proxy server at localhost:8080")
	http.ListenAndServe("localhost:8080", middleProxy)
}
