package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/log"
	backoff "github.com/cenkalti/backoff/v4"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

const (
	maxDownloadSize = 200 * 1024 * 1024 // 200MB
	downloadTimeout = 30 * time.Minute
	pendingFileName = "pending.json"
)

type pendingInfo struct {
	Link   string `json:"link"`
	Sha256 string `json:"sha256"`
}

func pendingFilePath() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "build", "upgrade_bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, pendingFileName), nil
}

func writePending(link, hash string) error {
	path, err := pendingFilePath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(pendingInfo{Link: link, Sha256: hash})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func readPending() (*pendingInfo, error) {
	path, err := pendingFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var p pendingInfo
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func clearPending() error {
	path, err := pendingFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// downloadBinary fetches rawURL, validates its sha256 against expectedHex,
// and unpacks or moves it into build/upgrade_bin. The download will be retried
// for a period of time before ultimately failing.
func downloadBinary(ctx context.Context, rawURL, expectedHex string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid url: %s", rawURL)
	}

	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	var finalPath string
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 200 * time.Millisecond
	bo.MaxInterval = 2 * time.Second
	bo.MaxElapsedTime = downloadTimeout

	err = backoff.Retry(func() error {
		var err error
		finalPath, err = downloadBinaryOnce(dlCtx, rawURL, expectedHex)
		if err != nil {
			log.Error("downloadBinary: attempt failed", "err", err)
		}
		return err
	}, backoff.WithContext(bo, dlCtx))
	if err != nil {
		return "", fmt.Errorf("failed to download %s: %w", rawURL, err)
	}
	return finalPath, nil
}

// downloadBinaryOnce performs a single download attempt.
func downloadBinaryOnce(ctx context.Context, rawURL, expectedHex string) (string, error) {
	client := &http.Client{}
	var resp *http.Response
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 200 * time.Millisecond
	bo.MaxInterval = 2 * time.Second
	bo.MaxElapsedTime = 200 * time.Second

	err := backoff.Retry(func() error {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if reqErr != nil {
			return reqErr
		}
		var httpErr error
		resp, httpErr = client.Do(req)
		if httpErr != nil {
			log.Error("downloadBinary: http request failed", "err", httpErr)
			return httpErr
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			statusErr := fmt.Errorf("status %d", resp.StatusCode)
			log.Error("downloadBinary: bad status", "status", resp.StatusCode)
			return statusErr
		}
		if resp.ContentLength > 0 && resp.ContentLength > maxDownloadSize {
			resp.Body.Close()
			return fmt.Errorf("file too large: %d bytes", resp.ContentLength)
		}
		return nil
	}, backoff.WithContext(bo, ctx))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Create progress bar
	p := mpb.New(mpb.WithWidth(60))

	var bar *mpb.Bar
	if resp.ContentLength > 0 {
		// Known content length - use byte-based progress
		bar = p.New(resp.ContentLength,
			mpb.BarStyle().Lbound("|").Filler("=").Tip(">").Rbound("|"),
			mpb.PrependDecorators(
				decor.Name("Downloading: ", decor.WC{W: len("Downloading: ") + 1, C: decor.DindentRight}),
				decor.CountersKibiByte("% .2f / % .2f"),
			),
			mpb.AppendDecorators(decor.Percentage()),
		)
	} else {
		// Unknown content length - use spinner
		bar = p.New(0,
			mpb.SpinnerStyle("∙∘◦○●○◦∘∙").PositionLeft(),
			mpb.PrependDecorators(
				decor.Name("Downloading: ", decor.WC{W: len("Downloading: ") + 1, C: decor.DindentRight}),
				decor.CountersKibiByte("% .2f"),
			),
		)
	}

	tmp, err := os.CreateTemp("", "bdag-download-*")
	if err != nil {
		bar.Abort(true)
		p.Wait()
		return "", err
	}
	defer os.Remove(tmp.Name())

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxDownloadSize+1)

	// Create a multi-writer that writes to both the file and hasher
	multiWriter := io.MultiWriter(tmp, hasher)

	// Create a proxy reader that updates the progress bar
	proxyReader := bar.ProxyReader(limited)
	defer proxyReader.Close()

	n, err := io.Copy(multiWriter, proxyReader)
	if err != nil {
		bar.Abort(true)
		p.Wait()
		tmp.Close()
		return "", err
	}
	tmp.Close()

	if n > maxDownloadSize {
		bar.Abort(true)
		p.Wait()
		return "", fmt.Errorf("file exceeds %d bytes", maxDownloadSize)
	}

	// Complete the progress bar
	if resp.ContentLength <= 0 {
		bar.SetTotal(n, true) // Set total for unknown length downloads
	}
	p.Wait()

	actualHex := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualHex, expectedHex) {
		return "", fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, actualHex)
	}

	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	upgradeDir := filepath.Join(root, "build", "upgrade_bin")
	if err := os.MkdirAll(upgradeDir, 0o755); err != nil {
		return "", err
	}

	base := filepath.Base(strings.TrimSuffix(strings.TrimSuffix(rawURL, ".zip"), ".tar.gz"))
	binName := strings.TrimSuffix(strings.TrimSuffix(base, ".tgz"), ".zip")

	files, err := os.ReadDir(upgradeDir)
	if err != nil {
		return "", err
	}
	var versions []string
	maxV := 0
	for _, f := range files {
		name := f.Name()
		if strings.HasPrefix(name, binName+"_v") {
			versions = append(versions, name)
			parts := strings.SplitN(name, "_v", 2)
			if v, _ := strconv.Atoi(parts[1]); v > maxV {
				maxV = v
			}
		}
	}
	nextV := maxV + 1
	target := filepath.Join(upgradeDir, fmt.Sprintf("%s_v%d", binName, nextV))

	lower := strings.ToLower(rawURL)
	var finalPath string
	switch {
	case strings.HasSuffix(lower, ".zip"):
		finalPath, err = unpackZipTo(tmp.Name(), target)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		finalPath, err = unpackTarGzTo(tmp.Name(), target)
	default:
		if err = os.Chmod(tmp.Name(), 0o755); err != nil {
			return "", err
		}
		err = os.Rename(tmp.Name(), target)
		finalPath = target
	}
	if err != nil {
		return "", err
	}

	const maxKeep = 5
	sort.Strings(versions)
	if len(versions) >= maxKeep {
		toRemove := versions[:len(versions)-maxKeep+1]
		for _, old := range toRemove {
			path := filepath.Join(upgradeDir, old)
			if remErr := os.Remove(path); remErr != nil {
				log.Warn("failed to remove old binary", "path", path, "err", remErr)
			}
		}
	}

	log.Info("downloadBinary: new version saved", "path", finalPath)
	return finalPath, nil
}

