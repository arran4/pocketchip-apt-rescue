package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type logLevel int

const (
	logQuiet logLevel = iota
	logWarn
	logInfo
	logDebug
)

type config struct {
	listenAddr          string
	upstreamProxy       string
	timeout             time.Duration
	requestTimeout      time.Duration
	logLevelName        string
	logLevel            logLevel
	dumpRequests        bool
	insecureTLS         bool
	allowConnect        bool
	httpsUpstream       bool
	forceHTTPSAll       bool
	rewriteKnownRepos   bool
	blockKnownDeadRepos bool
	stripRange          bool
	userAgent           string
}

type proxyHandler struct {
	cfg    config
	client *http.Client
}

type rewriteResult struct {
	Changed bool
	Rule    string
	Before  string
	After   string
}

func main() {
	cfg := loadConfig()

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   cfg.timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.timeout,
		ResponseHeaderTimeout: cfg.timeout,
		ExpectContinueTimeout: 5 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.insecureTLS, //nolint:gosec
		},
	}

	if cfg.upstreamProxy != "" {
		u, err := url.Parse(cfg.upstreamProxy)
		if err != nil {
			log.Fatalf("invalid upstream proxy URL: %v", err)
		}
		transport.Proxy = http.ProxyURL(u)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Return redirects to the PocketCHIP after rewriting Location headers.
			// This keeps the old client talking to this proxy over plain HTTP.
			return http.ErrUseLastResponse
		},
	}

	handler := &proxyHandler{cfg: cfg, client: client}

	log.Printf("pocketchip-apt-rescue listening on %s", cfg.listenAddr)
	log.Printf("https_upstream=%v force_https_all=%v rewrite_known_repos=%v strip_range=%v block_known_dead_repos=%v allow_connect=%v log_level=%s timeout=%s request_timeout=%s",
		cfg.httpsUpstream,
		cfg.forceHTTPSAll,
		cfg.rewriteKnownRepos,
		cfg.stripRange,
		cfg.blockKnownDeadRepos,
		cfg.allowConnect,
		cfg.logLevelName,
		cfg.timeout,
		cfg.requestTimeout,
	)

	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
	}

	log.Fatal(server.ListenAndServe())
}

func loadConfig() config {
	var cfg config

	flag.StringVar(&cfg.listenAddr, "listen", envString("POCKETCHIP_PROXY_LISTEN", ":3142"), "listen address")
	flag.StringVar(&cfg.upstreamProxy, "upstream-proxy", envString("POCKETCHIP_PROXY_UPSTREAM", ""), "optional upstream proxy URL")
	flag.StringVar(&cfg.logLevelName, "log-level", envString("POCKETCHIP_PROXY_LOG_LEVEL", "warn"), "quiet, warn, info, debug")
	flag.BoolVar(&cfg.dumpRequests, "dump-requests", envBool("POCKETCHIP_PROXY_DUMP_REQUESTS", false), "dump outbound HTTP request headers")
	flag.BoolVar(&cfg.insecureTLS, "insecure-tls", envBool("POCKETCHIP_PROXY_INSECURE_TLS", false), "skip upstream TLS verification")
	flag.BoolVar(&cfg.allowConnect, "allow-connect", envBool("POCKETCHIP_PROXY_ALLOW_CONNECT", false), "allow CONNECT tunnelling")
	flag.BoolVar(&cfg.httpsUpstream, "https-upstream", envBool("POCKETCHIP_PROXY_HTTPS_UPSTREAM", true), "fetch known HTTPS-capable upstream repositories over HTTPS")
	flag.BoolVar(&cfg.forceHTTPSAll, "force-https-all", envBool("POCKETCHIP_PROXY_FORCE_HTTPS_ALL", false), "force HTTPS for all non-private upstream hosts; may break mirrors with mismatched certificates")
	flag.BoolVar(&cfg.rewriteKnownRepos, "rewrite-known-repos", envBool("POCKETCHIP_PROXY_REWRITE_KNOWN_REPOS", true), "rewrite known dead Jessie/PocketCHIP repo URLs")
	flag.BoolVar(&cfg.blockKnownDeadRepos, "block-known-dead-repos", envBool("POCKETCHIP_PROXY_BLOCK_DEAD_REPOS", false), "return helpful errors for known dead repos instead of fetching")
	flag.BoolVar(&cfg.stripRange, "strip-range", envBool("POCKETCHIP_PROXY_STRIP_RANGE", true), "strip Range and If-Range from upstream requests to avoid stale partial-cache 416 responses")
	flag.StringVar(&cfg.userAgent, "user-agent", envString("POCKETCHIP_PROXY_USER_AGENT", "pocketchip-apt-rescue/0.1"), "upstream User-Agent")

	timeout, err := time.ParseDuration(envString("POCKETCHIP_PROXY_TIMEOUT", "25s"))
	if err != nil {
		log.Fatalf("invalid POCKETCHIP_PROXY_TIMEOUT: %v", err)
	}
	cfg.timeout = timeout

	requestTimeout, err := time.ParseDuration(envString("POCKETCHIP_PROXY_REQUEST_TIMEOUT", "10m"))
	if err != nil {
		log.Fatalf("invalid POCKETCHIP_PROXY_REQUEST_TIMEOUT: %v", err)
	}
	cfg.requestTimeout = requestTimeout

	flag.Parse()

	lvl, err := parseLogLevel(cfg.logLevelName)
	if err != nil {
		log.Fatalf("invalid log level %q: %v", cfg.logLevelName, err)
	}
	cfg.logLevel = lvl
	cfg.logLevelName = strings.ToLower(strings.TrimSpace(cfg.logLevelName))

	return cfg
}

