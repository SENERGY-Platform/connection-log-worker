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
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

func TestHubOfflineTrackingChanges(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	t.Run("nothing for empty input", func(t *testing.T) {
		removeIds, newInfos := hubOfflineTrackingChanges(nil)
		if len(removeIds) != 0 || len(newInfos) != 0 {
			t.Errorf("expected nothing, got remove=%v new=%v", removeIds, newInfos)
		}
	})

	t.Run("offline starts a period at the log time", func(t *testing.T) {
		removeIds, newInfos := hubOfflineTrackingChanges([]model.HubLog{{Id: "h1", Connected: false, Time: t1}})
		expected := []HubOfflineNotificationInfo{{HubId: "h1", OfflineSince: t1.Unix(), Notified: false}}
		if len(removeIds) != 0 || !reflect.DeepEqual(expected, newInfos) {
			t.Errorf("expected new=%v, got remove=%v new=%v", expected, removeIds, newInfos)
		}
	})

	t.Run("online ends the period", func(t *testing.T) {
		removeIds, newInfos := hubOfflineTrackingChanges([]model.HubLog{{Id: "h1", Connected: true, Time: t1}})
		if !reflect.DeepEqual([]string{"h1"}, removeIds) || len(newInfos) != 0 {
			t.Errorf("expected remove=[h1], got remove=%v new=%v", removeIds, newInfos)
		}
	})

	t.Run("several ids are decided independently", func(t *testing.T) {
		removeIds, newInfos := hubOfflineTrackingChanges([]model.HubLog{
			{Id: "h1", Connected: false, Time: t1},
			{Id: "h2", Connected: true, Time: t1},
			{Id: "h3", Connected: false, Time: t2},
		})
		expectedInfos := []HubOfflineNotificationInfo{
			{HubId: "h1", OfflineSince: t1.Unix()},
			{HubId: "h3", OfflineSince: t2.Unix()},
		}
		if !reflect.DeepEqual([]string{"h2"}, removeIds) || !reflect.DeepEqual(expectedInfos, newInfos) {
			t.Errorf("expected remove=[h2] new=%v, got remove=%v new=%v", expectedInfos, removeIds, newInfos)
		}
	})
}

func TestParseHubOfflineNotificationConfig(t *testing.T) {
	conf := func(delay string, interval string) config.Config {
		return config.Config{HubOfflineNotificationDelay: delay, HubOfflineNotificationInterval: interval}
	}

	for _, delay := range []string{"", "-"} {
		enabled, _, _, err := parseHubOfflineNotificationConfig(conf(delay, "garbage"))
		if err != nil || enabled {
			t.Errorf("delay %q: expected disabled without error, got enabled=%v err=%v", delay, enabled, err)
		}
	}

	enabled, delay, interval, err := parseHubOfflineNotificationConfig(conf("30m", "1m"))
	if err != nil || !enabled || delay != 30*time.Minute || interval != time.Minute {
		t.Errorf("unexpected result %v %v %v %v", enabled, delay, interval, err)
	}

	for name, c := range map[string]config.Config{
		"unparsable delay":    conf("soon", "1m"),
		"negative delay":      conf("-1m", "1m"),
		"unparsable interval": conf("30m", "often"),
		"empty interval":      conf("30m", ""),
		"zero interval":       conf("30m", "0s"),
	} {
		if _, _, _, err := parseHubOfflineNotificationConfig(c); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestNewRejectsInvalidHubOfflineNotificationConfig(t *testing.T) {
	// the configuration is validated before any connection is made, so no database is needed
	for name, c := range map[string]config.Config{
		"delay":    {HubOfflineNotificationDelay: "soon", HubOfflineNotificationInterval: "1m"},
		"interval": {HubOfflineNotificationDelay: "30m", HubOfflineNotificationInterval: "often"},
	} {
		_, err := New(t.Context(), c)
		if err == nil || !strings.Contains(err.Error(), "HubOfflineNotification") {
			t.Errorf("%s: expected New to fail on the hub notification config, got %v", name, err)
		}
	}
}
