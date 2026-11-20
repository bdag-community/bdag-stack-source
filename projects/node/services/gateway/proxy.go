package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
)

var errLimitedUpstreamCredentials = errors.New("limited upstream credentials are incomplete or collide with primary credentials")
var errLimitedUpstreamTarget = errors.New("limited upstream target is not an authenticated native port")

func (g *Gateway) needsAuth(url string) bool {
	// Optimization: Check port suffix using pre-computed map
	idx := strings.LastIndex(url, ":")
	if idx == -1 {
		return false
	}
	return g.authPorts[url[idx+1:]]
}

func (g *Gateway) forwardCtx(ctx context.Context, url string, body []byte, role gatewayRole) ([]byte, error) {
	return g.forwardWithClient(ctx, g.client, url, body, role, true)
}

func (g *Gateway) forwardProbeCtx(ctx context.Context, url string, body []byte, role gatewayRole) ([]byte, error) {
	client := g.probeClient
	if client == nil {
		client = g.client
	}
	return g.forwardWithClient(ctx, client, url, body, role, false)
}

func (g *Gateway) forwardHealthProbeCtx(ctx context.Context, url string, body []byte, role gatewayRole) ([]byte, error) {
	return g.forwardProbeCtx(ctx, url, body, role)
}

func (g *Gateway) forwardWithClient(ctx context.Context, client *http.Client, url string, body []byte, role gatewayRole, reportFailure bool) ([]byte, error) {
	user, pass, err := g.outboundCredentials(role)
	if err != nil {
		return nil, err
	}
	if role == roleLimited && !g.needsAuth(url) {
		return nil, errLimitedUpstreamTarget
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-By", "bdag-gateway")
	if g.needsAuth(url) && user != "" {
		req.SetBasicAuth(user, pass)
	}
	if client == nil {
		client = http.DefaultClient
	}
	if role == roleLimited {
		// Do not let a verified limited request follow a redirect to an
		// unapproved host or port, where credentials could be stripped or
		// replayed outside the authenticated native surface.
		limitedClient := *client
		limitedClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
		client = &limitedClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if reportFailure {
			g.reportUpstreamFailure(url)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if reportFailure {
			g.reportUpstreamFailure(url)
		}
		return nil, fmt.Errorf("upstream RPC returned HTTP %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return io.ReadAll(resp.Body)
}

func (g *Gateway) outboundCredentials(role gatewayRole) (string, string, error) {
	if role == roleUnknown {
		return "", "", fmt.Errorf("unknown gateway forwarding role")
	}
	if (g.cfg.UpUser == "") != (g.cfg.UpPass == "") {
		return "", "", fmt.Errorf("upstream credentials must be provided as a complete pair")
	}
	user, pass := g.cfg.UpUser, g.cfg.UpPass
	if role == roleLimited {
		user, pass = g.cfg.UpLimitUser, g.cfg.UpLimitPass
		if !credentialsConfigured(user, pass) ||
			(credentialsConfigured(g.cfg.UpUser, g.cfg.UpPass) &&
				credentialsEqual(user, pass, g.cfg.UpUser, g.cfg.UpPass)) {
			return "", "", errLimitedUpstreamCredentials
		}
	}
	if (user == "") != (pass == "") {
		return "", "", fmt.Errorf("upstream credentials must be provided as a complete pair")
	}
	return user, pass, nil
}

func isLimitedForwardRejection(err error) bool {
	return errors.Is(err, errLimitedUpstreamCredentials) || errors.Is(err, errLimitedUpstreamTarget)
}

func (g *Gateway) reportUpstreamFailure(rawURL string) {
	if g.peerManager == nil {
		return
	}
	if parsed, err := neturl.Parse(rawURL); err == nil {
		g.peerManager.ReportFailure(parsed.Hostname())
	}
}