func (p *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method == http.MethodConnect {
		if !p.cfg.allowConnect {
			p.logWarn("CONNECT denied host=%s", r.Host)
			http.Error(w, "CONNECT disabled; this proxy is intended for plain HTTP APT clients", http.StatusMethodNotAllowed)
			return
		}
		p.handleConnect(w, r)
		return
	}

	target, err := requestTargetURL(r)
	if err != nil {
		p.logWarn("bad_request remote=%s method=%s host=%s uri=%q err=%v", r.RemoteAddr, r.Method, r.Host, r.RequestURI, err)
		http.Error(w, "bad proxy request: "+err.Error(), http.StatusBadRequest)
		return
	}

	original := cloneURL(target)
	rewrite := rewriteResult{Changed: false, Rule: "none", Before: original.String(), After: target.String()}

	if p.cfg.rewriteKnownRepos {
		rewrite = rewriteKnownRepoURL(target)
	}

	if p.cfg.blockKnownDeadRepos && isKnownDeadRepo(original) {
		p.logWarn("blocked_dead_repo original=%s rewritten=%s rule=%s", original.String(), target.String(), rewrite.Rule)
		http.Error(w, deadRepoMessage(original, target), http.StatusGone)
		return
	}

	if p.cfg.httpsUpstream && shouldUseHTTPSUpstream(target, p.cfg.forceHTTPSAll) {
		before := target.String()
		target.Scheme = "https"
		if before != target.String() && !rewrite.Changed {
			rewrite = rewriteResult{Changed: true, Rule: "http-to-https", Before: before, After: target.String()}
		}
	}

	if rewrite.Changed {
		p.logInfo("rewrite rule=%s before=%s after=%s", rewrite.Rule, rewrite.Before, target.String())
	}

	p.logDebug("request remote=%s method=%s original=%s upstream=%s", r.RemoteAddr, r.Method, original.String(), target.String())

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		http.Error(w, "failed to build upstream request: "+err.Error(), http.StatusBadRequest)
		return
	}

	copyHeaders(outReq.Header, r.Header)
	removeHopHeaders(outReq.Header)

	outReq.Host = target.Host
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Accept-Encoding")

	if p.cfg.stripRange {
		outReq.Header.Del("Range")
		outReq.Header.Del("If-Range")
	}

	if p.cfg.userAgent != "" {
		outReq.Header.Set("User-Agent", p.cfg.userAgent)
	}

	if p.cfg.dumpRequests || p.cfg.logLevel >= logDebug {
		if dump, err := httputil.DumpRequestOut(outReq, false); err == nil {
			p.logDebug("outbound_request:\n%s", string(dump))
		}
	}

	resp, err := p.client.Do(outReq)
	if err != nil {
		p.logWarn("upstream_error original=%s upstream=%s err=%v", original.String(), target.String(), err)
		http.Error(w, "upstream fetch failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	removeHopHeaders(w.Header())

	if p.cfg.stripRange && resp.StatusCode == http.StatusPartialContent {
		w.Header().Del("Content-Range")
	}

	if loc := w.Header().Get("Location"); loc != "" {
		rewritten := rewriteLocationForOldAPT(loc)
		if rewritten != loc {
			p.logInfo("location_rewrite from=%s to=%s", loc, rewritten)
			w.Header().Set("Location", rewritten)
		}
	}

	w.Header().Set("X-PocketCHIP-APT-Rescue-Original-URL", original.String())
	w.Header().Set("X-PocketCHIP-APT-Rescue-Upstream-URL", target.String())

	w.WriteHeader(resp.StatusCode)
	bytesCopied, copyErr := io.Copy(w, resp.Body)

	if resp.StatusCode >= 400 {
		p.logWarn("response status=%d bytes=%d duration=%s original=%s upstream=%s", resp.StatusCode, bytesCopied, time.Since(start).Truncate(time.Millisecond), original.String(), target.String())
	} else {
		p.logDebug("response status=%d bytes=%d duration=%s original=%s upstream=%s", resp.StatusCode, bytesCopied, time.Since(start).Truncate(time.Millisecond), original.String(), target.String())
	}

	if copyErr != nil {
		p.logWarn("copy_error upstream=%s err=%v", target.String(), copyErr)
	}
}

func requestTargetURL(r *http.Request) (*url.URL, error) {
	if r.URL != nil && r.URL.IsAbs() {
		u := cloneURL(r.URL)
		if u.Scheme == "" {
			u.Scheme = "http"
		}
		return u, nil
	}

	if r.Host == "" || r.URL == nil {
		return nil, errors.New("missing Host or URL")
	}

	return &url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}, nil
}

