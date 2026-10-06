package main

import (
	"fmt"
	"sync"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

// fakePower is the in-memory backend behind --fake-power: a plausible
// laptop model that accepts every apply, records actions and keeps its
// schedule in memory, so the UI tests exercise the Power page on Linux.
type fakePower struct {
	mu         sync.Mutex
	model      PowerModel
	items      []ScheduleItem
	lastAction string
	actions    []string
}

func newFakePower() *fakePower {
	off := false
	return &fakePower{model: PowerModel{
		OnBattery:     PowerSource{DisplaySleepMin: 5, SystemSleepMin: 15, LidClose: "sleep"},
		OnPower:       PowerSource{DisplaySleepMin: 10, SystemSleepMin: 30, LidClose: "nothing"},
		LidOpenWakes:  true,
		WakeOnNetwork: true,
		PowerNap:      &off,
		LowPowerMode:  &off,
		Hibernate:     &HibernateSetting{Enabled: true, Mode: "full"},
		PowerButton:   "sleep",
		Supported: PowerSupport{
			Battery: true, Lid: true,
			LidClose:           []string{"nothing", "sleep", "hibernate", "shutdown"},
			LidOpenWakes:       true,
			WakeOnNetwork:      true,
			PowerNap:           true,
			LowPowerMode:       true,
			Hibernate:          true,
			HibernateModes:     []string{"full", "reduced"},
			PowerButton:        true,
			PowerButtonActions: powerButtonActions,
			DisplaySleep:       true,
			SystemSleep:        true,
			KeepAwake:          true,
			Actions:            powerActions,
			ScheduleTypes:      []string{"sleep", "shutdown", "restart", "hibernate"},
		},
		KeepAwake: KeepAwakeState{Mode: "system"},
		Scheme:    "Balanced (simulated)",
		Fake:      true,
	}, items: []ScheduleItem{}}
}

func (p *fakePower) Get() (PowerModel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.model, nil
}

func (p *fakePower) Apply(changes map[string]any) (ApplyResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	res := ApplyResult{Applied: []string{}, Failed: []ApplyFailure{}}
	flat := flattenChanges(changes)
	for _, key := range sortedKeys(flat) {
		if err := applyToModel(&p.model, key, flat[key]); err != nil {
			res.Failed = append(res.Failed, ApplyFailure{Key: key, Reason: err.Error()})
			continue
		}
		res.Applied = append(res.Applied, key)
	}
	return res, nil
}

func (p *fakePower) Action(action string) error {
	if !contains(powerActions, action) {
		return bridge.Errorf(bridge.CodeInvalidParams, "unknown action %q", action)
	}
	p.mu.Lock()
	p.lastAction = action
	p.actions = append(p.actions, action)
	p.mu.Unlock()
	return nil
}

func (p *fakePower) KeepAwake(enabled bool, mode string) (KeepAwakeState, error) {
	if mode == "" {
		mode = "system"
	}
	if !contains(keepAwakeModes, mode) {
		return KeepAwakeState{}, bridge.Errorf(bridge.CodeInvalidParams, "keep-awake mode must be system or display")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.model.KeepAwake = KeepAwakeState{Enabled: enabled, Mode: mode}
	return p.model.KeepAwake, nil
}

func (p *fakePower) AwakeState() KeepAwakeState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.model.KeepAwake
}

func (p *fakePower) ScheduleGet() ([]ScheduleItem, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ScheduleItem{}, p.items...), nil
}

func (p *fakePower) ScheduleSet(items []ScheduleItem) ([]ScheduleItem, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	clean, err := validateSchedule(items, p.model.Supported.ScheduleTypes)
	if err != nil {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "%v", err)
	}
	p.items = clean
	return append([]ScheduleItem{}, clean...), nil
}

// String describes the fake state for logs.
func (p *fakePower) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf("fake power: lid %s/%s, last action %q, %d schedule items", p.model.OnBattery.LidClose, p.model.OnPower.LidClose, p.lastAction, len(p.items))
}
