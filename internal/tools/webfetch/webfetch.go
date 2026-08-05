// Package webfetch retrieves a URL on the model's behalf and returns readable
// text.
//
// The interesting part is not the fetch, it is the refusals. archied runs on a
// host where the Docker API, NATS and its own dashboard answer on private
// addresses, and the URL it is asked to fetch arrives in chat -- an untrusted
// path, since a forge issue body or an inbound email can end up quoted into a
// turn. So the address is vetted at dial time rather than by inspecting the
// URL: a name that looks public can resolve to a private address, and only the
// resolved address tells the truth.
package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	// maxRedirects bounds a redirect chain. Each hop is re-vetted by the
	// dialer, so this only stops a loop from spending the timeout.
	maxRedirects = 5

	// statusExcerpt is how much of a failed response body is quoted back.
	// Enough to tell an API error message from an HTML error page.
	statusExcerpt = 200
)

// Config controls the fetcher. The zero value is disabled.
type Config struct {
	// Enabled advertises the tool. When false, Tool returns nil so the
	// model is never shown a capability the operator turned off.
	Enabled bool

	// Timeout bounds one fetch, redirects included.
	Timeout time.Duration

	// MaxBytes bounds how much of a response body is read.
	MaxBytes int64

	// AllowPrivateNetworks permits loopback, private, link-local and
	// carrier-grade-NAT addresses. Off by default: leaving it off is what
	// keeps the daemon's own control surfaces out of reach.
	AllowPrivateNetworks bool
}

// Client fetches URLs under a Config.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client whose transport refuses disallowed addresses at dial
// time.
func New(cfg Config) *Client {
	c := &Client{cfg: cfg}
	transport := &http.Transport{
		DialContext:           c.dialContext,
		TLSHandshakeTimeout:   cfg.Timeout,
		ResponseHeaderTimeout: cfg.Timeout,
		DisableKeepAlives:     true,
	}
	c.http = &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return checkScheme(req.URL)
		},
	}
	return c
}

// Fetch retrieves rawURL and returns its readable content. When raw is true
// the body is returned as received rather than reduced to text.
func (c *Client) Fetch(ctx context.Context, rawURL string, raw bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid url %q: %w", rawURL, err)
	}
	if err := checkScheme(parsed); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	// Some sites serve a different, poorer page to an unidentified client,
	// and a few refuse one outright.
	req.Header.Set("User-Agent", "archie-webfetch/1.0 (+https://github.com/samcharles93/archie-core)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.1")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", unwrapFetchError(err)
	}
	defer resp.Body.Close()

	body, truncated, err := c.readBody(resp)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s returned %d %s: %s",
			parsed.Redacted(), resp.StatusCode, http.StatusText(resp.StatusCode), excerpt(body))
	}

	mediaType := contentType(resp)
	rendered, err := render(mediaType, body, raw, len(body))
	if err != nil {
		return "", err
	}
	if truncated {
		rendered += fmt.Sprintf("\n\n[webfetch: response truncated at %d bytes]", c.cfg.MaxBytes)
	}
	return rendered, nil
}

// readBody reads at most MaxBytes, reporting whether more was available.
func (c *Client) readBody(resp *http.Response) (string, bool, error) {
	limit := c.cfg.MaxBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	// One byte past the limit distinguishes "exactly at the limit" from
	// "there was more".
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", false, fmt.Errorf("read response: %w", err)
	}
	if int64(len(data)) > limit {
		return string(data[:limit]), true, nil
	}
	return string(data), false, nil
}

// render turns a body into what the model sees, refusing anything that is not
// text. Returning binary would spend the context window on bytes the model
// cannot read.
func render(mediaType, body string, raw bool, size int) (string, error) {
	switch {
	case raw:
		return body, nil
	case mediaType == "text/html", mediaType == "application/xhtml+xml":
		return htmlToText(body), nil
	case strings.HasPrefix(mediaType, "text/"),
		mediaType == "application/json",
		mediaType == "application/xml",
		strings.HasSuffix(mediaType, "+json"),
		strings.HasSuffix(mediaType, "+xml"):
		return body, nil
	default:
		return "", fmt.Errorf("refusing %s (%d bytes): only text, html, json and xml are readable", mediaType, size)
	}
}

