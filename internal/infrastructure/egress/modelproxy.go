package egress

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
)

// Environment a native agent container reads to send its model provider
// requests through the proxy.
const (
	ModelProxyEnv      = "ARCHIE_MODEL_PROXY"
	ModelProxyHostsEnv = "ARCHIE_MODEL_PROXY_HOSTS"
	ModelProxyCAEnv    = "ARCHIE_MODEL_PROXY_CA"
)

// ModelProxyURL is the proxy address a native container logs in to with its
// run credential.
func ModelProxyURL(addr, token string) string {
	return (&url.URL{Scheme: "http", User: url.UserPassword(proxyUser, token), Host: addr}).String()
}

// ModelTransport is an http.Transport that sends requests for hosts through
// proxyURL, trusting the proxy's CA, and everything else directly. Only the
// model provider hosts go through the proxy, so the rest of the agent's
// traffic is unchanged.
func ModelTransport(proxyURL string, hosts []string, caFile string) (*http.Transport, error) {
	proxy, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("model proxy url: %w", err)
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("model proxy ca: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("model proxy ca: no certificate in " + caFile)
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("model proxy: the default transport is not an *http.Transport")
	}
	t := base.Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	t.Proxy = func(r *http.Request) (*url.URL, error) {
		if slices.Contains(hosts, normalizeHost(r.URL.Hostname())) {
			return proxy, nil
		}
		return nil, nil
	}
	return t, nil
}

// ProviderHost is the host a provider's requests go to: its base URL's, or
// its class's default. An unknown class without a base URL has none, and
// cannot be served through the proxy.
func ProviderHost(class, baseURL string) (string, error) {
	if baseURL == "" {
		baseURL = classBaseURL[class]
	}
	if baseURL == "" {
		return "", fmt.Errorf("provider class %q needs a base_url", class)
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("provider base_url %q has no host", baseURL)
	}
	return normalizeHost(u.Hostname()), nil
}

// classBaseURL mirrors the default base URL each ai-sdk provider class uses
// when a provider sets none.
var classBaseURL = map[string]string{
	"openai":       "https://api.openai.com",
	"openaiobject": "https://api.openai.com",
	"anthropic":    "https://api.anthropic.com",
	"cohere":       "https://api.cohere.com",
	"deepseek":     "https://api.deepseek.com",
	"gemini":       "https://generativelanguage.googleapis.com",
	"groq":         "https://api.groq.com",
	"minimax":      "https://api.minimax.io",
	"mistral":      "https://api.mistral.ai",
	"perplexity":   "https://api.perplexity.ai",
	"xai":          "https://api.x.ai",
	"togetherai":   "https://api.together.xyz",
}

// ModelProxyEnvFor is the environment entry list naming the proxy, its hosts
// and the CA path inside the container.
func ModelProxyEnvFor(addr, token string, hosts []string, caPath string) []string {
	return []string{
		ModelProxyEnv + "=" + ModelProxyURL(addr, token),
		ModelProxyHostsEnv + "=" + strings.Join(hosts, ","),
		ModelProxyCAEnv + "=" + caPath,
	}
}

// ParseHosts splits ModelProxyHostsEnv's value.
func ParseHosts(value string) []string {
	var hosts []string
	for h := range strings.SplitSeq(value, ",") {
		if h = normalizeHost(strings.TrimSpace(h)); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}
