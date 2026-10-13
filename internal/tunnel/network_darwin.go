//go:build darwin

package tunnel

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

func readSystemProxy(ctx context.Context) (systemProxy, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return systemProxy{}, errors.New("system_proxy_unavailable")
	}
	return parseSCUtilProxy(string(data)), nil
}