// contentType returns the media type without parameters, defaulting to plain
// text when the server sends nothing.
func contentType(resp *http.Response) string {
	raw := resp.Header.Get("Content-Type")
	if raw == "" {
		return "text/plain"
	}
	if i := strings.IndexByte(raw, ';'); i >= 0 {
		raw = raw[:i]
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

// excerpt quotes the start of a failed response so the model can tell an API
// error message from a generic error page.
func excerpt(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= statusExcerpt {
		return body
	}
	return body[:statusExcerpt] + "..."
}

// checkScheme allows only http and https. Anything else -- file:, data:,
// gopher: -- reaches something that is not a web server.
func checkScheme(u *url.URL) error {
	switch u.Scheme {
	case "http", "https":
		return nil
	case "":
		return fmt.Errorf("url %q has no scheme: give an absolute http:// or https:// url", u.String())
	default:
		return fmt.Errorf("refusing scheme %q: only http and https are fetched", u.Scheme)
	}
}

// unwrapFetchError strips the url.Error wrapper so the refusal the dialer or
// the redirect check produced is what the model reads, rather than being
// buried behind Go's transport prose.
func unwrapFetchError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

// dialContext resolves the host and refuses addresses the daemon must not be
// talked into reaching, then dials a vetted address directly.
//
// Vetting here rather than on the URL is the whole point: between a pre-flight
// check and the connection, a name can resolve to something else. Dialing the
// address that was actually approved closes that window.
func (c *Client) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: c.cfg.Timeout}
	var refused []string
	for _, ip := range ips {
		if !c.addressAllowed(ip.IP) {
			refused = append(refused, ip.IP.String())
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}

	if len(refused) > 0 {
		return nil, fmt.Errorf(
			"refusing to connect to %s: it resolves to the private or loopback address %s, which is where this host's own services live; set tools.web_fetch.allow_private_networks to permit it",
			host, strings.Join(refused, ", "))
	}
	return nil, fmt.Errorf("could not connect to %s", host)
}

// addressAllowed reports whether an address may be reached.
func (c *Client) addressAllowed(ip net.IP) bool {
	if c.cfg.AllowPrivateNetworks {
		return true
	}
	switch {
	case ip.IsLoopback(), ip.IsPrivate(), ip.IsUnspecified(),
		ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return false
	case isCarrierGradeNAT(ip):
		return false
	default:
		return true
	}
}

// isCarrierGradeNAT reports whether ip is in 100.64.0.0/10, which net.IP has
// no predicate for and which routes to infrastructure rather than the public
// internet.
func isCarrierGradeNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// blockedElements hold text that is markup machinery rather than content.
var blockedElements = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"head": true, "template": true, "svg": true,
}

// htmlToText reduces a document to the text a reader would see. It is
// deliberately simple: the goal is to spend the context window on prose
// instead of markup, not to reproduce a browser.
func htmlToText(body string) string {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		// Unparseable markup still has readable text in it; handing back
		// the raw document beats returning nothing.
		return body
	}

	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && blockedElements[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			if text := strings.TrimSpace(n.Data); text != "" {
				b.WriteString(text)
				b.WriteByte(' ')
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		// Block-level elements end a line so headings and paragraphs do not
		// run together into one wall of text.
		if n.Type == html.ElementNode && breaksLine(n.Data) {
			b.WriteByte('\n')
		}
	}
	walk(doc)

	return collapseBlankLines(b.String())
}

// breaksLine reports whether an element should end the current line.
func breaksLine(tag string) bool {
	switch tag {
	case "p", "div", "br", "li", "tr", "section", "article", "header", "footer",
		"h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre":
		return true
	default:
		return false
	}
}

// collapseBlankLines trims each line and squeezes runs of blank lines, which
// nested markup produces in quantity.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
