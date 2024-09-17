package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/fj9140/frogproxy"
)

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()
	proxy := frogproxy.NewProxyHttpServer()
	proxy.Verbose = *verbose
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
