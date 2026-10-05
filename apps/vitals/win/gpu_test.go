package main

import "testing"

func TestParseGPUInstance(t *testing.T) {
	g, ok := parseGPUInstance("pid_10584_luid_0x00000000_0x00017FEA_phys_0_eng_3_engtype_VideoDecode")
	if !ok {
		t.Fatal("not ok")
	}
	if g.PID != 10584 || g.LUID != "0x00000000_0x00017FEA" || g.Phys != 0 || g.Eng != 3 || g.EngType != "VideoDecode" {
		t.Fatalf("got %+v", g)
	}
	g, ok = parseGPUInstance("pid_1_luid_0x00000000_0x0000F814_phys_0_eng_0_engtype_3D")
	if !ok || g.EngType != "3D" {
		t.Fatalf("got %+v ok=%v", g, ok)
	}
	if _, ok := parseGPUInstance("_Total"); ok {
		t.Fatal("_Total should not parse")
	}
	luid, ok := parseLUIDInstance("luid_0x00000000_0x0000F814_phys_0")
	if !ok || luid != "0x00000000_0x0000F814" {
		t.Fatalf("luid = %q ok=%v", luid, ok)
	}
}

func TestAggregateGPU(t *testing.T) {
	const a = "luid_0x00000000_0x0000F814_phys_0"
	items := []pdhItem{
		{"pid_100_" + a + "_eng_0_engtype_3D", 20},
		{"pid_200_" + a + "_eng_0_engtype_3D", 15}, // same engine → sums to 35
		{"pid_100_" + a + "_eng_1_engtype_VideoDecode", 40},
		{"pid_300_" + a + "_eng_2_engtype_Copy", 10},
		{"pid_300_" + a + "_eng_3_engtype_Copy", 70},
		{"pid_300_" + a + "_eng_3_engtype_Copy", 70}, // duplicates sum past 100 → clamp
		{"pid_400_luid_0x00000000_0x0000AAAA_phys_0_eng_0_engtype_3D", 5},
		{"garbage", 99},
	}
	u := aggregateGPU(items)
	if got := u.Adapter["0x00000000_0x0000F814"]; got != 100 {
		t.Fatalf("adapter = %v, want 100 (clamped busiest engine)", got)
	}
	if got := u.Adapter["0x00000000_0x0000AAAA"]; got != 5 {
		t.Fatalf("second adapter = %v", got)
	}
	e := u.Engines["0x00000000_0x0000F814"]
	if e["3D"] != 35 || e["VideoDecode"] != 40 || e["Copy"] != 100 {
		t.Fatalf("engines = %v", e)
	}
	if u.Processes[100] != 40 || u.Processes[300] != 70 || u.Processes[200] != 15 {
		t.Fatalf("processes = %v", u.Processes)
	}
	if len(sortedLUIDs(u.Adapter)) != 2 || sortedLUIDs(u.Adapter)[0] != "0x00000000_0x0000AAAA" {
		t.Fatalf("sorted = %v", sortedLUIDs(u.Adapter))
	}
	if !isBasicRenderDriver("Microsoft Basic Render Driver") || isBasicRenderDriver("NVIDIA GeForce RTX 3060") {
		t.Fatal("basic render driver detection")
	}
}
