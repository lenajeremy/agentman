package daemon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for rel, data := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFolderIconPrefersTheShallowestPicture(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string][]byte{
		"mobile/assets/images/icon.png": []byte("png"),
		"public/favicon.ico":            []byte("ico"),
	})
	if got := findFolderIcon(root); got != "public/favicon.ico" {
		t.Fatalf("found %q, want public/favicon.ico", got)
	}
}

func TestFolderIconPrefersALogoAtOneDepth(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string][]byte{
		"public/favicon.ico": []byte("ico"),
		"public/icon.png":    []byte("png"),
		"public/logo.png":    []byte("png"),
	})
	if got := findFolderIcon(root); got != "public/logo.png" {
		t.Fatalf("found %q, want public/logo.png", got)
	}
}

func TestFolderIconFindsAnExpoAppsIconThreeDown(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string][]byte{"mobile/assets/images/icon.png": []byte("png")})
	if got := findFolderIcon(root); got != "mobile/assets/images/icon.png" {
		t.Fatalf("found %q", got)
	}
}

func TestFolderIconSkipsDependenciesBuildsAndHiddenFolders(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string][]byte{
		"node_modules/pkg/logo.png": []byte("png"),
		"build/icon.png":            []byte("png"),
		".github/logo.png":          []byte("png"),
		"a/b/c/d/logo.png":          []byte("png"),
		"README.md":                 []byte("# hi"),
		"public/empty.ico":          {},
	})
	if got := findFolderIcon(root); got != "" {
		t.Fatalf("found %q, want nothing", got)
	}
}

func TestFolderIconsSearchEachFolderOnce(t *testing.T) {
	icons := newFolderIcons()
	searches := 0
	icons.find = func(string) string { searches++; return "" }
	for range 3 {
		icons.lookup("/projects/site")
		icons.lookup("/projects/site/")
	}
	if searches != 1 {
		t.Fatalf("searched %d times, want once", searches)
	}
	icons.home = "/Users/me"
	if got := icons.lookup("/Users/me"); got != "" || searches != 1 {
		t.Fatalf("the home folder was searched (%q, %d)", got, searches)
	}
}

// icon builds an .ico file around one picture.
func icon(width, height, bits int, picture []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, []uint16{0, 1, 1})
	b.Write([]byte{byte(width % 256), byte(height % 256), 0, 0})
	_ = binary.Write(&b, binary.LittleEndian, []uint16{1, uint16(bits)})
	_ = binary.Write(&b, binary.LittleEndian, []uint32{uint32(len(picture)), 22})
	b.Write(picture)
	return b.Bytes()
}

func TestIconPNGKeepsAnEmbeddedPNG(t *testing.T) {
	var picture bytes.Buffer
	_ = png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 16, 16)))
	out, err := iconPNG(icon(16, 16, 32, picture.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, picture.Bytes()) {
		t.Fatal("the embedded PNG was not passed through whole")
	}
}

func TestIconPNGReadsABitmapAndItsMask(t *testing.T) {
	// A 2×2, 24-bit bitmap, rows bottom up: red, green / blue, white, with
	// the bottom-left pixel masked out.
	var bmp bytes.Buffer
	_ = binary.Write(&bmp, binary.LittleEndian, []uint32{40, 2, 4})
	_ = binary.Write(&bmp, binary.LittleEndian, []uint16{1, 24})
	_ = binary.Write(&bmp, binary.LittleEndian, []uint32{0, 0, 0, 0, 0, 0})
	bmp.Write([]byte{255, 0, 0, 255, 255, 255, 0, 0}) // bottom row: blue, white, padding
	bmp.Write([]byte{0, 0, 255, 0, 255, 0, 0, 0})     // top row: red, green, padding
	bmp.Write([]byte{0x80, 0, 0, 0})                  // mask, bottom row: first pixel clear
	bmp.Write([]byte{0, 0, 0, 0})                     // mask, top row
	out, err := iconPNG(icon(2, 2, 24, bmp.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	want := map[image.Point]color.NRGBA{
		{0, 0}: {255, 0, 0, 255},
		{1, 0}: {0, 255, 0, 255},
		{0, 1}: {0, 0, 255, 0},
		{1, 1}: {255, 255, 255, 255},
	}
	for at, c := range want {
		got := color.NRGBAModel.Convert(img.At(at.X, at.Y)).(color.NRGBA)
		if got.A != c.A || (c.A != 0 && got != c) {
			t.Errorf("pixel %v is %v, want %v", at, got, c)
		}
	}
}

func TestIconPNGRefusesWhatIsNotAnIcon(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not an icon"), {0, 0, 1, 0, 0, 0}} {
		if _, err := iconPNG(data); err == nil {
			t.Errorf("%q was read as an icon", data)
		}
	}
}

func TestAFaviconCanBeReadAsAPicture(t *testing.T) {
	root := t.TempDir()
	var picture bytes.Buffer
	_ = png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 32, 32)))
	writeFiles(t, root, map[string][]byte{"favicon.ico": icon(32, 32, 32, picture.Bytes())})
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	_, img, mime, source, _, err := readWorkspace(dir, "favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	// No source: the apps drop a reply whose original is not a picture
	// format they can save.
	if img == "" || mime != "image/png" || source != nil {
		t.Fatalf("got image %t, mime %q, source %+v", img != "", mime, source)
	}
}
