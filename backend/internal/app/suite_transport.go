package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

func suiteHTTPError(status int, code, message string) error {
	return &storage.SuiteError{Status: status, Code: code, Message: message, Retryable: status == 503}
}
func (s *Server) suiteSecureTransport(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	if r.Header.Get("X-Nox-Suite-Transport") != "https" || r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	for _, raw := range s.cfg.SuiteTrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err == nil && prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}
func (s *Server) suiteOrigin() (string, error) {
	origin, err := noxmcp.NormalizeSuiteOrigin(strings.TrimSpace(s.cfg.PublicURL))
	if err != nil {
		return "", suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Set NOX_BACKEND_PUBLIC_URL to the trusted HTTPS Backend origin before using Suite trust.")
	}
	return origin, nil
}
func (s *Server) approvedSuiteTarget(ctx context.Context, origin string) (*url.URL, []netip.Addr, error) {
	normalized, err := noxmcp.NormalizeSuiteOrigin(origin)
	if err != nil {
		return nil, nil, suiteHTTPError(400, "INVALID_REGISTRATION", "A valid HTTPS origin is required.")
	}
	target, err := url.Parse(normalized)
	if err != nil {
		return nil, nil, err
	}
	port := target.Port()
	if port == "" {
		port = "443"
	}
	approvedPort := false
	ports := s.cfg.SuiteAllowedPorts
	if len(ports) == 0 {
		ports = []string{"443"}
	}
	for _, value := range ports {
		if strings.TrimSpace(value) == port {
			approvedPort = true
		}
	}
	if !approvedPort {
		return nil, nil, suiteHTTPError(403, "FORBIDDEN", "The target port is outside the administrator-approved Suite policy.")
	}
	prefixes := []netip.Prefix{}
	for _, raw := range s.cfg.SuiteAllowedCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, nil, suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Invalid Suite target network policy.")
		}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) == 0 {
		return nil, nil, suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Configure NOX_BACKEND_SUITE_ALLOWED_CIDRS before verifying Suite services.")
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(target.Hostname()); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		dnsCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		addresses, err = net.DefaultResolver.LookupNetIP(dnsCtx, "ip", target.Hostname())
		if err != nil {
			return nil, nil, suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The approved product address could not be resolved.")
		}
	}
	if len(addresses) == 0 {
		return nil, nil, suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The approved product address has no usable targets.")
	}
	for _, address := range addresses {
		allowed := false
		address = address.Unmap()
		for _, prefix := range prefixes {
			if prefix.Contains(address) {
				allowed = true
				break
			}
		}
		if !allowed || !address.IsValid() || address.IsUnspecified() || address.IsMulticast() {
			return nil, nil, suiteHTTPError(403, "FORBIDDEN", "A resolved product target is outside the administrator-approved Suite network policy.")
		}
	}
	return target, addresses, nil
}
func (s *Server) fetchSuiteDeclaration(ctx context.Context, origin, pin string) (json.RawMessage, error) {
	target, _, err := s.approvedSuiteTarget(ctx, origin)
	if err != nil {
		return nil, err
	}
	wanted, err := base64.RawURLEncoding.DecodeString(pin)
	if err != nil || len(wanted) != 32 {
		return nil, suiteHTTPError(400, "INVALID_REGISTRATION", "Invalid proven TLS public-key fingerprint.")
	}
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second, MaxConnsPerHost: 1,
		TLSClientConfig: suitePinVerification(target.Hostname(), wanted),
	}
	defer transport.CloseIdleConnections()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// Resolve again for every connection and dial the approved IP directly. The
		// transport cannot re-resolve a validated hostname to an unchecked destination.
		_, addresses, err := s.approvedSuiteTarget(ctx, origin)
		if err != nil {
			return nil, err
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(host, target.Hostname()) {
			return nil, errors.New("unexpected Suite connection destination")
		}
		for _, ip := range addresses {
			conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("approved product target unreachable")
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, "GET", target.String()+"/nox/integration", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		var rejected *storage.SuiteError
		if errors.As(err, &rejected) {
			return nil, rejected
		}
		return nil, suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The product could not be verified over the proven HTTPS connection.")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, suiteHTTPError(403, "FORBIDDEN", "Suite declaration redirects are not followed.")
	}
	if response.StatusCode != 200 {
		return nil, suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The product declaration endpoint did not return success.")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, noxmcp.SuiteMaxPayloadBytes+1))
	if err != nil {
		return nil, suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The product declaration response was interrupted.")
	}
	if len(raw) > noxmcp.SuiteMaxPayloadBytes {
		return nil, suiteHTTPError(400, "INVALID_REGISTRATION", "The product declaration exceeds the contract limit.")
	}
	if _, err := noxmcp.LoadSuiteProductDeclaration(raw); err != nil {
		return nil, suiteDocumentError(err, "The product declaration is invalid.")
	}
	return raw, nil
}
func validateSuiteConfig(cfg Config) error {
	for _, values := range [][]string{cfg.SuiteAllowedCIDRs, cfg.SuiteTrustedProxyCIDRs} {
		for _, raw := range values {
			if _, err := netip.ParsePrefix(strings.TrimSpace(raw)); err != nil {
				return fmt.Errorf("invalid Suite CIDR configuration")
			}
		}
	}
	for _, raw := range cfg.SuiteAllowedPorts {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || value < 1 || value > 65535 {
			return fmt.Errorf("invalid Suite allowed port configuration")
		}
	}
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return fmt.Errorf("TLS certificate and key files must be configured together")
	}
	return nil
}

