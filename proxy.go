package frogproxy

type ProxyHttpServer struct{}

func NewProxyHttpServer() *ProxyHttpServer {
	proxy := ProxyHttpServer{}

	return &proxy
}
