package transport

import (
	"bufio"
	"compress/gzip"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

var DefaultMaxIdleConnsPerHost = 2

type Transport struct {
	Proxy               func(*http.Request) (*url.URL, error)
	lk                  sync.Mutex
	altProto            map[string]RoundTripper
	idleConn            map[string][]*persistConn
	Dial                func(net, addr string) (c net.Conn, err error)
	TLSClientConfig     *tls.Config
	DisableCompression  bool
	DisableKeepAlives   bool
	MaxIdleConnsPerHost int
}

type RoundTripDetails struct {
	Host    string
	TCPAddr *net.TCPAddr
	IsProxy bool
	Error   error
}

type transportRequest struct {
	*http.Request
	extra http.Header
}

type connectMethod struct {
	proxyURL     *url.URL
	targetSchema string
	targetAddr   string
}

type responseAndError struct {
	res *http.Response
	err error
}

type requestAndChan struct {
	req       *http.Request
	ch        chan responseAndError
	addedGzip bool
}

type persistConn struct {
	t                    *Transport
	cacheKey             string
	conn                 net.Conn
	br                   *bufio.Reader
	bw                   *bufio.Writer
	reqch                chan requestAndChan
	isProxy              bool
	mutateHeaderFunc     func(http.Header)
	lk                   sync.Mutex
	numExpectedResponses int
	broken               bool
	host                 string
	ip                   *net.TCPAddr
}

type discardOnCloseReadCloser struct {
	io.ReadCloser
}

type readFirstCloseBoth struct {
	io.ReadCloser
	io.Closer
}

type bodyEOFSignal struct {
	body     io.ReadCloser
	fn       func()
	isClosed bool
}

func (es *bodyEOFSignal) Read(p []byte) (n int, err error) {
	n, err = es.body.Read(p)
	if es.isClosed && n > 0 {
		panic("http: unexpected bodyEOFSingal Read after Close; see goproxy issue 1725")
	}
	if err == io.EOF && es.fn != nil {
		es.fn()
		es.fn = nil
	}
	return
}

func (es *bodyEOFSignal) Close() (err error) {
	if es.isClosed {
		return nil
	}
	es.isClosed = true
	err = es.body.Close()
	if err == nil && es.fn != nil {
		es.fn()
		es.fn = nil
	}
	return
}

func (r *readFirstCloseBoth) Close() error {
	if err := r.ReadCloser.Close(); err != nil {
		r.Closer.Close()
		return err
	}
	if err := r.Closer.Close(); err != nil {
		return err
	}

	return nil
}

func (d *discardOnCloseReadCloser) Close() error {
	io.Copy(io.Discard, d.ReadCloser)
	return d.ReadCloser.Close()
}

func (tr *transportRequest) extraHeaders() http.Header {
	if tr.extra == nil {
		tr.extra = make(http.Header)
	}
	return tr.extra
}

func (pc *persistConn) isBroken() bool {
	pc.lk.Lock()
	defer pc.lk.Unlock()
	return pc.broken
}

func (pc *persistConn) close() {
	pc.lk.Lock()
	defer pc.lk.Unlock()
	pc.closeLocked()
}

func (pc *persistConn) closeLocked() {
	pc.broken = true
	pc.conn.Close()
	pc.mutateHeaderFunc = nil
}

func (pc *persistConn) readLoop() {
	alive := false
	var lastBody io.ReadCloser
	for alive {
		pb, err := pc.br.Peek(1)

		pc.lk.Lock()
		if pc.numExpectedResponses == 0 {
			pc.closeLocked()
			pc.lk.Unlock()
			if len(pb) == 0 {
				log.Printf("Unsolicited response recived on idle HTTP channel starting with %q; err=%v", string(pb), err)
			}
			return
		}
		pc.lk.Unlock()

		rc := <-pc.reqch

		if lastBody != nil {
			lastBody.Close()
			lastBody = nil
		}
		resp, err := http.ReadResponse(pc.br, rc.req)

		if err != nil {
			pc.close()
		} else {
			hasBody := rc.req.Method != "HEAD" && resp.ContentLength != 0
			if rc.addedGzip && hasBody && resp.Header.Get("Content-Encoding") == "gzip" {
				resp.Header.Del("Content-Encoding")
				resp.Header.Del("Content-Length")
				resp.ContentLength = -1
				gzReader, zerr := gzip.NewReader(resp.Body)
				if zerr != nil {
					pc.close()
					err = zerr
				} else {
					resp.Body = &readFirstCloseBoth{&discardOnCloseReadCloser{gzReader}, resp.Body}
				}
			}
			resp.Body = &bodyEOFSignal{body: resp.Body}
		}

		if err != nil || resp.Close || rc.req.Close {
			alive = false
		}

		hasBody := resp != nil && resp.ContentLength != 0
		var waitForBodyRead chan bool
		if alive {
			if hasBody {
				lastBody = resp.Body
				waitForBodyRead = make(chan bool)
				resp.Body.(*bodyEOFSignal).fn = func() {
					if !pc.t.putIdleConn(pc) {
						alive = false
					}
					waitForBodyRead <- true
				}
			} else {
				lastBody = nil
				if !pc.t.putIdleConn(pc) {
					alive = false
				}
			}
		}

		rc.ch <- responseAndError{resp, err}

		if waitForBodyRead != nil {
			<-waitForBodyRead
		}
	}
}

func (pc *persistConn) roundTrip(req *transportRequest) (resp *http.Response, err error) {
	if pc.mutateHeaderFunc != nil {
		panic("mutateHeaderFunc not supported in modified Transport")
	}

	requestedGzip := false
	if !pc.t.DisableCompression && req.Header.Get("Accept-Encoding") == "" {
		requestedGzip = true
		req.extraHeaders().Set("Accept-Encoding", "gzip")
	}

	pc.lk.Lock()
	pc.numExpectedResponses++
	pc.lk.Unlock()

	if pc.isProxy {
		err = req.Request.WriteProxy(pc.bw)
	} else {
		err = req.Request.Write(pc.bw)
	}
	if err != nil {
		pc.close()
		return
	}
	pc.bw.Flush()

	ch := make(chan responseAndError, 1)
	pc.reqch <- requestAndChan{req.Request, ch, requestedGzip}
	re := <-ch
	pc.lk.Lock()
	pc.numExpectedResponses--
	pc.lk.Unlock()

	return re.res, re.err
}

func (cm *connectMethod) String() string {
	proxyStr := ""
	if cm.proxyURL != nil {
		proxyStr = cm.proxyURL.String()
	}
	return strings.Join([]string{proxyStr, cm.targetSchema, cm.targetAddr}, "|")
}

func (cm *connectMethod) tlsHost() string {
	h := cm.targetAddr
	if hasPort(h) {
		h = h[:strings.LastIndex(h, ":")]
	}
	return h
}

func (cm *connectMethod) addr() string {
	if cm.proxyURL != nil {
		return canonicalAddr(cm.proxyURL)
	}
	return cm.targetAddr
}

func (cm *connectMethod) proxyAuth() string {
	if cm.proxyURL == nil {
		return ""
	}
	if u := cm.proxyURL.User; u != nil {
		return "Basic " + base64.URLEncoding.EncodeToString([]byte(u.String()))
	}
	return ""
}

func (t *Transport) connectMethodForRequest(treq *transportRequest) (*connectMethod, error) {
	cm := &connectMethod{
		targetSchema: treq.URL.Scheme,
		targetAddr:   canonicalAddr(treq.URL),
	}
	if t.Proxy != nil {
		var err error
		cm.proxyURL, err = t.Proxy(treq.Request)
		if err != nil {
			return nil, err
		}
	}
	return cm, nil
}

func (t *Transport) getIdleConn(cm *connectMethod) (pconn *persistConn) {
	t.lk.Lock()
	defer t.lk.Unlock()
	if t.idleConn == nil {
		t.idleConn = make(map[string][]*persistConn)
	}
	key := cm.String()
	for {
		pconns, ok := t.idleConn[key]
		if !ok {
			return nil
		}
		if len(pconns) == 1 {
			pconn = pconns[0]
			delete(t.idleConn, key)
		} else {
			pconn = pconns[len(pconns)-1]
			t.idleConn[key] = pconns[0 : len(pconns)-1]
		}
		if !pconn.isBroken() {
			return
		}
	}
}

func (t *Transport) dial(network, addr string) (c net.Conn, raddr string, ip *net.TCPAddr, err error) {
	if t.Dial != nil {
		ip, err = net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return
		}
		c, err = t.Dial(network, addr)
		raddr = addr
		return
	}
	addri, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return
	}
	c, err = net.DialTCP("tcp", nil, addri)
	raddr = addr
	ip = addri
	return
}

