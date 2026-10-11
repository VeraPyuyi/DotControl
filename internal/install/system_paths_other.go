//go:build !darwin

package install

func normalizeSystemPath(p string) (string, error) { return p, nil }
