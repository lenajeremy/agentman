package webapp

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The web app's screens ship beside am, not inside it: each release puts
// them on its GitHub release as one file, agentman-web_v<version>.tar.gz,
// and am serve fetches the one for its own version the first time `am web`
// asks, into <config>/web/<version>. am stays its own size; the screens
// (about 9 MB to download, 26 MB unpacked) come once per version.
//
// They can read and write the user's files and run their terminals, so a
// file that isn't the release's own is never used: each released am carries
// the file's SHA-256, stamped at build time (.goreleaser.yaml, and
// desktop/scripts/build-am.sh for the Mac app's am), and the download must
// match it. A build without it, as in development, fetches nothing.

// bundleSHA256 is the release's web file's SHA-256, in hex, stamped with
// -ldflags "-X github.com/lenajeremy/agentman/internal/webapp.bundleSHA256=…".
var bundleSHA256 string

// releaseURL is where a release's web file is, by version (without its v).
const releaseURL = "https://github.com/lenajeremy/agentman/releases/download/v%[1]s/agentman-web_v%[1]s.tar.gz"

// What an archive may hold, well above what one does: past these it isn't
// the release's, and unpacking stops before it fills the disk.
const (
	maxArchiveBytes  = 100 << 20
	maxUnpackedBytes = 300 << 20
	maxFiles         = 10_000
)

// marker is written into a version's folder once it is whole: the SHA-256 of
// the file it came from.
const marker = ".bundle-sha256"

// ErrNotFetchable is a build that can't fetch the web app: one without the
// release's SHA-256, as in development.
var ErrNotFetchable = errors.New("this am can't fetch the web app: it is a development build, without a release's web file to check against. " +
	"Build the web app with (cd web && npm ci && npm run build) and set AGENTMAN_WEB_DIR=internal/webapp/dist, or build am with -tags webapp")

// Store is the web app's screens in the config folder, fetched once per
// version.
type Store struct {
	dir     string // <config>/web
	version string
	sha     string
	url     string
	client  *http.Client

	mu    sync.Mutex
	ready string // the version's folder, once known to be whole
}

// NewStore keeps the screens for version in dir (<config>/web), checked
// against sha (hex) and fetched from url. An empty sha fetches nothing.
func NewStore(dir, version, sha, url string) *Store {
	return &Store{dir: dir, version: version, sha: strings.ToLower(strings.TrimSpace(sha)), url: url,
		client: &http.Client{Timeout: 5 * time.Minute}}
}

// ForThisBuild is the store for this am: its version, the SHA-256 stamped
// into it, and the release's address, or AGENTMAN_WEB_URL's (a test server).
func ForThisBuild(configDir, version string) *Store {
	url := os.Getenv("AGENTMAN_WEB_URL")
	if url == "" {
		url = fmt.Sprintf(releaseURL, strings.TrimPrefix(version, "v"))
	}
	return NewStore(filepath.Join(configDir, "web"), version, bundleSHA256, url)
}

// Fetchable says whether this build can fetch its screens at all.
func (s *Store) Fetchable() bool { return s != nil && s.sha != "" && s.version != "" }

// Dir is the version's folder when it is whole, without fetching.
func (s *Store) Dir() (string, bool) {
	if !s.Fetchable() {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.whole()
}

func (s *Store) whole() (string, bool) {
	if s.ready != "" {
		return s.ready, true
	}
	folder := filepath.Join(s.dir, s.version)
	have, err := os.ReadFile(filepath.Join(folder, marker))
	if err != nil || strings.TrimSpace(string(have)) != s.sha {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(folder, "index.html")); err != nil {
		return "", false
	}
	s.ready = folder
	return folder, true
}

// Ensure has the version's screens on disk, fetching them if they aren't:
// downloaded, checked against the SHA-256, unpacked beside the folder and
// then put in its place, so a folder is never half there. Other versions'
// folders go once this one is whole. fetched says whether it downloaded.
func (s *Store) Ensure(ctx context.Context) (folder string, fetched bool, err error) {
	if !s.Fetchable() {
		return "", false, ErrNotFetchable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if folder, ok := s.whole(); ok {
		return folder, false, nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", false, err
	}
	archive, err := s.download(ctx)
	if err != nil {
		return "", false, err
	}
	defer os.Remove(archive)

	partial, err := os.MkdirTemp(s.dir, ".partial-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(partial) // gone once renamed; the rest of a failure otherwise
	file, err := os.Open(archive)
	if err != nil {
		return "", false, err
	}
	err = unpack(file, partial)
	_ = file.Close()
	if err != nil {
		return "", false, fmt.Errorf("the web app's file couldn't be unpacked: %w", err)
	}
	if _, err := os.Stat(filepath.Join(partial, "index.html")); err != nil {
		return "", false, errors.New("the web app's file has no index.html")
	}
	if err := os.WriteFile(filepath.Join(partial, marker), []byte(s.sha+"\n"), 0o600); err != nil {
		return "", false, err
	}
	folder = filepath.Join(s.dir, s.version)
	if err := os.RemoveAll(folder); err != nil {
		return "", false, err
	}
	if err := os.Rename(partial, folder); err != nil {
		return "", false, err
	}
	s.ready = folder
	s.forgetOthers()
	return folder, true, nil
}

// download saves the release's file beside the store and checks it.
func (s *Store) download(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the web app couldn't be downloaded: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the web app couldn't be downloaded from %s: %s", s.url, resp.Status)
	}
	out, err := os.CreateTemp(s.dir, ".download-*.tar.gz")
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, sum), io.LimitReader(resp.Body, maxArchiveBytes+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > maxArchiveBytes {
		err = errors.New("the web app's file is larger than any release's")
	}
	if err == nil && hex.EncodeToString(sum.Sum(nil)) != s.sha {
		err = errors.New("the downloaded web app isn't the one this am was released with (its SHA-256 differs), so it wasn't used")
	}
	if err != nil {
		_ = os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// forgetOthers removes other versions' folders and anything left by an
// earlier attempt that stopped half way.
func (s *Store) forgetOthers() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() != s.version {
			_ = os.RemoveAll(filepath.Join(s.dir, entry.Name()))
		}
	}
}

// unpack writes a .tar.gz's folders and plain files into dest, and nothing
// else: no links, no device files, no name that leaves dest, and no more
// than a release's screens could be.
func unpack(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	files := 0
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if name == "." {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
			return fmt.Errorf("%q is outside the web app's folder", header.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if files++; files > maxFiles {
				return errors.New("more files than a release's screens")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			n, err := io.Copy(out, io.LimitReader(tr, maxUnpackedBytes-total+1))
			if closeErr := out.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
			if total += n; total > maxUnpackedBytes {
				return errors.New("larger unpacked than a release's screens")
			}
		default:
			return fmt.Errorf("%q isn't a plain file or folder", header.Name)
		}
	}
}
