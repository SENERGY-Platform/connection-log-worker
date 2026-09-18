/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package controller

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

// sortHubLogs and sortHubStates order slices deterministically so comparisons
// don't depend on map iteration order (handleHubLogs builds both by ranging
// over a map).
func sortHubLogs(logs []model.HubLog) {
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].Id != logs[j].Id {
			return logs[i].Id < logs[j].Id
		}
		return logs[i].Time.Before(logs[j].Time)
	})
}

func sortHubStates(states []HubState) {
	sort.Slice(states, func(i, j int) bool {
		return states[i].Gateway < states[j].Gateway
	})
}

func sortDeviceLogs(logs []model.DeviceLog) {
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].Id != logs[j].Id {
			return logs[i].Id < logs[j].Id
		}
		return logs[i].Time.Before(logs[j].Time)
	})
}

func sortDeviceStates(states []DeviceState) {
	sort.Slice(states, func(i, j int) bool {
		return states[i].Device < states[j].Device
	})
}

func TestHandleHubLogs(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	t1 := base
	t2 := base.Add(time.Minute)
	t3 := base.Add(2 * time.Minute)

	t.Run("returns nothing for an empty batch", func(t *testing.T) {
		newStates, newLogs := handleHubLogs(map[string]HubState{}, nil)
		if len(newStates) != 0 || len(newLogs) != 0 {
			t.Errorf("expected no states and no logs, got states=%v logs=%v", newStates, newLogs)
		}
	})

	t.Run("adds a new hub and includes its log in the history batch", func(t *testing.T) {
		states := map[string]HubState{}
		logs := []model.HubLog{
			{Id: "hubNew", Connected: true, Time: t1},
		}
		newStates, newLogs := handleHubLogs(states, logs)
		expectedStates := []HubState{
			{Gateway: "hubNew", Online: true, Since: t1.Unix()},
		}
		expectedLogs := []model.HubLog{
			{Id: "hubNew", Connected: true, Time: t1},
		}
		if !reflect.DeepEqual(expectedStates, newStates) {
			t.Errorf("expected states %v, got %v", expectedStates, newStates)
		}
		if !reflect.DeepEqual(expectedLogs, newLogs) {
			t.Errorf("expected logs %v, got %v", expectedLogs, newLogs)
		}
	})

	t.Run("drops a single duplicate log that matches the stored state", func(t *testing.T) {
		states := map[string]HubState{
			"hub1": {Gateway: "hub1", Online: true, Since: t1.Unix()},
		}
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: t2},
		}
		newStates, newLogs := handleHubLogs(states, logs)
		if len(newStates) != 0 || len(newLogs) != 0 {
			t.Errorf("expected the duplicate to produce no state and no log, got states=%v logs=%v", newStates, newLogs)
		}
	})

	t.Run("produces nothing when every log in the batch duplicates the stored state", func(t *testing.T) {
		states := map[string]HubState{
			"hub1": {Gateway: "hub1", Online: true, Since: t1.Unix()},
		}
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: t2},
			{Id: "hub1", Connected: true, Time: t3},
		}
		newStates, newLogs := handleHubLogs(states, logs)
		if len(newStates) != 0 || len(newLogs) != 0 {
			t.Errorf("expected no state and no log, got states=%v logs=%v", newStates, newLogs)
		}
	})

	t.Run("keeps and writes an id whose online flag changed", func(t *testing.T) {
		states := map[string]HubState{
			"hub1": {Gateway: "hub1", Online: true, Since: t1.Unix()},
		}
		logs := []model.HubLog{
			{Id: "hub1", Connected: false, Time: t2},
		}
		newStates, newLogs := handleHubLogs(states, logs)
		expectedStates := []HubState{
			{Gateway: "hub1", Online: false, Since: t2.Unix()},
		}
		expectedLogs := []model.HubLog{
			{Id: "hub1", Connected: false, Time: t2},
		}
		if !reflect.DeepEqual(expectedStates, newStates) {
			t.Errorf("expected states %v, got %v", expectedStates, newStates)
		}
		if !reflect.DeepEqual(expectedLogs, newLogs) {
			t.Errorf("expected logs %v, got %v", expectedLogs, newLogs)
		}
	})

	t.Run("drops a leading duplicate but keeps the real transitions that follow within the batch", func(t *testing.T) {
		states := map[string]HubState{
			"hub1": {Gateway: "hub1", Online: true, Since: t1.Unix()},
		}
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: t1},  // duplicate of stored state
			{Id: "hub1", Connected: false, Time: t2}, // real transition
			{Id: "hub1", Connected: true, Time: t3},  // real transition back
		}
		newStates, newLogs := handleHubLogs(states, logs)
		expectedStates := []HubState{
			{Gateway: "hub1", Online: true, Since: t3.Unix()},
		}
		expectedLogs := []model.HubLog{
			{Id: "hub1", Connected: false, Time: t2},
			{Id: "hub1", Connected: true, Time: t3},
		}
		if !reflect.DeepEqual(expectedStates, newStates) {
			t.Errorf("expected states %v, got %v", expectedStates, newStates)
		}
		if !reflect.DeepEqual(expectedLogs, newLogs) {
			t.Errorf("expected logs %v (duplicate leading entry dropped), got %v", expectedLogs, newLogs)
		}
	})

	t.Run("processes multiple ids independently in one call", func(t *testing.T) {
		states := map[string]HubState{
			"hub1": {Gateway: "hub1", Online: true, Since: t1.Unix()},  // unchanged -> dropped
			"hub2": {Gateway: "hub2", Online: false, Since: t1.Unix()}, // flips -> kept
		}
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: t2},
			{Id: "hub2", Connected: true, Time: t2},
			{Id: "hub3", Connected: false, Time: t2}, // brand new -> added
		}
		newStates, newLogs := handleHubLogs(states, logs)
		expectedStates := []HubState{
			{Gateway: "hub2", Online: true, Since: t2.Unix()},
			{Gateway: "hub3", Online: false, Since: t2.Unix()},
		}
		expectedLogs := []model.HubLog{
			{Id: "hub2", Connected: true, Time: t2},
			{Id: "hub3", Connected: false, Time: t2},
		}
		sortHubStates(newStates)
		sortHubStates(expectedStates)
		sortHubLogs(newLogs)
		sortHubLogs(expectedLogs)
		if !reflect.DeepEqual(expectedStates, newStates) {
			t.Errorf("expected states %v, got %v", expectedStates, newStates)
		}
		if !reflect.DeepEqual(expectedLogs, newLogs) {
			t.Errorf("expected logs %v, got %v", expectedLogs, newLogs)
		}
	})

	t.Run("ignores stored state for ids that are not part of the batch", func(t *testing.T) {
		states := map[string]HubState{
			"hubUnrelated": {Gateway: "hubUnrelated", Online: true, Since: t1.Unix()},
		}
		newStates, newLogs := handleHubLogs(states, nil)
		if len(newStates) != 0 || len(newLogs) != 0 {
			t.Errorf("expected states and logs to ignore ids outside the batch, got states=%v logs=%v", newStates, newLogs)
		}
	})
}

