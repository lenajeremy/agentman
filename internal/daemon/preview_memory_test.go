package daemon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"
	"time"
)

// On macOS memory a process once touched stays charged to it after the Go
// runtime hands it back, so the largest preview ever made is what the daemon
// weighs from then on. A 38 MP image used to peak near 350 MB because
// resizing first copied the whole decoded picture. Shrinking must read the
// decoded image in place and allocate only what is the size of the result.
func TestShrinkingAnImageDoesNotCopyItWhole(t *testing.T) {
	const width, height = 4000, 4000
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	decoded := uint64(len(img.Pix))

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	scaled := fitImage(img, 1024)
	runtime.ReadMemStats(&after)

	if got := scaled.Bounds(); got.Dx() != 1024 || got.Dy() != 1024 {
		t.Fatalf("scaled to %v", got)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > decoded/2 {
		t.Errorf("shrinking a %d MB image allocated %d MB; a whole-image copy is back",
			decoded>>20, allocated>>20)
	}
}

// Several previews at once each held a decoded image, and the phone may ask
// for many: a page of screenshots, or two phones. One is made at a time.
func TestPreviewsAreMadeOneAtATime(t *testing.T) {
	// Wider than a preview, so it has to be resized, and small, so that
	// without the limit it finishes long before the check below.
	wide := image.NewRGBA(image.Rect(0, 0, previewMaxDimension+100, 8))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, wide); err != nil {
		t.Fatal(err)
	}
	source := encoded.Bytes()
	previewSlots <- struct{}{} // another preview is being made
	done := make(chan error, 1)
	go func() {
		_, _, _, err := previewImage(bytes.NewReader(source), "image/png")
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("a second preview ran alongside the first")
	case <-time.After(time.Second):
	}
	<-previewSlots
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the preview never ran once the first was done")
	}
}

// Shrinking straight from a transparent image must still average in
// premultiplied space, or edges against transparency darken.
func TestShrinkingFromATransparentImageKeepsEdgesClean(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	img.Set(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	img.Set(1, 0, color.NRGBA{R: 0, G: 0, B: 0, A: 0})
	img.Set(2, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	img.Set(3, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	scaled := fitImage(img, 2)
	// White and nothing average to half-transparent white, not grey.
	r, g, b, a := scaled.At(0, 0).RGBA()
	if a == 0 || r != a || g != a || b != a {
		t.Errorf("edge pixel = %v, want premultiplied white", scaled.At(0, 0))
	}
}
