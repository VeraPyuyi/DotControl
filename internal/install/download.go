package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type DownloadOptions struct {
	Root     string
	StateDir string
	URL      string
	SHA256   string
	// MaxBytes defaults to 2 GiB and includes any resumed prefix.
	MaxBytes int64
	// Client permits an application transport configuration. Redirects are
	// always checked for HTTPS, even when a caller provides a custom client.
	Client   *http.Client
	Progress func(Progress)
	Rollback func(context.Context, *Current) error
}

// Download resumes a SHA-256-pinned HTTPS archive inside Root/.downloads. The
// returned archive is fully verified. Extraction is deliberately separate:
// Apply accepts only an already extracted, manifest-verified local bundle.
func Download(ctx context.Context, o DownloadOptions) (result string, err error) {
	u, e := url.Parse(o.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", errors.New("download requires an HTTPS URL without embedded credentials")
	}
	sum, e := hex.DecodeString(o.SHA256)
	if e != nil || len(sum) != 32 || strings.ToLower(o.SHA256) != o.SHA256 {
		return "", errors.New("download requires a lowercase SHA-256 pin")
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 2 << 30
	}
	if o.MaxBytes < 1 {
		return "", errors.New("invalid download size limit")
	}
	root, e := preparePaths(o.Root, o.StateDir)
	if e != nil {
		return "", e
	}
	unlock, e := lockRoot(ctx, root)
	if e != nil {
		return "", e
	}
	defer unlock()
	own, e := loadOwnership(root)
	if e != nil {
		return "", e
	}
	if e = atomicJSON(filepath.Join(root, ownerName), own); e != nil {
		return "", e
	}
	if e = recoverInterrupted(ctx, root, o.Rollback); e != nil {
		return "", e
	}
	prior, e := ReadProgress(root)
	if e != nil && !os.IsNotExist(e) {
		return "", e
	}
	// Acquisition does not own lifecycle reference checks. Keep a committed
	// cleanup journal intact so the next Apply/Repair can resume it safely.
	preserveCleanup := e == nil && (prior.Phase == "cleaning_old_versions" || prior.Phase == "cleanup_pending")
	p := Progress{Phase: "download", UpdatedAt: time.Now().UTC()}
	if !preserveCleanup {
		if e = atomicJSON(filepath.Join(root, "transaction.json"), p); e != nil {
			return "", e
		}
	}
	if o.Progress != nil {
		o.Progress(p)
	}
	defer func() {
		if err != nil {
			p.Phase = "failed"
			p.Error = err.Error()
			p.UpdatedAt = time.Now().UTC()
			if !preserveCleanup {
				if e := atomicJSON(filepath.Join(root, "transaction.json"), p); e != nil {
					err = errors.Join(err, e)
				}
			}
			if o.Progress != nil {
				o.Progress(p)
			}
		}
	}()
	dir := filepath.Join(root, ".downloads")
	if e = checkAncestors(dir); e != nil {
		return "", e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	dest := filepath.Join(dir, o.SHA256+".bundle")
	part := filepath.Join(dir, o.SHA256+".part")
	if _, e = os.Lstat(dest); e == nil {
		if e = checkDownload(ctx, dest, o.SHA256, o.MaxBytes); e != nil {
			return "", e
		}
		return dest, nil
	} else if !os.IsNotExist(e) {
		return "", e
	}
	offset := int64(0)
	if _, e = os.Lstat(part); e == nil {
		info, e := openAndCloseRegular(part)
		if e != nil {
			return "", e
		}
		offset = info.Size()
		if offset > o.MaxBytes {
			return "", errors.Join(errors.New("partial download exceeds size limit"), discardPartial(part))
		}
		if e = checkDownload(ctx, part, o.SHA256, o.MaxBytes); e == nil {
			if e = os.Rename(part, dest); e != nil {
				return "", e
			}
			return dest, nil
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	client := http.Client{Timeout: 30 * time.Minute}
	if o.Client != nil {
		client = *o.Client
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("download redirect must remain HTTPS without credentials")
		}
		if len(via) >= 10 {
			return errors.New("too many download redirects")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, o.URL, nil)
	if e != nil {
		return "", e
	}
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, e := client.Do(req)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		// A complete but wrong prefix cannot be resumed. Removing only this owned
		// regular scratch file allows a subsequent attempt to start cleanly.
		if _, e = openAndCloseRegular(part); e == nil {
			_ = os.Remove(part)
		}
		return "", errors.New("server rejected download range; retry starts from zero")
	}
	expected := int64(-1)
	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0
	case http.StatusPartialContent:
		start, end, total, e := parseContentRange(resp.Header.Get("Content-Range"))
		if e != nil || start != offset || total > o.MaxBytes || end != total-1 {
			return "", errors.New("invalid download Content-Range")
		}
		expected = end - start + 1
		if resp.ContentLength >= 0 && resp.ContentLength != expected {
			return "", errors.New("download range length mismatch")
		}
	default:
		return "", fmt.Errorf("download HTTP status %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength > o.MaxBytes-offset {
		return "", errors.New("download exceeds size limit")
	}
	if e = checkAncestors(part); e != nil {
		return "", e
	}
	if _, e = os.Lstat(part); e == nil {
		if _, e = openAndCloseRegular(part); e != nil {
			return "", e
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	flag := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flag |= os.O_TRUNC
	} else {
		flag |= os.O_APPEND
	}
	out, e := os.OpenFile(part, flag, 0600)
	if e != nil {
		return "", e
	}
	count, e := io.Copy(out, io.LimitReader(&contextReader{ctx: ctx, r: resp.Body}, o.MaxBytes-offset+1))
	oversized := count > o.MaxBytes-offset
	if oversized {
		e = errors.Join(e, errors.New("download exceeds size limit"))
	}
	if e == nil && expected >= 0 && count != expected {
		e = io.ErrUnexpectedEOF
	}
	if e == nil {
		e = out.Sync()
	}
	closeErr := out.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		if oversized {
			e = errors.Join(e, discardPartial(part))
		}
		return "", e
	}
	if e = checkDownload(ctx, part, o.SHA256, o.MaxBytes); e != nil {
		if ctx.Err() == nil {
			_ = os.Remove(part)
		}
		return "", e
	}
	if e = os.Rename(part, dest); e != nil {
		return "", e
	}
	return dest, nil
}

// discardPartial only removes the validated regular scratch file. It refuses
// links and reports cleanup failure rather than leaving retry failure hidden.
func discardPartial(p string) error {
	if _, e := openAndCloseRegular(p); e != nil {
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	return os.Remove(p)
}

func checkDownload(ctx context.Context, p, digest string, max int64) error {
	f, e := openRegular(p)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if info.Size() > max {
		return errors.New("download exceeds size limit")
	}
	h := sha256.New()
	if _, e = io.Copy(h, &contextReader{ctx: ctx, r: f}); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("download SHA-256 mismatch")
	}
	return nil
}
func parseContentRange(s string) (int64, int64, int64, error) {
	if !strings.HasPrefix(s, "bytes ") {
		return 0, 0, 0, errors.New("invalid Content-Range")
	}
	bounds := strings.Split(strings.TrimPrefix(s, "bytes "), "/")
	if len(bounds) != 2 {
		return 0, 0, 0, errors.New("invalid Content-Range")
	}
	span := strings.Split(bounds[0], "-")
	if len(span) != 2 {
		return 0, 0, 0, errors.New("invalid Content-Range")
	}
	start, e := strconv.ParseInt(span[0], 10, 64)
	if e != nil {
		return 0, 0, 0, e
	}
	end, e := strconv.ParseInt(span[1], 10, 64)
	if e != nil {
		return 0, 0, 0, e
	}
	total, e := strconv.ParseInt(bounds[1], 10, 64)
	if e != nil || start < 0 || end < start || total <= end {
		return 0, 0, 0, errors.New("invalid Content-Range bounds")
	}
	return start, end, total, nil
}
