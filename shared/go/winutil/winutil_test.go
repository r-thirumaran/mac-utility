package winutil

import "testing"

// pixelOf is the inverse Windows applies to a normalised absolute
// coordinate: (n * size) >> 16.
func pixelOf(n int32, size int) int {
	return int((int64(n) * int64(size)) >> 16)
}

func TestAbsoluteCoordinate(t *testing.T) {
	for _, size := range []int{1, 2, 3, 7, 640, 800, 1366, 1920, 2560, 3440, 3840, 5120, 7680} {
		for _, origin := range []int{0, -1920, 1920, -3} {
			step := size/4096 + 1
			for p := 0; p < size; p += step {
				n := AbsoluteCoordinate(origin+p, origin, size)
				if n < 0 || n > 65535 {
					t.Fatalf("size %d p %d: %d out of range", size, p, n)
				}
				if got := pixelOf(n, size); got != p {
					t.Fatalf("size %d origin %d p %d: normalised %d maps back to %d", size, origin, p, n, got)
				}
				// The rounding variant of the mapping lands on the same pixel.
				if got := int((float64(n)*float64(size-1))/65535 + 0.5); size > 1 && got != p {
					t.Fatalf("size %d p %d: rounding variant maps %d to %d", size, p, n, got)
				}
			}
			// The far edge and anything past it stay inside.
			if n := AbsoluteCoordinate(origin+size-1, origin, size); pixelOf(n, size) != size-1 {
				t.Fatalf("size %d: last pixel maps to %d", size, pixelOf(n, size))
			}
			if n := AbsoluteCoordinate(origin+size+100, origin, size); n != 65535 {
				t.Fatalf("size %d: beyond the edge = %d, want 65535", size, n)
			}
			if n := AbsoluteCoordinate(origin-100, origin, size); n != 0 {
				t.Fatalf("size %d: before the origin = %d, want 0", size, n)
			}
		}
	}
	if AbsoluteCoordinate(10, 0, 0) != 0 || AbsoluteCoordinate(10, 0, -5) != 0 {
		t.Fatal("empty axis must yield 0")
	}
}