// Pairing intent can precede deployment/DNS readiness. It grants no membership
// and sends nothing to the product; full address validation runs on completion.
func (s *Server) suiteIntentTarget(origin string) error {
	normalized, err := noxmcp.NormalizeSuiteOrigin(origin)
	if err != nil {
		return suiteHTTPError(400, "INVALID_REGISTRATION", "A valid HTTPS origin is required.")
	}
	target, err := url.Parse(normalized)
	if err != nil {
		return err
	}
	port := target.Port()
	if port == "" {
		port = "443"
	}
	ports := s.cfg.SuiteAllowedPorts
	if len(ports) == 0 {
		ports = []string{"443"}
	}
	found := false
	for _, allowed := range ports {
		if strings.TrimSpace(allowed) == port {
			found = true
		}
	}
	if !found {
		return suiteHTTPError(403, "FORBIDDEN", "The target port is outside the approved Suite policy.")
	}
	if len(s.cfg.SuiteAllowedCIDRs) == 0 {
		return suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Configure the approved Suite network policy before pairing.")
	}
	for _, raw := range s.cfg.SuiteAllowedCIDRs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(raw)); err != nil {
			return suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Invalid approved Suite network policy.")
		}
	}
	if _, err := netip.ParseAddr(target.Hostname()); err == nil {
		_, _, err = s.approvedSuiteTarget(context.Background(), origin)
		return err
	}
	return nil
}

// suitePinVerification implements the explicit leaf-key trust mode after the
// pairing HMAC has authenticated the expected pin. VerifyConnection is mandatory
// on every handshake, including resumed connections; there is no unchecked mode.
// https://pkg.go.dev/crypto/tls#Config
func suitePinVerification(serverName string, wanted []byte) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName,
		// Pin trust comes only from the verified pairing HMAC. This callback replaces
		// PKI validation with exact leaf SPKI verification, never unchecked TLS.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("missing product TLS certificate")
			}
			leaf := state.PeerCertificates[0]
			now := time.Now()
			if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
				return errors.New("product TLS certificate is not currently valid")
			}
			actual := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
			if subtle.ConstantTimeCompare(actual[:], wanted) != 1 {
				return suiteHTTPError(409, "REGISTRATION_CONFLICT", "The product TLS key does not match the proven pairing identity. Reconcile before retrying.")
			}
			return nil
		}}
}
