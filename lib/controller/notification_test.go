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

func TestGroupDeviceLogsForNotifications(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	t1 := now.Add(-50 * time.Minute)
	t2 := now.Add(-40 * time.Minute)
	t3 := now.Add(-30 * time.Minute)
	old := now.Add(-2 * time.Hour)

	t.Run("returns nothing for an empty batch", func(t *testing.T) {
		groups, orderedIds := groupDeviceLogsForNotifications(nil, now)
		if len(groups) != 0 || len(orderedIds) != 0 {
			t.Errorf("expected no groups and no ids, got groups=%v ids=%v", groups, orderedIds)
		}
	})

	t.Run("drops logs older than an hour", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: old},
		}
		groups, orderedIds := groupDeviceLogsForNotifications(logs, now)
		if len(groups) != 0 || len(orderedIds) != 0 {
			t.Errorf("expected the stale log to be dropped, got groups=%v ids=%v", groups, orderedIds)
		}
	})

	t.Run("keeps a recent log even for a brand new id", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: t1},
		}
		groups, orderedIds := groupDeviceLogsForNotifications(logs, now)
		expectedGroups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: false, Time: t1}},
		}
		if !reflect.DeepEqual(expectedGroups, groups) {
			t.Errorf("expected groups %v, got %v", expectedGroups, groups)
		}
		if !reflect.DeepEqual([]string{"device1"}, orderedIds) {
			t.Errorf("expected orderedIds %v, got %v", []string{"device1"}, orderedIds)
		}
	})

	t.Run("collapses consecutive logs with the same connected value", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: t1},
			{Id: "device1", Connected: false, Time: t2},
		}
		groups, _ := groupDeviceLogsForNotifications(logs, now)
		expected := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: t1},
		}
		if !reflect.DeepEqual(expected, groups["device1"]) {
			t.Errorf("expected %v, got %v", expected, groups["device1"])
		}
	})

	t.Run("keeps every transition when the state flips within the batch", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: t1},
			{Id: "device1", Connected: true, Time: t2},
			{Id: "device1", Connected: false, Time: t3},
		}
		groups, _ := groupDeviceLogsForNotifications(logs, now)
		expected := []model.DeviceLog{
			{Id: "device1", Connected: false, Time: t1},
			{Id: "device1", Connected: true, Time: t2},
			{Id: "device1", Connected: false, Time: t3},
		}
		if !reflect.DeepEqual(expected, groups["device1"]) {
			t.Errorf("expected %v, got %v", expected, groups["device1"])
		}
	})

	t.Run("lists ids in first-appearance order", func(t *testing.T) {
		logs := []model.DeviceLog{
			{Id: "device2", Connected: false, Time: t1},
			{Id: "device1", Connected: false, Time: t1},
			{Id: "device2", Connected: true, Time: t2},
		}
		_, orderedIds := groupDeviceLogsForNotifications(logs, now)
		expected := []string{"device2", "device1"}
		if !reflect.DeepEqual(expected, orderedIds) {
			t.Errorf("expected %v, got %v", expected, orderedIds)
		}
	})
}

