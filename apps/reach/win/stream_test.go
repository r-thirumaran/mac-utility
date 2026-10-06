package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"testing"
	"time"
)

func TestFakeCaptureJPEG(t *testing.T) {
	fb := newFakeBackend()
	if err := fb.MouseMoveTo(0, 0.25, 0.5); err != nil {
		t.Fatal(err)
	}
	img, err := fb.Capture(0)
	if err != nil {
		t.Fatal(err)
	}
	if img.Rect.Dx() != fakeW || img.Rect.Dy() != fakeH {
		t.Fatalf("frame %v", img.Rect)
	}
	// The marker ring is white 8..11 px from the pointer (off the crosshair).
	mx, my := int(0.25*fakeW), int(0.5*fakeH)
	if r, g, b, _ := img.At(mx+7, my+7).RGBA(); r != 0xffff || g != 0xffff || b != 0xffff {
		t.Fatalf("no marker at the pointer: %v", img.At(mx+7, my+7))
	}
	p := streamParams{Display: 0, MaxWidth: 640, Quality: 0.7, FPS: 5}.normalize()
	frame, w, h, err := encodeFrame(img, p)
	if err != nil {
		t.Fatal(err)
	}
	if w != 640 || h != 360 {
		t.Fatalf("scaled to %dx%d", w, h)
	}
	kind, display, fw, fh, body, ok := decodeFrameHeader(frame)
	if !ok || kind != frameKindJPEG || display != 0 || fw != 640 || fh != 360 {
		t.Fatalf("header kind=%d display=%d %dx%d", kind, display, fw, fh)
	}
	dec, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	if dec.Bounds().Dx() != 640 || dec.Bounds().Dy() != 360 {
		t.Fatalf("decoded %v", dec.Bounds())
	}
	if len(body) < 2000 || len(body) > 200000 {
		t.Fatalf("suspicious JPEG size %d", len(body))
	}
	// Two captures a moment apart differ (the timestamp overlay).
	time.Sleep(5 * time.Millisecond)
	img2, _ := fb.Capture(0)
	if bytes.Equal(img.Pix, img2.Pix) {
		t.Fatal("frames are identical; timestamp overlay missing")
	}
}

func TestDownscale(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			o := src.PixOffset(x, y)
			v := uint8(0)
			if x%2 == 0 {
				v = 200
			}
			src.Pix[o], src.Pix[o+1], src.Pix[o+2], src.Pix[o+3] = v, v, v, 255
		}
	}
	dst := downscale(src, 4)
	if dst.Rect.Dx() != 4 || dst.Rect.Dy() != 2 {
		t.Fatalf("size %v", dst.Rect)
	}
	if dst.Pix[0] != 100 || dst.Pix[3] != 255 {
		t.Fatalf("box average = %v", dst.Pix[:4])
	}
	if same := downscale(src, 8); same != src {
		t.Fatal("no-op downscale copied")
	}
	n := streamParams{MaxWidth: 5000, Quality: 2, FPS: 99}.normalize()
	if n.MaxWidth != maxFrameWidth || n.Quality != maxQuality || n.FPS != maxFPS {
		t.Fatalf("normalize = %+v", n)
	}
	z := streamParams{}.normalize()
	if z.MaxWidth != maxFrameWidth || z.Quality != defaultQuality || z.FPS != 5 {
		t.Fatalf("defaults = %+v", z)
	}
}

func TestStreamerLoop(t *testing.T) {
	fb := newFakeBackend()
	frames := make(chan int, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := startStream(ctx, fb, streamParams{MaxWidth: 160, Quality: 0.3, FPS: 10}, func(b []byte) error {
		frames <- len(b)
		return nil
	}, nil)
	deadline := time.After(3 * time.Second)
	for i := 0; i < 3; i++ {
		select {
		case n := <-frames:
			if n < frameHeader+100 {
				t.Fatalf("frame %d too small: %d", i, n)
			}
		case <-deadline:
			t.Fatal("stream produced no frames")
		}
	}
	s.stop()
	// After stop no more frames arrive.
	drained := len(frames)
	time.Sleep(250 * time.Millisecond)
	if len(frames) > drained+1 {
		t.Fatalf("stream kept running after stop: %d → %d", drained, len(frames))
	}
}
