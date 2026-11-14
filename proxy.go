package main

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func requestHTTPS(r *http.Request) bool {
	if r.TLS != nil || os.Getenv("PUBLIC_HTTPS") == "1" {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	for _, value := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		value = strings.TrimSpace(value)
		parsed := net.ParseIP(value)
		_, block, e := net.ParseCIDR(value)
		if parsed != nil && parsed.Equal(ip) || e == nil && block.Contains(ip) {
			if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
				return true
			}
		}
	}
	// Retain the existing browser TLS-termination workflow, with host validation.
	origin, e := url.Parse(r.Header.Get("Origin"))
	return e == nil && origin.Scheme == "https" && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == "" && strings.EqualFold(origin.Host, r.Host)
}
