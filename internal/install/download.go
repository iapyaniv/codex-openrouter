package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"codex-openrouter/internal/distribution"
)

func download(ctx context.Context, target distribution.Target, destination string, transport http.RoundTripper) error {
	initial, err := url.Parse(target.Archive.URL)
	if err != nil || !allowedURL(initial, []string{initialHost(initial)}) {
		return errors.New("embedded artifact URL is not permitted")
	}
	if transport == nil {
		owned := &http.Transport{
			Proxy: http.ProxyFromEnvironment, DisableCompression: true,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout: 30 * time.Second, MaxIdleConns: 2, MaxConnsPerHost: 2,
		}
		defer owned.CloseIdleConnections()
		transport = owned
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Minute}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > 5 || !allowedURL(request.URL, target.Archive.RedirectHosts) {
			return errors.New("artifact redirect is not permitted")
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, initial.String(), nil)
	if err != nil {
		return errors.New("cannot prepare artifact request")
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Transport errors can contain proxy credentials or signed redirect URLs.
		return errors.New("artifact download failed; check the network or proxy and retry")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("artifact download returned HTTP %d; retry later", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cannot create staged artifact: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, target.Archive.Bytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("artifact download was interrupted; retry")
	}
	if n != target.Archive.Bytes || hex.EncodeToString(digest.Sum(nil)) != target.Archive.SHA256 {
		return errors.New("artifact length or SHA-256 does not match the embedded manifest")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

func initialHost(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Hostname()
}

func allowedURL(value *url.URL, hosts []string) bool {
	if value == nil || value.Scheme != "https" || value.User != nil || (value.Port() != "" && value.Port() != "443") || value.Fragment != "" {
		return false
	}
	for _, host := range hosts {
		if strings.EqualFold(value.Hostname(), host) && host != "" {
			return true
		}
	}
	return false
}
