package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBlockedIP(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.9", "192.168.1.1", "169.254.169.254", "0.0.0.0", "100.64.1.1", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "224.0.0.1", "255.255.255.255", "198.18.0.1"} {
		if !blockedIP(net.ParseIP(ip)) {
			t.Errorf("%s was allowed", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"} {
		if blockedIP(net.ParseIP(ip)) {
			t.Errorf("%s was blocked", ip)
		}
	}
}

func TestParseWebURL(t *testing.T) {
	for _, ok := range []string{"https://example.com", "http://example.com/a?b=c", "https://example.com:443/x"} {
		if _, err := parseWebURL(ok, false); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ftp://example.com", "file:///etc/passwd", "javascript:alert(1)", "https://user:pw@example.com", "https://example.com:8080", "//example.com", "https://", "not a url"} {
		if _, err := parseWebURL(bad, false); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := parseWebURL("https://example.com:8443", true); err != nil {
		t.Errorf("any port should be allowed when asked: %v", err)
	}
}

func TestGuardBlocksLoopbackAndRedirectsToIt(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer target.Close()
	strict := guard{}.client(3 * time.Second)
	if resp, err := strict.Get(target.URL); err == nil {
		resp.Body.Close()
		t.Fatal("the guard let a loopback address through")
	} else if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
	// a public-looking page cannot bounce the server onto an internal one either: the redirect target is dialled through the same guard
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer hop.Close()
	open := guard{allowPrivate: true}.client(3 * time.Second)
	if resp, err := open.Get(target.URL); err != nil {
		t.Fatalf("allowPrivate should connect: %v", err)
	} else {
		resp.Body.Close()
	}
	if resp, err := strict.Get(hop.URL); err == nil {
		resp.Body.Close()
		t.Fatal("redirect chain reached a private address")
	}
}
