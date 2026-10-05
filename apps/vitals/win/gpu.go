package main

import (
	"sort"
	"strconv"
	"strings"
)

// GPU counter aggregation (docs/research/windows-system-stats.md §2). The
// PDH set \GPU Engine(*)\Utilization Percentage has one instance per
// (process, adapter, engine). Task Manager's adapter "GPU %" is the busiest
// physical engine: sum the per-process instances of each engine, clamp to
// 100, take the max over engines. Never sum engine types.

// pdhItem is one instance of a formatted PDH counter array.
type pdhItem struct {
	Instance string
	Value    float64
}

// gpuInstance is a parsed "pid_…_luid_…_phys_…_eng_…_engtype_…" name.
type gpuInstance struct {
	PID     uint32
	LUID    string // "0x00000000_0x0000F814" — HighPart first, upper-case hex
	Phys    int
	Eng     int
	EngType string
}

// parseGPUInstance splits an instance name on "_" (never by fixed offsets).
func parseGPUInstance(s string) (g gpuInstance, ok bool) {
	parts := strings.Split(s, "_")
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "pid":
			if i+1 < len(parts) {
				v, _ := strconv.ParseUint(parts[i+1], 10, 32)
				g.PID = uint32(v)
				i++
			}
		case "luid":
			if i+2 < len(parts) {
				g.LUID = parts[i+1] + "_" + parts[i+2]
				ok = true
				i += 2
			}
		case "phys":
			if i+1 < len(parts) {
				g.Phys, _ = strconv.Atoi(parts[i+1])
				i++
			}
		case "eng":
			if i+1 < len(parts) {
				g.Eng, _ = strconv.Atoi(parts[i+1])
				i++
			}
		case "engtype":
			g.EngType = strings.Join(parts[i+1:], "_")
			i = len(parts)
		}
	}
	return g, ok
}

// gpuUtil is the aggregate of one collection.
type gpuUtil struct {
	Adapter   map[string]float64            // luid → busiest engine %
	Engines   map[string]map[string]float64 // luid → engine type → busiest engine of that type %
	Processes map[uint32]float64            // pid → % of its busiest instance
}

// aggregateGPU applies the Task Manager rule to the formatted instances.
func aggregateGPU(items []pdhItem) gpuUtil {
	type key struct {
		luid      string
		phys, eng int
	}
	sum := map[key]float64{}
	typ := map[key]string{}
	u := gpuUtil{Adapter: map[string]float64{}, Engines: map[string]map[string]float64{}, Processes: map[uint32]float64{}}
	for _, it := range items {
		g, ok := parseGPUInstance(it.Instance)
		if !ok {
			continue
		}
		k := key{g.LUID, g.Phys, g.Eng}
		sum[k] += it.Value
		typ[k] = g.EngType
		if it.Value > u.Processes[g.PID] {
			u.Processes[g.PID] = it.Value
		}
	}
	for k, v := range sum {
		if v > 100 {
			v = 100
		}
		if u.Engines[k.luid] == nil {
			u.Engines[k.luid] = map[string]float64{}
		}
		if v > u.Engines[k.luid][typ[k]] {
			u.Engines[k.luid][typ[k]] = v
		}
		if v > u.Adapter[k.luid] {
			u.Adapter[k.luid] = v
		}
	}
	return u
}

// parseLUIDInstance extracts the LUID key of a "luid_…_phys_…" instance
// (the GPU Adapter Memory counter set).
func parseLUIDInstance(s string) (luid string, ok bool) {
	g, ok := parseGPUInstance(s)
	return g.LUID, ok
}

// sortedLUIDs returns the adapter keys in a stable order.
func sortedLUIDs(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// isBasicRenderDriver reports the software adapter Task Manager hides.
func isBasicRenderDriver(name string) bool {
	return strings.Contains(strings.ToLower(name), "basic render")
}
