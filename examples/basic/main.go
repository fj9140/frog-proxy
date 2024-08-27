package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/fj9140/frogproxy"
)

func main() {

	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()
	proxy := frogproxy.NewProxyHttpServer()
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
