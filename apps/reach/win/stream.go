package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"time"
)

// Binary frame layout (docs/reach-api.md §4): u8 kind, then for kind 0x01
// u8 display, u16 w, u16 h (big-endian) and the JPEG bytes.
const (
	frameKindJPEG = 0x01
	frameHeader   = 6

	maxFrameWidth  = 1280
	defaultQuality = 0.6
	minQuality     = 0.1
	maxQuality     = 0.9
	minFPS         = 1
	maxFPS         = 10
)

// streamParams are the parameters of screen.frame / screen.stream.start.
type streamParams struct {
	Display  int     `json:"display"`
	MaxWidth int     `json:"maxWidth"`
	Quality  float64 `json:"quality"`
	FPS      int     `json:"fps"`
}

// normalize clamps the parameters to the ranges of the spec.
func (p streamParams) normalize() streamParams {
	if p.MaxWidth <= 0 || p.MaxWidth > maxFrameWidth {
		p.MaxWidth = maxFrameWidth
	}
	if p.Quality == 0 {
		p.Quality = defaultQuality
	}
	if p.Quality < minQuality {
		p.Quality = minQuality
	}
	if p.Quality > maxQuality {
		p.Quality = maxQuality
	}
	if p.FPS < minFPS {
		p.FPS = 5
	}
	if p.FPS > maxFPS {
		p.FPS = maxFPS
	}
	return p
}

// downscale shrinks src with a box filter so that its width is at most
// maxWidth. The factor is an integer (ceil(w/maxWidth)), which keeps the
// filter exact and fast; the result can therefore be somewhat narrower than
// maxWidth. src is returned unchanged when it already fits.
func downscale(src *image.RGBA, maxWidth int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if maxWidth <= 0 || w <= maxWidth || w == 0 || h == 0 {
		return src
	}
	f := (w + maxWidth - 1) / maxWidth
	ow, oh := w/f, h/f
	if ow == 0 || oh == 0 {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	n := uint32(f * f)
	half := n / 2
	for oy := 0; oy < oh; oy++ {
		for ox := 0; ox < ow; ox++ {
			var r, g, b uint32
			for yy := 0; yy < f; yy++ {
				row := src.PixOffset(src.Rect.Min.X+ox*f, src.Rect.Min.Y+oy*f+yy)
				px := src.Pix[row : row+f*4 : row+f*4]
				for xx := 0; xx < f*4; xx += 4 {
					r += uint32(px[xx])
					g += uint32(px[xx+1])
					b += uint32(px[xx+2])
				}
			}
			o := dst.PixOffset(ox, oy)
			dst.Pix[o] = uint8((r + half) / n)
			dst.Pix[o+1] = uint8((g + half) / n)
			dst.Pix[o+2] = uint8((b + half) / n)
			dst.Pix[o+3] = 0xff
		}
	}
	return dst
}

// encodeJPEG encodes img with quality 0.1–0.9 mapped to 10–90.
func encodeJPEG(img image.Image, quality float64) ([]byte, error) {
	q := int(quality*100 + 0.5)
	if q < 10 {
		q = 10
	}
	if q > 90 {
		q = 90
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
		return nil, fmt.Errorf("jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// encodeFrame scales img to p.MaxWidth and returns the binary frame plus the
// encoded size.
func encodeFrame(img *image.RGBA, p streamParams) (frame []byte, w, h int, err error) {
	small := downscale(img, p.MaxWidth)
	w, h = small.Rect.Dx(), small.Rect.Dy()
	if w > 0xffff || h > 0xffff || w == 0 || h == 0 {
		return nil, 0, 0, fmt.Errorf("frame %dx%d out of range", w, h)
	}
	jpg, err := encodeJPEG(small, p.Quality)
	if err != nil {
		return nil, 0, 0, err
	}
	frame = make([]byte, frameHeader+len(jpg))
	frame[0] = frameKindJPEG
	frame[1] = uint8(p.Display)
	binary.BigEndian.PutUint16(frame[2:], uint16(w))
	binary.BigEndian.PutUint16(frame[4:], uint16(h))
	copy(frame[frameHeader:], jpg)
	return frame, w, h, nil
}

// decodeFrameHeader parses a binary frame header (tests and tools).
func decodeFrameHeader(frame []byte) (kind, display uint8, w, h int, body []byte, ok bool) {
	if len(frame) < frameHeader {
		return 0, 0, 0, 0, nil, false
	}
	return frame[0], frame[1], int(binary.BigEndian.Uint16(frame[2:])), int(binary.BigEndian.Uint16(frame[4:])), frame[frameHeader:], true
}

// streamer runs one connection's screen stream: a capture → scale → encode →
// send loop at p.FPS. Sends are synchronous, so a slow client stalls the loop
// and the ticker drops the ticks that pile up meanwhile (frames are never
// queued). It stops on stop(), when ctx ends, when send fails or after
// repeated capture failures.
type streamer struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startStream begins streaming. send is called with each complete binary
// frame from the stream's own goroutine.
func startStream(ctx context.Context, b Backend, p streamParams, send func([]byte) error, logf func(string, ...any)) *streamer {
	ctx, cancel := context.WithCancel(ctx)
	s := &streamer{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		interval := time.Second / time.Duration(p.FPS)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			img, err := b.Capture(p.Display)
			if err != nil {
				failures++
				if logf != nil {
					logf("stream: capture display %d: %v", p.Display, err)
				}
				if failures >= 5 {
					return
				}
				continue
			}
			failures = 0
			frame, _, _, err := encodeFrame(img, p)
			if err != nil {
				if logf != nil {
					logf("stream: encode: %v", err)
				}
				continue
			}
			if err := send(frame); err != nil {
				return
			}
		}
	}()
	return s
}

// stop ends the loop and waits for it to finish.
func (s *streamer) stop() {
	s.cancel()
	<-s.done
}
