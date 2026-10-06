package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Power settings model (docs/ARCHITECTURE.md §5). Keys are platform-neutral;
// each backend maps them to powercfg (Windows) or pmset (macOS). Supported
// tells the UI which rows and options to show on this machine.

// PowerSource is one of the onBattery / onPower groups. Minutes of 0 mean
// "never".
type PowerSource struct {
	DisplaySleepMin int    `json:"displaySleepMin"`
	SystemSleepMin  int    `json:"systemSleepMin"`
	LidClose        string `json:"lidClose"`
}

// HibernateSetting is the Windows hiberfile state.
type HibernateSetting struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"` // "full" | "reduced"
}

// KeepAwakeState mirrors power.keepAwake.
type KeepAwakeState struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"` // "system" | "display"
}

// PowerSupport lists what this machine and backend can read and write.
type PowerSupport struct {
	Battery            bool     `json:"battery"`
	Lid                bool     `json:"lid"`
	LidClose           []string `json:"lidClose"`
	LidOpenWakes       bool     `json:"lidOpenWakes"`
	WakeOnNetwork      bool     `json:"wakeOnNetwork"`
	PowerNap           bool     `json:"powerNap"`
	LowPowerMode       bool     `json:"lowPowerMode"`
	Hibernate          bool     `json:"hibernate"`
	HibernateModes     []string `json:"hibernateModes,omitempty"`
	PowerButton        bool     `json:"powerButton"`
	PowerButtonActions []string `json:"powerButtonActions,omitempty"`
	DisplaySleep       bool     `json:"displaySleep"`
	SystemSleep        bool     `json:"systemSleep"`
	KeepAwake          bool     `json:"keepAwake"`
	Actions            []string `json:"actions"`
	ScheduleTypes      []string `json:"scheduleTypes"`
}

// PowerModel is the power.get result.
type PowerModel struct {
	OnBattery     PowerSource       `json:"onBattery"`
	OnPower       PowerSource       `json:"onPower"`
	LidOpenWakes  bool              `json:"lidOpenWakes"`
	WakeOnNetwork bool              `json:"wakeOnNetwork"`
	PowerNap      *bool             `json:"powerNap,omitempty"`
	LowPowerMode  *bool             `json:"lowPowerMode,omitempty"`
	Hibernate     *HibernateSetting `json:"hibernate,omitempty"`
	PowerButton   string            `json:"powerButton,omitempty"`
	Supported     PowerSupport      `json:"supported"`
	KeepAwake     KeepAwakeState    `json:"keepAwake"`
	Elevated      bool              `json:"elevated"`
	Scheme        string            `json:"scheme,omitempty"`
	Fake          bool              `json:"fake,omitempty"`
}

// ApplyFailure is one key power.apply could not change.
type ApplyFailure struct {
	Key            string `json:"key"`
	Reason         string `json:"reason"`
	NeedsElevation bool   `json:"needsElevation,omitempty"`
}

// ApplyResult is the power.apply result.
type ApplyResult struct {
	Applied        []string       `json:"applied"`
	Failed         []ApplyFailure `json:"failed"`
	NeedsElevation bool           `json:"needsElevation,omitempty"`
}

// ScheduleItem is a recurring power event. Days are "mon" … "sun"; an empty
// list means every day. Time is "HH:MM" (24 h).
type ScheduleItem struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"` // sleep | shutdown | restart | hibernate | wake
	Days    []string `json:"days"`
	Time    string   `json:"time"`
	Enabled bool     `json:"enabled"`
}

// Power is the platform backend behind the power.* methods.
type Power interface {
	Get() (PowerModel, error)
	Apply(changes map[string]any) (ApplyResult, error)
	Action(action string) error
	KeepAwake(enabled bool, mode string) (KeepAwakeState, error)
	// AwakeState reports the keep-awake state without re-reading the
	// settings (power.keepAwake.get).
	AwakeState() KeepAwakeState
	ScheduleGet() ([]ScheduleItem, error)
	ScheduleSet(items []ScheduleItem) ([]ScheduleItem, error)
}

var (
	lidActions         = []string{"nothing", "sleep", "hibernate", "shutdown", "displayOff"}
	powerButtonActions = []string{"nothing", "sleep", "hibernate", "shutdown", "displayOff"}
	powerActions       = []string{"sleep", "displayOff", "lock", "restart", "shutdown"}
	keepAwakeModes     = []string{"system", "display"}
	weekDays           = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	timeRE             = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
)

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// flattenChanges turns the nested partial model the UI sends
// ({onBattery: {lidClose: "sleep"}, hibernate: {enabled: true}}) into dotted
// keys ("onBattery.lidClose"). Already-flat keys pass through.
func flattenChanges(changes map[string]any) map[string]any {
	out := map[string]any{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if m, ok := v.(map[string]any); ok && !isLeafObject(prefix) {
			for k, child := range m {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, child)
			}
			return
		}
		out[prefix] = v
	}
	for k, v := range changes {
		walk(k, v)
	}
	return out
}

// isLeafObject marks keys whose value is an object the model treats as one
// unit (none today; reserved for future keys like schedules).
func isLeafObject(key string) bool { return false }