func rewriteKnownRepoURL(u *url.URL) rewriteResult {
	before := u.String()
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	path := cleanPath(u.Path)
	rule := "none"

	switch {
	case isSecurityDebianHost(host):
		u.Host = "archive.debian.org"
		u.Scheme = "https"
		u.Path = securityArchivePath(path)
		rule = "debian-security-to-archive"

	case isDebianMirrorHost(host):
		u.Host = "archive.debian.org"
		u.Scheme = "https"
		u.Path = debianArchivePath(path)
		rule = "debian-mirror-to-archive"

	case host == "opensource.nextthing.co":
		u.Host = "chip.jfpossibilities.com"
		// chip.jfpossibilities.com currently serves a certificate for other names.
		// Use HTTP upstream for this mirror by default.
		u.Scheme = "http"
		u.Path = nextThingArchivePath(path)
		rule = "nextthing-to-jfpossibilities"

	case host == "chip.jfpossibilities.com":
		// chip.jfpossibilities.com is useful, but its HTTPS certificate may not match.
		u.Scheme = "http"
		u.Path = collapseSlashes(path)
		rule = "jfpossibilities-http"

	case host == "archive.debian.org":
		if strings.HasPrefix(path, "/dists/") {
			u.Path = debianArchivePath(path)
			rule = "archive-path-normalise"
		} else {
			u.Path = collapseSlashes(path)
		}
	}

	u.Path = collapseSlashes(u.Path)
	after := u.String()
	changed := before != after
	if changed && rule == "none" {
		rule = "generic"
	}

	return rewriteResult{Changed: changed, Rule: rule, Before: before, After: after}
}

func isSecurityDebianHost(host string) bool {
	return host == "security.debian.org"
}

func isDebianMirrorHost(host string) bool {
	if host == "archive.debian.org" || host == "security.debian.org" {
		return false
	}

	switch host {
	case "http.debian.net", "deb.debian.org", "ftp.debian.org", "httpredir.debian.org", "cdn-fastly.deb.debian.org":
		return true
	}

	return strings.HasPrefix(host, "ftp.") && strings.HasSuffix(host, ".debian.org")
}

func debianArchivePath(path string) string {
	path = cleanPath(path)
	if strings.HasPrefix(path, "/debian/") || path == "/debian" {
		return path
	}
	if strings.HasPrefix(path, "/dists/") || strings.HasPrefix(path, "/pool/") || path == "/project/trace" || strings.HasPrefix(path, "/project/") {
		return "/debian" + path
	}
	return path
}

func securityArchivePath(path string) string {
	path = cleanPath(path)
	if strings.HasPrefix(path, "/debian-security/") || path == "/debian-security" {
		return path
	}
	if strings.HasPrefix(path, "/dists/") || strings.HasPrefix(path, "/pool/") {
		return "/debian-security" + path
	}
	return "/debian-security" + path
}