func TestHandleDeviceLogs(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	t1 := base
	t2 := base.Add(time.Minute)
	t3 := base.Add(2 * time.Minute)

	t.Run("returns nothing for an empty batch", func(t *testing.T) {
		newStates, newLogs := handleDeviceLogs(map[string]DeviceState{}, nil)
		if len(newStates) != 0 || len(newLogs) != 0 {
			t.Errorf("expected no states and no logs, got states=%v logs=%v", newStates, newLogs)
		}
	})

	t.Run("adds a new device and includes its log in the history batch", func(t *testing.T) {
		states := map[string]DeviceState{}
		logs := []model.DeviceLog{
			{Id: "deviceNew", Connected: true, Time: t1},
		}
		newStates, newLogs := handleDeviceLogs(states, logs)
		expectedStates := []DeviceState{
			{Device: "deviceNew", Online: true, Since: t1.Unix()},
		}
		expectedLogs := []model.DeviceLog{
			{Id: "deviceNew", Connected: true, Time: t1},
		}
		if !reflect.DeepEqual(expectedStates, newStates) {
			t.Errorf("expected states %v, got %v", expectedStates, newStates)
		}
		if !reflect.DeepEqual(expectedLogs, newLogs) {
			t.Errorf("expected logs %v, got %v", expectedLogs, newLogs)
		}
	})

	t.Run("drops a single duplicate log that matches the stored state", func(t *testing.T) {
		states := map[string]DeviceState{
			"device1": {Device: "device1", Online: true, Since: t1.Unix()},
		}
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: t2},
		}
		newStates, newLogs := handleDeviceLogs(states, logs)
		if len(newStates) != 0 || len(newLogs) != 0 {
			t.Errorf("expected the duplicate to produce no state and no log, got states=%v logs=%v", newStates, newLogs)
		}
	})

	t.Run("produces nothing when every log in the batch duplicates the stored state", func(t *testing.T) {
		states := map[string]DeviceState{
			"device1": {Device: "device1", Online: true, Since: t1.Unix()},
		}
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: t2},
			{Id: "device1", Connected: true, Time: t3},
		}
		newStates, newLogs := handleDeviceLogs(states, logs)
		if len(newStates) != 0 || len(newLogs) != 0 {
			t.Errorf("expected no state and no log, got states=%v logs=%v", newStates, newLogs)
		}
	})

	t.Run("keeps and writes an id whose online flag changed", func(t *testing.T) {
		states := map[string]DeviceState{
			"device1": {Device: "device1", Online: true, Since: t1.Unix()},
		}
		logs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: t2},
		}
		newStates, newLogs := handleDeviceLogs(states, logs)
		expectedStates := []DeviceState{
			{Device: "device1", Online: false, Since: t2.Unix()},
		}
		expectedLogs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: t2},
		}
		if !reflect.DeepEqual(expectedStates, newStates) {
			t.Errorf("expected states %v, got %v", expectedStates, newStates)
		}
		if !reflect.DeepEqual(expectedLogs, newLogs) {
			t.Errorf("expected logs %v, got %v", expectedLogs, newLogs)
		}
	})

	t.Run("drops a leading duplicate but keeps the real transitions that follow within the batch", func(t *testing.T) {
		states := map[string]DeviceState{
			"device1": {Device: "device1", Online: true, Since: t1.Unix()},
		}
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: t1},  // duplicate of stored state
			{Id: "device1", Connected: false, Time: t2}, // real transition
			{Id: "device1", Connected: true, Time: t3},  // real transition back
		}
		newStates, newLogs := handleDeviceLogs(states, logs)
		expectedStates := []DeviceState{
			{Device: "device1", Online: true, Since: t3.Unix()},
		}
		expectedLogs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: t2},
			{Id: "device1", Connected: true, Time: t3},
		}
		if !reflect.DeepEqual(expectedStates, newStates) {
			t.Errorf("expected states %v, got %v", expectedStates, newStates)
		}
		if !reflect.DeepEqual(expectedLogs, newLogs) {
			t.Errorf("expected logs %v (duplicate leading entry dropped), got %v", expectedLogs, newLogs)
		}
	})

	t.Run("processes multiple ids independently in one call", func(t *testing.T) {
		states := map[string]DeviceState{
			"device1": {Device: "device1", Online: true, Since: t1.Unix()},  // unchanged -> dropped
			"device2": {Device: "device2", Online: false, Since: t1.Unix()}, // flips -> kept
		}
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: t2},
			{Id: "device2", Connected: true, Time: t2},
			{Id: "device3", Connected: false, Time: t2}, // brand new -> added
		}
		newStates, newLogs := handleDeviceLogs(states, logs)
		expectedStates := []DeviceState{
			{Device: "device2", Online: true, Since: t2.Unix()},
			{Device: "device3", Online: false, Since: t2.Unix()},
		}
		expectedLogs := []model.DeviceLog{
			{Id: "device2", Connected: true, Time: t2},
			{Id: "device3", Connected: false, Time: t2},
		}
		sortDeviceStates(newStates)
		sortDeviceStates(expectedStates)
		sortDeviceLogs(newLogs)
		sortDeviceLogs(expectedLogs)
		if !reflect.DeepEqual(expectedStates, newStates) {
			t.Errorf("expected states %v, got %v", expectedStates, newStates)
		}
		if !reflect.DeepEqual(expectedLogs, newLogs) {
			t.Errorf("expected logs %v, got %v", expectedLogs, newLogs)
		}
	})

	t.Run("ignores stored state for ids that are not part of the batch", func(t *testing.T) {
		states := map[string]DeviceState{
			"deviceUnrelated": {Device: "deviceUnrelated", Online: true, Since: t1.Unix()},
		}
		newStates, newLogs := handleDeviceLogs(states, nil)
		if len(newStates) != 0 || len(newLogs) != 0 {
			t.Errorf("expected states and logs to ignore ids outside the batch, got states=%v logs=%v", newStates, newLogs)
		}
	})
}
