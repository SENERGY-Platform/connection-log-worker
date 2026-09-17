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

func TestFilterHubLogs(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	t1 := base
	t2 := base.Add(time.Minute)
	t3 := base.Add(2 * time.Minute)
	t4 := base.Add(3 * time.Minute)

	t.Run("returns an empty map for an empty slice", func(t *testing.T) {
		result := filterHubLogs([]model.HubLog{})
		if len(result) != 0 {
			t.Errorf("expected empty map, got %v", result)
		}
	})

	t.Run("keeps a single log when there is only one per id", func(t *testing.T) {
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: t1},
		}
		expected := map[string][]model.HubLog{
			"hub1": {{Id: "hub1", Connected: true, Time: t1}},
		}
		result := filterHubLogs(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("collapses repeated connected states down to the point where the state changed", func(t *testing.T) {
		// t1=connected, t2=connected, t3=connected, t4=disconnected -> t1=connected, t4=disconnected
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: t1},
			{Id: "hub1", Connected: true, Time: t2},
			{Id: "hub1", Connected: true, Time: t3},
			{Id: "hub1", Connected: false, Time: t4},
		}
		expected := map[string][]model.HubLog{
			"hub1": {
				{Id: "hub1", Connected: true, Time: t1},
				{Id: "hub1", Connected: false, Time: t4},
			},
		}
		result := filterHubLogs(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("keeps every point where the state flips back and forth", func(t *testing.T) {
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: t1},
			{Id: "hub1", Connected: false, Time: t2},
			{Id: "hub1", Connected: true, Time: t3},
		}
		expected := map[string][]model.HubLog{
			"hub1": {
				{Id: "hub1", Connected: true, Time: t1},
				{Id: "hub1", Connected: false, Time: t2},
				{Id: "hub1", Connected: true, Time: t3},
			},
		}
		result := filterHubLogs(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("filters independently per id", func(t *testing.T) {
		logs := []model.HubLog{
			{Id: "hub1", Connected: true, Time: t1},
			{Id: "hub2", Connected: false, Time: t1},
			{Id: "hub1", Connected: true, Time: t2},
			{Id: "hub2", Connected: false, Time: t2},
			{Id: "hub1", Connected: false, Time: t3},
			{Id: "hub2", Connected: true, Time: t3},
		}
		expected := map[string][]model.HubLog{
			"hub1": {
				{Id: "hub1", Connected: true, Time: t1},
				{Id: "hub1", Connected: false, Time: t3},
			},
			"hub2": {
				{Id: "hub2", Connected: false, Time: t1},
				{Id: "hub2", Connected: true, Time: t3},
			},
		}
		result := filterHubLogs(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})
}

func TestFilterDeviceLogs(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	t1 := base
	t2 := base.Add(time.Minute)
	t3 := base.Add(2 * time.Minute)
	t4 := base.Add(3 * time.Minute)

	t.Run("returns an empty map for an empty slice", func(t *testing.T) {
		result := filterDeviceLogs([]model.DeviceLog{})
		if len(result) != 0 {
			t.Errorf("expected empty map, got %v", result)
		}
	})

	t.Run("keeps a single log when there is only one per id", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: t1},
		}
		expected := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: true, Time: t1}},
		}
		result := filterDeviceLogs(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("collapses repeated connected states down to the point where the state changed", func(t *testing.T) {
		// t1=connected, t2=connected, t3=connected, t4=disconnected -> t1=connected, t4=disconnected
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: t1},
			{Id: "device1", Connected: true, Time: t2},
			{Id: "device1", Connected: true, Time: t3},
			{Id: "device1", Connected: false, Time: t4},
		}
		expected := map[string][]model.DeviceLog{
			"device1": {
				{Id: "device1", Connected: true, Time: t1},
				{Id: "device1", Connected: false, Time: t4},
			},
		}
		result := filterDeviceLogs(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("keeps every point where the state flips back and forth", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: t1},
			{Id: "device1", Connected: false, Time: t2},
			{Id: "device1", Connected: true, Time: t3},
		}
		expected := map[string][]model.DeviceLog{
			"device1": {
				{Id: "device1", Connected: true, Time: t1},
				{Id: "device1", Connected: false, Time: t2},
				{Id: "device1", Connected: true, Time: t3},
			},
		}
		result := filterDeviceLogs(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("filters independently per id", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: true, Time: t1},
			{Id: "device2", Connected: false, Time: t1},
			{Id: "device1", Connected: true, Time: t2},
			{Id: "device2", Connected: false, Time: t2},
			{Id: "device1", Connected: false, Time: t3},
			{Id: "device2", Connected: true, Time: t3},
		}
		expected := map[string][]model.DeviceLog{
			"device1": {
				{Id: "device1", Connected: true, Time: t1},
				{Id: "device1", Connected: false, Time: t3},
			},
			"device2": {
				{Id: "device2", Connected: false, Time: t1},
				{Id: "device2", Connected: true, Time: t3},
			},
		}
		result := filterDeviceLogs(logs)
		if !reflect.DeepEqual(expected, result) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})
}