func nextThingArchivePath(path string) string {
	path = cleanPath(path)

	if strings.HasPrefix(path, "/chip/debian/repo/") || path == "/chip/debian/repo" {
		return path
	}

	if strings.HasPrefix(path, "/chip/debian/pocketchip/") || path == "/chip/debian/pocketchip" {
		return path
	}

	if strings.HasPrefix(path, "/chip/debian/") {
		return path
	}

	if strings.HasPrefix(path, "/dists/") || strings.HasPrefix(path, "/pool/") {
		return "/chip/debian/repo" + path
	}

	return path
}

func isKnownDeadRepo(u *url.URL) bool {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	path := cleanPath(u.Path)

	if host == "opensource.nextthing.co" {
		return true
	}

	if host == "security.debian.org" && strings.Contains(path, "/jessie/updates") {
		return true
	}

	if isDebianMirrorHost(host) && strings.Contains(path, "jessie") {
		return true
	}

	return false
}

func deadRepoMessage(original, rewritten *url.URL) string {
	return fmt.Sprintf(
		"known dead/obsolete PocketCHIP source\noriginal: %s\nsuggested rewritten upstream: %s\n\nRun with POCKETCHIP_PROXY_BLOCK_DEAD_REPOS=false to rewrite automatically.\n",
		original.String(),
		rewritten.String(),
	)
}

func shouldUseHTTPSUpstream(u *url.URL, forceAll bool) bool {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || isPrivateOrLocalHost(host) {
		return false
	}

	if forceAll {
		return true
	}

	switch host {
	case "archive.debian.org", "deb.debian.org", "security.debian.org":
		return true
	default:
		return false
	}
}

func isPrivateOrLocalHost(host string) bool {
	if host == "localhost" || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "10.") || strings.HasPrefix(host, "192.168.") {
		return true
	}

	if strings.HasPrefix(host, "172.") {
		parts := strings.Split(host, ".")
		if len(parts) >= 2 {
			second, err := strconv.Atoi(parts[1])
			if err == nil && second >= 16 && second <= 31 {
				return true
			}
		}
	}

	return false
}

func rewriteLocationForOldAPT(loc string) string {
	parsed, err := url.Parse(loc)
	if err != nil || parsed.Host == "" {
		if strings.HasPrefix(loc, "https://") {
			return "http://" + strings.TrimPrefix(loc, "https://")
		}
		return loc
	}

	rewriteKnownRepoURL(parsed)

	// Return plain HTTP to the PocketCHIP; the next request will come back through this proxy.
	if parsed.Scheme == "https" {
		parsed.Scheme = "http"
	}

	return parsed.String()
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func removeHopHeaders(h http.Header) {
	for _, key := range []string{
		"Connection",
		"Proxy-Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		h.Del(key)
	}
}

func cleanPath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func collapseSlashes(path string) string {
	if path == "" {
		return "/"
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func cloneURL(u *url.URL) *url.URL {
	v := *u
	return &v
}

func (p *proxyHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	p.logInfo("CONNECT host=%s", r.Host)

	destConn, err := net.DialTimeout("tcp", r.Host, p.cfg.timeout)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer destConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer clientConn.Close()

	_, _ = fmt.Fprint(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n")

	errc := make(chan error, 2)

	go func() {
		_, err := io.Copy(destConn, clientConn)
		errc <- err
	}()

	go func() {
		_, err := io.Copy(clientConn, destConn)
		errc <- err
	}()

	<-errc
}

func (p *proxyHandler) logWarn(format string, args ...any) {
	if p.cfg.logLevel >= logWarn {
		log.Printf(format, args...)
	}
}

func (p *proxyHandler) logInfo(format string, args ...any) {
	if p.cfg.logLevel >= logInfo {
		log.Printf(format, args...)
	}
}

func (p *proxyHandler) logDebug(format string, args ...any) {
	if p.cfg.logLevel >= logDebug {
		log.Printf(format, args...)
	}
}

func parseLogLevel(value string) (logLevel, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "quiet", "none", "off":
		return logQuiet, nil
	case "warn", "warning":
		return logWarn, nil
	case "info":
		return logInfo, nil
	case "debug", "trace":
		return logDebug, nil
	default:
		return logInfo, fmt.Errorf("expected quiet, warn, info, or debug")
	}
}

func envString(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if value == "" {
		return fallback
	}

	if b, err := strconv.ParseBool(value); err == nil {
		return b
	}

	switch value {
	case "yes", "y", "on", "1":
		return true
	case "no", "n", "off", "0":
		return false
	default:
		return fallback
	}
}
