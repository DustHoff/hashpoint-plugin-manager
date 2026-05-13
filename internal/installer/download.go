package installer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/httpx"
)

// download fetches url into dest, refusing anything larger than maxBytes.
// The size enforcement reads one byte past the limit so we can tell
// "exactly at limit" from "exceeds limit". Transient errors are
// wrapped sdk.ErrTransient so the host can route to retry-UI.
func download(ctx context.Context, client *http.Client, url, dest string, maxBytes int64) error {
	if err := httpx.AssertWhitelisted(url); err != nil {
		return fmt.Errorf("download: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download: build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: download %s: %v", sdk.ErrTransient, url, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: download %s: %s", sdk.ErrTransient, url, resp.Status)
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: download %s: %s", sdk.ErrTransient, url, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("download %s: not found", url)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %v", dest, err)
	}
	defer f.Close()

	n, err := io.Copy(f, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("%w: write %s: %v", sdk.ErrTransient, dest, err)
	}
	if n > maxBytes {
		return fmt.Errorf("download %s: response exceeds %d bytes", url, maxBytes)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %v", dest, err)
	}
	return nil
}
