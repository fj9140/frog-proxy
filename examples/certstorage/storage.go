package main

import (
	"crypto/tls"
	"sync"
)

type CertStorage struct {
	certs sync.Map
}

func (tcs *CertStorage) Fetch(host string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	var cert tls.Certificate
	icert, ok := tcs.certs.Load(host)
	if ok {
		cert = icert.(tls.Certificate)
	} else {
		certp, err := gen()
		if err != nil {
			return nil, err
		}
		cert = *certp
		tcs.certs.Store(host, cert)
	}
	return &cert, nil
}

func NewCertStorage() *CertStorage {
	tcs := &CertStorage{}
	tcs.certs = sync.Map{}
	return tcs
}