// sortedKeys returns the map keys in order, so results are deterministic.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// asInt accepts JSON numbers (float64), ints and numeric strings.
func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case json.Number:
		i, err := x.Int64()
		return int(i), err == nil
	case string:
		var i int
		_, err := fmt.Sscanf(x, "%d", &i)
		return i, err == nil
	}
	return 0, false
}

func asBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case float64:
		return x != 0, true
	case string:
		switch strings.ToLower(x) {
		case "true", "1", "on", "yes":
			return true, true
		case "false", "0", "off", "no":
			return false, true
		}
	}
	return false, false
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// validateChange checks one flat key/value against the model and returns the
// normalized value (int minutes, bool or string).
func validateChange(key string, v any) (any, error) {
	switch key {
	case "onBattery.displaySleepMin", "onBattery.systemSleepMin", "onPower.displaySleepMin", "onPower.systemSleepMin":
		n, ok := asInt(v)
		if !ok || n < 0 || n > 24*60*365 {
			return nil, fmt.Errorf("%s must be a non-negative number of minutes", key)
		}
		return n, nil
	case "onBattery.lidClose", "onPower.lidClose":
		s, ok := asString(v)
		if !ok || !contains(lidActions, s) {
			return nil, fmt.Errorf("%s must be one of %s", key, strings.Join(lidActions, ", "))
		}
		return s, nil
	case "powerButton":
		s, ok := asString(v)
		if !ok || !contains(powerButtonActions, s) {
			return nil, fmt.Errorf("%s must be one of %s", key, strings.Join(powerButtonActions, ", "))
		}
		return s, nil
	case "lidOpenWakes", "wakeOnNetwork", "powerNap", "lowPowerMode", "hibernate.enabled":
		b, ok := asBool(v)
		if !ok {
			return nil, fmt.Errorf("%s must be a boolean", key)
		}
		return b, nil
	case "hibernate.mode":
		s, ok := asString(v)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s must be a string", key)
		}
		return s, nil
	}
	return nil, fmt.Errorf("unknown setting %q", key)
}

// applyToModel writes one validated change into m (used by the fake backend
// and to echo applied values).
func applyToModel(m *PowerModel, key string, v any) error {
	nv, err := validateChange(key, v)
	if err != nil {
		return err
	}
	src := &m.OnPower
	if strings.HasPrefix(key, "onBattery.") {
		src = &m.OnBattery
	}
	switch key {
	case "onBattery.displaySleepMin", "onPower.displaySleepMin":
		src.DisplaySleepMin = nv.(int)
	case "onBattery.systemSleepMin", "onPower.systemSleepMin":
		src.SystemSleepMin = nv.(int)
	case "onBattery.lidClose", "onPower.lidClose":
		src.LidClose = nv.(string)
	case "powerButton":
		m.PowerButton = nv.(string)
	case "lidOpenWakes":
		m.LidOpenWakes = nv.(bool)
	case "wakeOnNetwork":
		m.WakeOnNetwork = nv.(bool)
	case "powerNap":
		b := nv.(bool)
		m.PowerNap = &b
	case "lowPowerMode":
		b := nv.(bool)
		m.LowPowerMode = &b
	case "hibernate.enabled":
		if m.Hibernate == nil {
			m.Hibernate = &HibernateSetting{Mode: "full"}
		}
		m.Hibernate.Enabled = nv.(bool)
	case "hibernate.mode":
		if m.Hibernate == nil {
			m.Hibernate = &HibernateSetting{}
		}
		m.Hibernate.Mode = nv.(string)
	}
	return nil
}

// validateSchedule normalizes the items of power.schedule.set: ids are
// assigned when missing, days are lower-cased three-letter names in week
// order, times must be HH:MM and types must be in allowed.
func validateSchedule(items []ScheduleItem, allowed []string) ([]ScheduleItem, error) {
	out := make([]ScheduleItem, 0, len(items))
	for i, it := range items {
		it.Type = strings.ToLower(strings.TrimSpace(it.Type))
		if !contains(allowed, it.Type) {
			return nil, fmt.Errorf("item %d: unsupported type %q", i+1, it.Type)
		}
		if !timeRE.MatchString(it.Time) {
			return nil, fmt.Errorf("item %d: time must be HH:MM", i+1)
		}
		seen := map[string]bool{}
		for _, d := range it.Days {
			d = strings.ToLower(strings.TrimSpace(d))
			if len(d) > 3 {
				d = d[:3]
			}
			if !contains(weekDays, d) {
				return nil, fmt.Errorf("item %d: unknown day %q", i+1, d)
			}
			seen[d] = true
		}
		days := []string{}
		for _, d := range weekDays {
			if seen[d] {
				days = append(days, d)
			}
		}
		if len(days) == 7 {
			days = []string{}
		}
		it.Days = days
		if it.ID == "" {
			it.ID = fmt.Sprintf("%s-%s-%d", it.Type, strings.ReplaceAll(it.Time, ":", ""), i+1)
		}
		out = append(out, it)
	}
	return out, nil
}