func (t *Transport) putIdleConn(pconn *persistConn) bool {
	t.lk.Lock()
	defer t.lk.Unlock()
	if t.DisableKeepAlives || t.MaxIdleConnsPerHost < 0 {
		pconn.close()
		return false
	}
	if pconn.isBroken() {
		return false
	}
	key := pconn.cacheKey
	max := t.MaxIdleConnsPerHost
	if max == 0 {
		max = DefaultMaxIdleConnsPerHost
	}
	if len(t.idleConn[key]) >= max {
		pconn.close()
		return false
	}
	t.idleConn[key] = append(t.idleConn[key], pconn)
	return true
}

func (t *Transport) getConn(cm *connectMethod) (*persistConn, error) {
	if pc := t.getIdleConn(cm); pc != nil {
		return pc, nil
	}

	conn, raddr, ip, err := t.dial("tcp", cm.addr())
	if err != nil {
		if cm.proxyURL != nil {
			err = fmt.Errorf("http: error connecting to proxy %s: %v", cm.proxyURL, err)
		}
		return nil, err
	}

	pa := cm.proxyAuth()

	pconn := &persistConn{
		t:        t,
		cacheKey: cm.String(),
		conn:     conn,
		reqch:    make(chan requestAndChan, 50),
		host:     raddr,
		ip:       ip,
	}

	switch {
	case cm.proxyURL == nil:
	case cm.targetSchema == "http":
		pconn.isProxy = true
		if pa != "" {
			pconn.mutateHeaderFunc = func(h http.Header) {
				h.Set("Proxy-Authorization", pa)
			}
		}
	case cm.targetSchema == "https":
		connectReq := &http.Request{
			Method: "CONNECT",
			URL:    &url.URL{Opaque: cm.targetAddr},
			Host:   cm.targetAddr,
			Header: make(http.Header),
		}
		if pa != "" {
			connectReq.Header.Set("Proxy-Authorization", pa)
		}
		connectReq.Write(conn)
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, connectReq)
		if err != nil {
			conn.Close()
			return nil, err
		}
		if resp.StatusCode != 200 {
			f := strings.SplitN(resp.Status, " ", 2)
			conn.Close()
			return nil, errors.New(f[1])
		}
	}

	if cm.targetSchema == "https" {
		conn = tls.Client(conn, t.TLSClientConfig)
		if err = conn.(*tls.Conn).Handshake(); err != nil {
			return nil, err
		}
		if t.TLSClientConfig == nil || !t.TLSClientConfig.InsecureSkipVerify {
			if err = conn.(*tls.Conn).VerifyHostname(cm.tlsHost()); err != nil {
				return nil, err
			}
		}

	}
	pconn.br = bufio.NewReader(pconn.conn)
	pconn.bw = bufio.NewWriter(pconn.conn)
	go pconn.readLoop()
	return pconn, nil
}

