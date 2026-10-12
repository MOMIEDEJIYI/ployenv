package netx

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
)

var (
	partialMu sync.Mutex
	partials  = map[string]*os.File{}
)

// registerPartial records an in-progress download. f may be nil (e.g. when the
// download is delegated to curl). Abort closes it before deleting, because
// Windows refuses to remove a file that is still open.
func registerPartial(path string, f *os.File) {
	partialMu.Lock()
	partials[path] = f
	partialMu.Unlock()
}

func unregisterPartial(path string) {
	partialMu.Lock()
	delete(partials, path)
	partialMu.Unlock()
}

// Abort deletes any in-progress partial downloads. Call it from a signal
// handler so an interrupted run (Ctrl+C) leaves no .part files behind.
func Abort() {
	partialMu.Lock()
	defer partialMu.Unlock()
	for path, f := range partials {
		if f != nil {
			_ = f.Close()
		}
		_ = os.Remove(path)
	}
	partials = map[string]*os.File{}
}

// partialName is the temporary name for an in-progress download. It carries the
// process id so two ployenv runs cannot truncate each other's file (BUG-013);
// the final rename into place is atomic either way.
func partialName(dest string) string {
	return fmt.Sprintf("%s.part.%d", dest, os.Getpid())
}

// isPartial reports whether a cache entry looks like an interrupted download.
func isPartial(name string) bool {
	return strings.HasSuffix(name, ".part") || strings.Contains(name, ".part.")
}

// PurgePartials removes stale .part files in dir (older than an hour), e.g.
// from a run that was killed before it could clean up. Recent partials are
// left alone in case another ployenv is downloading right now.
func PurgePartials(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Hour)
	for _, e := range entries {
		if e.IsDir() || !isPartial(e.Name()) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// Download fetches urlFor(base) for each base in order, trying the list twice
// before falling back to the system curl. The first base is the configured
// mirror (when set), followed by the official source.
func Download(bases []string, urlFor func(base string) string, dest, label string) error {
	var lastErr error
	for round := 1; round <= 2; round++ {
		for _, base := range bases {
			url := urlFor(base)
			if err := downloadFile(url, dest, label); err == nil {
				return nil
			} else {
				lastErr = err
				if len(bases) > 1 {
					logx.Verbosef("source %s failed: %v", ShortURL(base), err)
				}
			}
		}
		if round < 2 {
			logx.Verbosef("all %d sources failed, retrying once...", len(bases))
		}
	}
	if curl := findCurl(); curl != "" {
		logx.Verbosef("retrying with the system curl")
		for _, base := range bases {
			if err := downloadViaCurl(curl, urlFor(base), dest, label); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
	}
	return lastErr
}

func downloadFile(url, dest, label string) error {
	req, cancel, err := newRequest(url, downloadTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	logx.Capture("download %s -> %s", url, dest)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed %s: HTTP %d", ShortURL(url), resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := partialName(dest)
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	registerPartial(tmp, f)
	defer unregisterPartial(tmp)
	prog := logx.NewProgress(label, resp.ContentLength)
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(tmp)
				return werr
			}
			prog.Add(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(tmp)
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if fi, err := os.Stat(tmp); err != nil || fi.Size() == 0 {
		os.Remove(tmp)
		return fmt.Errorf("download is empty: %s", ShortURL(url))
	}
	prog.Done()
	return os.Rename(tmp, dest)
}

func findCurl() string {
	if v := os.Getenv("PLOYENV_CURL"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	if p, err := exec.LookPath("curl"); err == nil {
		return p
	}
	return ""
}

// downloadViaCurl is the fallback for networks that only let allow-listed
// clients through: curl honors the system proxy settings.
func downloadViaCurl(curl, url, dest, label string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := partialName(dest)
	os.Remove(tmp)
	registerPartial(tmp, nil)
	defer unregisterPartial(tmp)
	logx.Verbosef("curl %s", ShortURL(url))
	cmd := exec.Command(curl, "-fL", "--retry", "2", "--connect-timeout", "20", "--max-time", "1800", "-o", tmp, url)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return err
	}
	st, err := os.Stat(tmp)
	if err != nil || st.Size() == 0 {
		os.Remove(tmp)
		return fmt.Errorf("curl download is empty: %s", ShortURL(url))
	}
	if label != "" {
		logx.Stepf("%s done %s", label, fsutil.HumanBytes(st.Size()))
	}
	return os.Rename(tmp, dest)
}