func TestComputeOfflineNotificationChanges(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	roundTime := time.Minute

	t.Run("starts tracking a new offline period without notifying immediately", func(t *testing.T) {
		infos := map[string]DeviceOfflineNotificationInfo{}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: false, Time: now, DeviceOwner: "owner1", MonitorConnectionState: "1s"}},
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if len(removeIds) != 0 || len(changedInfos) != 0 || len(notifications) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected only a new tracked info and nothing else, got removeIds=%v changedInfos=%v notifications=%v parseErrors=%v",
				removeIds, changedInfos, notifications, parseErrors)
		}
		expected := []DeviceOfflineNotificationInfo{
			{DeviceId: "device1", OfflineSince: now.Unix()},
		}
		if !reflect.DeepEqual(expected, newInfos) {
			t.Errorf("expected newInfos %v, got %v", expected, newInfos)
		}
	})

	t.Run("notifies once the tracked offline period exceeds the configured duration", func(t *testing.T) {
		offlineSince := now.Add(-40 * time.Minute)
		infos := map[string]DeviceOfflineNotificationInfo{
			"device1": {DeviceId: "device1", OfflineSince: offlineSince.Unix(), Notified: false},
		}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: false, Time: now, DeviceOwner: "owner1", DeviceName: "d1", MonitorConnectionState: "30m"}},
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if len(removeIds) != 0 || len(newInfos) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected only a notification and a changed info, got removeIds=%v newInfos=%v parseErrors=%v", removeIds, newInfos, parseErrors)
		}
		expectedChanged := []DeviceOfflineNotificationInfo{
			{DeviceId: "device1", OfflineSince: offlineSince.Unix(), Notified: true},
		}
		if !reflect.DeepEqual(expectedChanged, changedInfos) {
			t.Errorf("expected changedInfos %v, got %v", expectedChanged, changedInfos)
		}
		if len(notifications["owner1"]) != 1 || notifications["owner1"][0][0] != "device1" {
			t.Errorf("expected a notification for owner1/device1, got %v", notifications)
		}
	})

	t.Run("does not notify again once already notified for the same offline period", func(t *testing.T) {
		offlineSince := now.Add(-2 * time.Hour)
		infos := map[string]DeviceOfflineNotificationInfo{
			"device1": {DeviceId: "device1", OfflineSince: offlineSince.Unix(), Notified: true},
		}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: false, Time: now, DeviceOwner: "owner1", MonitorConnectionState: "30m"}},
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if len(removeIds) != 0 || len(newInfos) != 0 || len(changedInfos) != 0 || len(notifications) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected no changes at all, got removeIds=%v newInfos=%v changedInfos=%v notifications=%v parseErrors=%v",
				removeIds, newInfos, changedInfos, notifications, parseErrors)
		}
	})

	t.Run("does not notify while the offline duration has not been exceeded yet", func(t *testing.T) {
		offlineSince := now.Add(-10 * time.Minute)
		infos := map[string]DeviceOfflineNotificationInfo{
			"device1": {DeviceId: "device1", OfflineSince: offlineSince.Unix(), Notified: false},
		}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: false, Time: now, DeviceOwner: "owner1", MonitorConnectionState: "30m"}},
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if len(removeIds) != 0 || len(newInfos) != 0 || len(changedInfos) != 0 || len(notifications) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected no changes at all, got removeIds=%v newInfos=%v changedInfos=%v notifications=%v parseErrors=%v",
				removeIds, newInfos, changedInfos, notifications, parseErrors)
		}
	})

	t.Run("clears tracking when the device reconnects", func(t *testing.T) {
		infos := map[string]DeviceOfflineNotificationInfo{
			"device1": {DeviceId: "device1", OfflineSince: now.Add(-time.Hour).Unix(), Notified: true},
		}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: true, Time: now}},
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if !reflect.DeepEqual([]string{"device1"}, removeIds) {
			t.Errorf("expected removeIds [device1], got %v", removeIds)
		}
		if len(newInfos) != 0 || len(changedInfos) != 0 || len(notifications) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected only removeIds to be set, got newInfos=%v changedInfos=%v notifications=%v parseErrors=%v",
				newInfos, changedInfos, notifications, parseErrors)
		}
	})

	t.Run("a reconnect with nothing tracked is a no-op", func(t *testing.T) {
		infos := map[string]DeviceOfflineNotificationInfo{}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: true, Time: now}},
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if len(removeIds) != 0 || len(newInfos) != 0 || len(changedInfos) != 0 || len(notifications) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected no changes at all, got removeIds=%v newInfos=%v changedInfos=%v notifications=%v parseErrors=%v",
				removeIds, newInfos, changedInfos, notifications, parseErrors)
		}
	})

	t.Run("a reconnect then a new disconnect within one batch starts a fresh, un-notified period", func(t *testing.T) {
		// stale info from a previous, already-notified offline period
		infos := map[string]DeviceOfflineNotificationInfo{
			"device1": {DeviceId: "device1", OfflineSince: now.Add(-2 * time.Hour).Unix(), Notified: true},
		}
		reconnectAt := now.Add(-10 * time.Minute)
		groups := map[string][]model.DeviceLog{
			"device1": {
				{Id: "device1", Connected: true, Time: reconnectAt},
				{Id: "device1", Connected: false, Time: now, DeviceOwner: "owner1", MonitorConnectionState: "30m"},
			},
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if !reflect.DeepEqual([]string{"device1"}, removeIds) {
			t.Errorf("expected the stale info to be removed, got %v", removeIds)
		}
		expectedNew := []DeviceOfflineNotificationInfo{
			{DeviceId: "device1", OfflineSince: now.Unix()},
		}
		if !reflect.DeepEqual(expectedNew, newInfos) {
			t.Errorf("expected a fresh, un-notified info %v, got %v", expectedNew, newInfos)
		}
		if len(changedInfos) != 0 || len(notifications) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected no notification for the brand new period, got changedInfos=%v notifications=%v parseErrors=%v",
				changedInfos, notifications, parseErrors)
		}
	})

	t.Run("records a parse error for an invalid MonitorConnectionState and does not notify", func(t *testing.T) {
		infos := map[string]DeviceOfflineNotificationInfo{
			"device1": {DeviceId: "device1", OfflineSince: now.Add(-time.Hour).Unix(), Notified: false},
		}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: false, Time: now, DeviceOwner: "owner1", DeviceName: "d1", MonitorConnectionState: "not-a-duration"}},
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if len(removeIds) != 0 || len(newInfos) != 0 || len(changedInfos) != 0 || len(notifications) != 0 {
			t.Errorf("expected only a parse error, got removeIds=%v newInfos=%v changedInfos=%v notifications=%v", removeIds, newInfos, changedInfos, notifications)
		}
		if len(parseErrors["owner1"]) != 1 || parseErrors["owner1"][0][0] != "device1" {
			t.Errorf("expected a parse error for owner1/device1, got %v", parseErrors)
		}
	})

	t.Run("skips a tracked device missing MonitorConnectionState or DeviceOwner", func(t *testing.T) {
		infos := map[string]DeviceOfflineNotificationInfo{
			"device1": {DeviceId: "device1", OfflineSince: now.Add(-time.Hour).Unix(), Notified: false},
		}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: false, Time: now}}, // no owner, no MonitorConnectionState
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1"}, now, roundTime)

		if len(removeIds) != 0 || len(newInfos) != 0 || len(changedInfos) != 0 || len(notifications) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected no changes at all, got removeIds=%v newInfos=%v changedInfos=%v notifications=%v parseErrors=%v",
				removeIds, newInfos, changedInfos, notifications, parseErrors)
		}
	})

	t.Run("processes multiple ids independently in one call", func(t *testing.T) {
		infos := map[string]DeviceOfflineNotificationInfo{
			"device2": {DeviceId: "device2", OfflineSince: now.Add(-time.Hour).Unix(), Notified: false},
		}
		groups := map[string][]model.DeviceLog{
			"device1": {{Id: "device1", Connected: false, Time: now, DeviceOwner: "owner1", MonitorConnectionState: "1s"}},  // brand new
			"device2": {{Id: "device2", Connected: false, Time: now, DeviceOwner: "owner2", MonitorConnectionState: "30m"}}, // exceeds threshold
		}
		removeIds, newInfos, changedInfos, notifications, parseErrors :=
			computeOfflineNotificationChanges(infos, groups, []string{"device1", "device2"}, now, roundTime)

		if len(removeIds) != 0 || len(parseErrors) != 0 {
			t.Errorf("expected no removals and no parse errors, got removeIds=%v parseErrors=%v", removeIds, parseErrors)
		}

		sortInfos := func(s []DeviceOfflineNotificationInfo) {
			sort.Slice(s, func(i, j int) bool { return s[i].DeviceId < s[j].DeviceId })
		}
		sortInfos(newInfos)
		expectedNew := []DeviceOfflineNotificationInfo{
			{DeviceId: "device1", OfflineSince: now.Unix()},
		}
		if !reflect.DeepEqual(expectedNew, newInfos) {
			t.Errorf("expected newInfos %v, got %v", expectedNew, newInfos)
		}

		expectedChanged := []DeviceOfflineNotificationInfo{
			{DeviceId: "device2", OfflineSince: now.Add(-time.Hour).Unix(), Notified: true},
		}
		if !reflect.DeepEqual(expectedChanged, changedInfos) {
			t.Errorf("expected changedInfos %v, got %v", expectedChanged, changedInfos)
		}
		if len(notifications["owner2"]) != 1 || notifications["owner2"][0][0] != "device2" {
			t.Errorf("expected a notification for owner2/device2, got %v", notifications)
		}
		if len(notifications["owner1"]) != 0 {
			t.Errorf("expected no notification for owner1 yet, got %v", notifications["owner1"])
		}
	})
}