func (t *Transport) DetailedRoundTrip(req *http.Request) (details *RoundTripDetails, resp *http.Response, err error) {
	if req.URL == nil {
		return nil, nil, errors.New("http: nil Request.URL")
	}
	if req.Header == nil {
		return nil, nil, errors.New("http: nil Request.Header")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		t.lk.Lock()
		var rt RoundTripper
		if t.altProto != nil {
			rt = t.altProto[req.URL.Scheme]
		}
		t.lk.Unlock()
		if rt == nil {
			return nil, nil, &badStringError{"unsupported protocol scheme", req.URL.Scheme}
		}
		return rt.DetailedRoundTrip(req)
	}
	treq := &transportRequest{Request: req}
	cm, err := t.connectMethodForRequest(treq)
	if err != nil {
		return nil, nil, err
	}

	pconn, err := t.getConn(cm)
	if err != nil {
		return nil, nil, err
	}

	resp, err = pconn.roundTrip(treq)
	return &RoundTripDetails{pconn.host, pconn.ip, pconn.isProxy, err}, resp, err

}

func getenvEitherCase(k string) string {
	if v := os.Getenv(strings.ToUpper(k)); v != "" {
		return v
	}
	return os.Getenv(strings.ToLower(k))
}

var portMap = map[string]string{
	"http":  "80",
	"https": "443",
}

func canonicalAddr(url *url.URL) string {
	addr := url.Host
	if !hasPort(addr) {
		return addr + ":" + portMap[url.Scheme]
	}
	return addr
}

func useProxy(addr string) bool {
	if len(addr) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return false
		}
	}

	no_proxy := getenvEitherCase("NO_PROXY")
	if no_proxy == "*" {
		return false
	}

	addr = strings.ToLower(strings.TrimSpace(addr))
	if hasPort(addr) {
		addr = addr[:strings.LastIndex(addr, ":")]
	}

	for _, p := range strings.Split(no_proxy, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if len(p) == 0 {
			continue
		}
		if hasPort(p) {
			p = p[:strings.LastIndex(p, ":")]
		}
		if addr == p || (p[0] == '.' && (strings.HasSuffix(addr, p) || addr == p[1:])) {
			return false
		}
	}

	return true
}

func ProxyFromEnvironment(req *http.Request) (*url.URL, error) {
	proxy := getenvEitherCase("HTTP_PROXY")
	if proxy == "" {
		return nil, nil
	}
	if !useProxy(canonicalAddr(req.URL)) {
		return nil, nil
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil || proxyURL.Scheme == "" {
		if u, err := url.Parse("http://" + proxy); err == nil {
			proxyURL = u
			err = nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("invalid proxy address %q: %v", proxy, err)
	}
	return proxyURL, nil
}
