package download

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func zipDir(dir, dst string) error {
	f, err := os.Create(dst + ".part")
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	entries, err := os.ReadDir(dir)
	if err != nil {
		f.Close()
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		// Images are already compressed; store them.
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.Name(), Method: zip.Store})
		if err != nil {
			f.Close()
			return err
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(b); err != nil {
			f.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".part", dst)
}

var badChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func sanitize(s string) string {
	s = strings.TrimSpace(badChars.ReplaceAllString(s, "_"))
	s = strings.Trim(s, ".")
	if s == "" {
		return "untitled"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// padNumber zero-pads the integer part so folders sort correctly: "5.5" -> "0005.5".
func padNumber(n string) string {
	intPart, frac, hasFrac := strings.Cut(n, ".")
	if _, err := strconv.Atoi(intPart); err != nil {
		return n
	}
	intPart = strings.Repeat("0", max(0, 4-len(intPart))) + intPart
	if hasFrac {
		return intPart + "." + frac
	}
	return intPart
}

// ErrOutputUnavailable means the output directory is missing, typically because
// a NAS mount dropped. Callers should wait and retry rather than fail the series.
var ErrOutputUnavailable = errors.New("output directory unavailable")

// EnsureOutDir checks that the output directory exists, creating only its last
// path element. If a parent (such as a NAS mount point) is missing it fails
// instead of creating a local directory that would shadow the mount.
func EnsureOutDir(out string) error {
	if st, err := os.Stat(out); err == nil {
		if st.IsDir() {
			return nil
		}
		return fmt.Errorf("%w: %s is not a directory", ErrOutputUnavailable, out)
	}
	if err := os.Mkdir(out, 0o755); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrOutputUnavailable, out, err)
	}
	return nil
}
