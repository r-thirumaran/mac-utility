package main

import "testing"

func TestParseFakeDisplays(t *testing.T) {
	ds, err := parseFakeDisplays("1920x1080;2560x1440+1920+0@2;800x600+-800+0*")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 3 {
		t.Fatalf("got %d displays", len(ds))
	}
	if ds[0].W != 1920 || ds[0].H != 1080 || ds[0].Primary || ds[0].Scale != 1 {
		t.Fatalf("d0 = %+v", ds[0])
	}
	if ds[1].X != 1920 || ds[1].Y != 0 || ds[1].W != 2560 || ds[1].Scale != 2 {
		t.Fatalf("d1 = %+v", ds[1])
	}
	if ds[2].X != -800 || !ds[2].Primary {
		t.Fatalf("d2 = %+v", ds[2])
	}
	ds, err = parseFakeDisplays("1024x768")
	if err != nil || len(ds) != 1 || !ds[0].Primary {
		t.Fatalf("single: %+v %v", ds, err)
	}
	if ds, err := parseFakeDisplays(""); err != nil || ds != nil {
		t.Fatalf("empty: %+v %v", ds, err)
	}
	for _, bad := range []string{"abc", "1920x", "1920x1080+1", "0x10", "1920x1080@x"} {
		if _, err := parseFakeDisplays(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
