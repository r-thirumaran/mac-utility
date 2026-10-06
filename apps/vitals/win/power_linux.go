//go:build linux

package main

import (
	"sync"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

// linuxPower is the honest Linux backend: it reads and writes nothing. Every
// setting is reported unsupported, actions are not_supported, and keep-awake
// is a no-op that remembers its state. --fake-power swaps in fakePower.
type linuxPower struct {
	mu   sync.Mutex
	keep KeepAwakeState
}

func newPower(fake bool) Power {
	if fake {
		return newFakePower()
	}
	return &linuxPower{keep: KeepAwakeState{Mode: "system"}}
}

func (p *linuxPower) Get() (PowerModel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PowerModel{
		OnBattery: PowerSource{LidClose: "sleep"},
		OnPower:   PowerSource{LidClose: "sleep"},
		Supported: PowerSupport{
			LidClose:      []string{},
			KeepAwake:     true,
			Actions:       []string{},
			ScheduleTypes: []string{},
		},
		KeepAwake: p.keep,
	}, nil
}

func (p *linuxPower) Apply(changes map[string]any) (ApplyResult, error) {
	res := ApplyResult{Applied: []string{}, Failed: []ApplyFailure{}}
	flat := flattenChanges(changes)
	for _, key := range sortedKeys(flat) {
		res.Failed = append(res.Failed, ApplyFailure{Key: key, Reason: "power settings are not supported on Linux"})
	}
	return res, nil
}

func (p *linuxPower) Action(action string) error {
	if !contains(powerActions, action) {
		return bridge.Errorf(bridge.CodeInvalidParams, "unknown action %q", action)
	}
	return bridge.Errorf(bridge.CodeNotSupported, "power actions are not supported on Linux")
}

func (p *linuxPower) KeepAwake(enabled bool, mode string) (KeepAwakeState, error) {
	if mode == "" {
		mode = "system"
	}
	if !contains(keepAwakeModes, mode) {
		return KeepAwakeState{}, bridge.Errorf(bridge.CodeInvalidParams, "keep-awake mode must be system or display")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keep = KeepAwakeState{Enabled: enabled, Mode: mode}
	return p.keep, nil
}

func (p *linuxPower) ScheduleGet() ([]ScheduleItem, error) { return []ScheduleItem{}, nil }

func (p *linuxPower) ScheduleSet(items []ScheduleItem) ([]ScheduleItem, error) {
	return nil, bridge.Errorf(bridge.CodeNotSupported, "scheduled power events are not supported on Linux")
}

// openSettingsPane has no Linux equivalent.
func openSettingsPane(pane string) error {
	return bridge.Errorf(bridge.CodeNotSupported, "no settings pane for %q on Linux", pane)
}
