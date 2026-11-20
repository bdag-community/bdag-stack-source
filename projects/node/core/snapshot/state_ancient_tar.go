package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// materializeEVMStateAncientTarGz writes the state freezer directory to a temp
// gzip-tar file and returns its path, byte size, and hex sha256 of the file.
func materializeEVMStateAncientTarGz(stateDir string, gzipLevel int) (path string, size int64, sha256hex string, err error) {
	tmp, err := os.CreateTemp("", "bdag-evm-state-*.tar.gz")
	if err != nil {
		return "", 0, "", err
	}
	tmpPath := tmp.Name()
	cw := &countingWriter{w: tmp}
	h := sha256.New()
	mw := io.MultiWriter(cw, h)
	gz, err := gzip.NewWriterLevel(mw, gzipLevel)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", 0, "", err
	}
	tw := tar.NewWriter(gz)

	walkErr := filepath.WalkDir(stateDir, func(path string, d fs.DirEntry, we error) error {
		if we != nil {
			return we
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stateDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "../") || rel == ".." {
			return fmt.Errorf("invalid path under state freezer: %s", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = "state/" + rel
		hdr.Format = tar.FormatGNU
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		_ = f.Close()
		return err
	})
	if walkErr != nil {
		_ = tw.Close()
		_ = gz.Close()
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", 0, "", walkErr
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", 0, "", err
	}
	if err := gz.Close(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", 0, "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, "", err
	}
	return tmpPath, cw.n, hex.EncodeToString(h.Sum(nil)), nil
}

// extractEVMStateAncientTarGz reads exactly gzBytes from r, verifies the gzip
// payload sha256, and extracts into ancientRoot (the .../chaindata/ancient path).
func extractEVMStateAncientTarGz(r io.Reader, ancientRoot string, gzBytes uint64, wantSHA256 string) error {
	if gzBytes == 0 {
		return nil
	}
	lr := io.LimitReader(r, int64(gzBytes))
	h := sha256.New()
	tee := io.TeeReader(lr, h)
	gz, err := gzip.NewReader(tee)
	if err != nil {
		return fmt.Errorf("snapshot: state ancient gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("snapshot: state ancient tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		name := filepath.ToSlash(hdr.Name)
		if strings.Contains(name, "..") {
			return fmt.Errorf("snapshot: malicious tar entry %q", hdr.Name)
		}
		if !strings.HasPrefix(name, "state/") {
			return fmt.Errorf("snapshot: unexpected tar entry %q (want state/ prefix)", name)
		}
		dest := filepath.Join(ancientRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(hdr.Mode)&0o777)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	if n, _ := io.Copy(io.Discard, lr); n > 0 {
		return fmt.Errorf("snapshot: trailing garbage after state ancient tarball")
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, wantSHA256) {
		return fmt.Errorf("snapshot: state ancient sha256 mismatch (got %s want %s)", got, wantSHA256)
	}
	return nil
}

// verifyEVMStateAncientTarGz reads exactly gzBytes from r and checks the gzip
// stream and tar layout the same way as extract, without writing files.
func verifyEVMStateAncientTarGz(r io.Reader, gzBytes uint64, wantSHA256 string) error {
	if gzBytes == 0 {
		return nil
	}
	lr := io.LimitReader(r, int64(gzBytes))
	h := sha256.New()
	tee := io.TeeReader(lr, h)
	gz, err := gzip.NewReader(tee)
	if err != nil {
		return fmt.Errorf("snapshot: state ancient gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("snapshot: state ancient tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		name := filepath.ToSlash(hdr.Name)
		if strings.Contains(name, "..") {
			return fmt.Errorf("snapshot: malicious tar entry %q", hdr.Name)
		}
		if !strings.HasPrefix(name, "state/") {
			return fmt.Errorf("snapshot: unexpected tar entry %q (want state/ prefix)", name)
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return err
		}
	}
	if n, _ := io.Copy(io.Discard, lr); n > 0 {
		return fmt.Errorf("snapshot: trailing garbage after state ancient tarball")
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, wantSHA256) {
		return fmt.Errorf("snapshot: state ancient sha256 mismatch (got %s want %s)", got, wantSHA256)
	}
	return nil
}
