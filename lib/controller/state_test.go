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
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

func TestGetLastHubStates(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	older := base.Add(-time.Hour)
	newer := base.Add(time.Hour)

	t.Run("returns an empty map for an empty slice", func(t *testing.T) {
		result := getLastHubStates([]model.HubLog{})
		if len(result) != 0 {
			t.Errorf("expected empty map, got %v", result)
		}
	})

	t.Run("keeps the newer log when the newer one comes after the older one", func(t *testing.T) {
		logs := []model.HubLog{
			{Id: "hub1", Connected: false, Time: older},
			{Id: "hub1", Connected: true, Time: newer},
		}
		expected := map[string]model.HubLog{
			"hub1": {Id: "hub1", Connected: true, Time: newer},
		}
		result := getLastHubStates(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("keeps the newer log when the newer one comes before the older one", func(t *testing.T) {
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: newer},
			{Id: "hub1", Connected: false, Time: older},
		}
		expected := map[string]model.HubLog{
			"hub1": {Id: "hub1", Connected: true, Time: newer},
		}
		result := getLastHubStates(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("tracks the most recent log independently per id", func(t *testing.T) {
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: newer},
			{Id: "hub2", Connected: false, Time: older},
			{Id: "hub1", Connected: false, Time: older},
			{Id: "hub2", Connected: true, Time: newer},
		}
		expected := map[string]model.HubLog{
			"hub1": {Id: "hub1", Connected: true, Time: newer},
			"hub2": {Id: "hub2", Connected: true, Time: newer},
		}
		result := getLastHubStates(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("keeps the later element in the slice when timestamps are equal", func(t *testing.T) {
		logs := []model.HubLog{
			{Id: "hub1", Connected: false, Time: base},
			{Id: "hub1", Connected: true, Time: base},
		}
		expected := map[string]model.HubLog{
			"hub1": {Id: "hub1", Connected: true, Time: base},
		}
		result := getLastHubStates(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})
}

func TestGetLastDeviceStates(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	older := base.Add(-time.Hour)
	newer := base.Add(time.Hour)

	t.Run("returns an empty map for an empty slice", func(t *testing.T) {
		result := getLastDeviceStates([]model.DeviceLog{})
		if len(result) != 0 {
			t.Errorf("expected empty map, got %v", result)
		}
	})

	t.Run("keeps the newer log when the newer one comes after the older one", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: older, DeviceName: "old-name"},
			{Id: "device1", Connected: true, Time: newer, DeviceName: "new-name"},
		}
		expected := map[string]model.DeviceLog{
			"device1": {Id: "device1", Connected: true, Time: newer, DeviceName: "new-name"},
		}
		result := getLastDeviceStates(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("keeps the newer log when the newer one comes before the older one", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: newer, DeviceName: "new-name"},
			{Id: "device1", Connected: false, Time: older, DeviceName: "old-name"},
		}
		expected := map[string]model.DeviceLog{
			"device1": {Id: "device1", Connected: true, Time: newer, DeviceName: "new-name"},
		}
		result := getLastDeviceStates(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("tracks the most recent log independently per id", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: newer},
			{Id: "device2", Connected: false, Time: older},
			{Id: "device1", Connected: false, Time: older},
			{Id: "device2", Connected: true, Time: newer},
		}
		expected := map[string]model.DeviceLog{
			"device1": {Id: "device1", Connected: true, Time: newer},
			"device2": {Id: "device2", Connected: true, Time: newer},
		}
		result := getLastDeviceStates(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("keeps the later element in the slice when timestamps are equal", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: base},
			{Id: "device1", Connected: true, Time: base},
		}
		expected := map[string]model.DeviceLog{
			"device1": {Id: "device1", Connected: true, Time: base},
		}
		result := getLastDeviceStates(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})
}
