package main

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"io"
	"net/http"
	"runtime"
	"time"
)

// This opt-in public lookup sends no local identity, credentials or diagnostics.
// Installation is always a separate, reversible operation by the user.
func availableRelease(ctx context.Context) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/VeraPyuyi/DotControl/releases?per_page=5", nil)
	if err != nil {
		return nil, errors.New("update_check_failed")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "DotControl/"+model.Version)
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_rejected") }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("update_check_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("update_check_failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, errors.New("update_check_failed")
	}
	return selectRelease(body, model.Version, runtime.GOOS, runtime.GOARCH)
}
