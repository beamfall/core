package app

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type stubPrePlayPolicy struct{}

func (stubPrePlayPolicy) PlanPrePlay(context.Context, graph.PrePlayRequest) (graph.PrePlayPlan, error) {
	return graph.PrePlayPlan{}, nil
}

func TestActivePrePlayPolicyRequiresActiveBundle(t *testing.T) {
	st := storetest.Open(t)
	registry, err := plugin.NewRegistry([]plugin.Bundle{testPathBundle()})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	built := 0
	hook := func(pluginID string) *PrePlayPolicyHook {
		return &PrePlayPolicyHook{PluginID: pluginID, New: func(got *store.Store) graph.PrePlayPolicy {
			if got != st {
				t.Fatal("hook got a different store")
			}
			built++
			return stubPrePlayPolicy{}
		}}
	}
	if activePrePlayPolicy(registry, hook(testPathPluginID), st) == nil || built != 1 {
		t.Fatalf("active bundle: policy missing (built %d)", built)
	}
	for name, got := range map[string]graph.PrePlayPolicy{
		"inactive bundle": activePrePlayPolicy(registry, hook(testQueuePluginID), st),
		"nil hook":        activePrePlayPolicy(registry, nil, st),
		"nil New":         activePrePlayPolicy(registry, &PrePlayPolicyHook{PluginID: testPathPluginID}, st),
		"nil registry":    activePrePlayPolicy(nil, hook(testPathPluginID), st),
		"nil store":       activePrePlayPolicy(registry, hook(testPathPluginID), nil),
	} {
		if got != nil {
			t.Fatalf("%s: policy = %#v, want nil", name, got)
		}
	}
	if built != 1 {
		t.Fatalf("hook built %d policies, want 1", built)
	}
}
