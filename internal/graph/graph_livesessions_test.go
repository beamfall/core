package graph

import (
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func TestActiveSessionIDsByDevicePartitionsAndDeduplicatesRegardlessOfOrder(t *testing.T) {
	first := []store.ActiveSession{
		{DeviceID: "device-a", SessionID: "session-1"},
		{DeviceID: "device-b", SessionID: "session-3"},
		{DeviceID: "device-a", SessionID: "session-1"},
		{DeviceID: "device-c", SessionID: "session-1"},
		{DeviceID: "device-a", SessionID: "session-2"},
		{DeviceID: "device-b", SessionID: "session-3"},
	}
	second := []store.ActiveSession{
		first[1], first[0], first[5], first[2], first[3], first[4],
	}
	want := map[store.DeviceID][]store.SessionID{
		"device-a": {"session-1", "session-2"},
		"device-b": {"session-3"},
		"device-c": {"session-1"},
	}

	for _, active := range [][]store.ActiveSession{first, second} {
		if got := activeSessionIDsByDevice(active); !reflect.DeepEqual(got, want) {
			t.Fatalf("activeSessionIDsByDevice() = %v, want %v", got, want)
		}
	}
}