// unpackZipTo extracts a zip file to the target directory
func unpackZipTo(zipPath, targetDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}

	var executablePath string
	for _, f := range r.File {
		path := filepath.Join(targetDir, f.Name)

		// Ensure the file path is within the target directory
		if !strings.HasPrefix(path, filepath.Clean(targetDir)+string(os.PathSeparator)) {
			return "", fmt.Errorf("invalid file path: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(path, f.FileInfo().Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}

		rc, err := f.Open()
		if err != nil {
			return "", err
		}

		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.FileInfo().Mode())
		if err != nil {
			rc.Close()
			return "", err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return "", err
		}

		// Check if this might be the executable
		if executablePath == "" && (f.FileInfo().Mode()&0o111) != 0 {
			executablePath = path
		}
	}

	if executablePath != "" {
		return executablePath, nil
	}
	return targetDir, nil
}

// unpackTarGzTo extracts a tar.gz file to the target directory
func unpackTarGzTo(tarGzPath, targetDir string) (string, error) {
	file, err := os.Open(tarGzPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}

	var executablePath string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		path := filepath.Join(targetDir, header.Name)

		// Ensure the file path is within the target directory
		if !strings.HasPrefix(path, filepath.Clean(targetDir)+string(os.PathSeparator)) {
			return "", fmt.Errorf("invalid file path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, os.FileMode(header.Mode)); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}

			outFile, err := os.Create(path)
			if err != nil {
				return "", err
			}

			_, err = io.Copy(outFile, tr)
			outFile.Close()
			if err != nil {
				return "", err
			}

			if err := os.Chmod(path, os.FileMode(header.Mode)); err != nil {
				return "", err
			}

			// Check if this might be the executable
			if executablePath == "" && (header.Mode&0o111) != 0 {
				executablePath = path
			}
		}
	}

	if executablePath != "" {
		return executablePath, nil
	}
	return targetDir, nil
}
