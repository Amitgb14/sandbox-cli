package api

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// NewClientWithTLS is NewClient for an https endpoint reached with a TLS
// configuration the caller built: a private CA, a client certificate for
// mutual TLS, or both. It is how a gateway reaches its nodes, which answer
// only to the gateway's own certificate.
//
// The configuration is cloned, so the caller may not change it afterwards,
// and its minimum version is raised to TLS 1.2 if set lower.
func NewClientWithTLS(endpoint, token string, cfg *tls.Config) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q: TLS needs https://host[:port]", endpoint)
	}
	if cfg == nil {
		cfg = &tls.Config{}
	}
	cfg = cfg.Clone()
	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = cfg
	// A gateway sends many concurrent requests to few hosts; the default of two
	// idle connections per host would open and close one for nearly every one.
	tr.MaxIdleConnsPerHost = 64
	return &Client{base: strings.TrimSuffix(u.String(), "/"), token: token, http: &http.Client{Transport: tr}}, nil
}
