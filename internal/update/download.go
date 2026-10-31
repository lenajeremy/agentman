package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// DownloadsURL is where each release's files are, under its tag.
const DownloadsURL = "https://github.com/lenajeremy/agentman/releases/download"

const (
	// maxArchive bounds a release archive: am is about 30 MB, zipped.
	maxArchive = 200 << 20
	// maxBinary bounds the am inside it, unpacked.
	maxBinary = 400 << 20
	// maxChecksums bounds checksums.txt, a line per archive.
	maxChecksums = 1 << 20
)

// Archive names a release's archive for one system, as GoReleaser does.
func Archive(version, goos, goarch string) string {
	return fmt.Sprintf("agentman_v%s_%s_%s.tar.gz", version, goos, goarch)
}

// Fetch downloads the am of one release for one system, checks the archive
// against the release's published checksums, and returns the binary. It is
// what the install script does, for an am that was installed that way.
func Fetch(ctx context.Context, client *http.Client, base, version, goos, goarch string) ([]byte, error) {
	name := Archive(version, goos, goarch)
	tag := base + "/v" + version + "/"
	sums, err := get(ctx, client, tag+"checksums.txt", maxChecksums)
	if err != nil {
		return nil, fmt.Errorf("update: the release's checksums: %w", err)
	}
	want, ok := checksumFor(sums, name)
	if !ok {
		return nil, fmt.Errorf("update: the release lists no checksum for %s; there may be no build for %s/%s", name, goos, goarch)
	}
	archive, err := get(ctx, client, tag+name, maxArchive)
	if err != nil {
		return nil, fmt.Errorf("update: %s: %w", name, err)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("update: %s does not match its published checksum; nothing was installed", name)
	}
	return binaryIn(archive)
}

func get(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("larger than any release file should be")
	}
	return data, nil
}

// checksumFor finds name's line in a sha256sum listing: "<hex>  <name>",
// with "*" before the name for a binary-mode sum.
func checksumFor(sums []byte, name string) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// binaryIn takes am out of a release archive: the regular file named am at
// its top, as the install script expects it.
func binaryIn(archive []byte) ([]byte, error) {
	unzipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("update: the archive: %w", err)
	}
	files := tar.NewReader(unzipped)
	for {
		header, err := files.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("update: the archive holds no am")
		}
		if err != nil {
			return nil, fmt.Errorf("update: the archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || path.Clean(header.Name) != "am" {
			continue
		}
		if header.Size > maxBinary {
			return nil, errors.New("update: the am in the archive is larger than it should be")
		}
		data, err := io.ReadAll(io.LimitReader(files, maxBinary+1))
		if err != nil {
			return nil, fmt.Errorf("update: the archive: %w", err)
		}
		if int64(len(data)) > maxBinary {
			return nil, errors.New("update: the am in the archive is larger than it should be")
		}
		return data, nil
	}
}
