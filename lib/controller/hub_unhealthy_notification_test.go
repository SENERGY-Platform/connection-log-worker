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

func TestHubUnhealthyTrackingChanges(t *testing.T) {
	t1 := time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC)

	t.Run("nothing for empty input", func(t *testing.T) {
		removeIds, startInfos := hubUnhealthyTrackingChanges(nil)
		if len(removeIds) != 0 || len(startInfos) != 0 {
			t.Errorf("expected nothing, got remove=%v start=%v", removeIds, startInfos)
		}
	})

	t.Run("unhealthy starts a period at the received time", func(t *testing.T) {
		removeIds, startInfos := hubUnhealthyTrackingChanges([]HubHealth{{HubId: "h1", State: model.HubHealthUnhealthy, Received: t1}})
		expected := []HubUnhealthyNotificationInfo{{HubId: "h1", UnhealthySince: t1.Unix()}}
		if len(removeIds) != 0 || !reflect.DeepEqual(expected, startInfos) {
			t.Errorf("expected start=%v, got remove=%v start=%v", expected, removeIds, startInfos)
		}
	})

	t.Run("healthy ends the period, undetermined changes nothing", func(t *testing.T) {
		removeIds, startInfos := hubUnhealthyTrackingChanges([]HubHealth{
			{HubId: "h1", State: model.HubHealthHealthy, Received: t1},
			{HubId: "h2", State: model.HubHealthUndetermined, Received: t1},
		})
		if !reflect.DeepEqual([]string{"h1"}, removeIds) || len(startInfos) != 0 {
			t.Errorf("expected remove=[h1], got remove=%v start=%v", removeIds, startInfos)
		}
	})

	// applies the changes like the database does: removal first, then insert-if-absent
	sequence := func(states ...int) map[string]HubUnhealthyNotificationInfo {
		tracked := map[string]HubUnhealthyNotificationInfo{}
		for i, state := range states {
			removeIds, startInfos := hubUnhealthyTrackingChanges([]HubHealth{{HubId: "h", State: state, Received: t1.Add(time.Duration(i) * time.Minute)}})
			for _, id := range removeIds {
				delete(tracked, id)
			}
			for _, info := range startInfos {
				if _, exists := tracked[info.HubId]; !exists {
					tracked[info.HubId] = info
				}
			}
		}
		return tracked
	}
	since := func(minutes int) map[string]HubUnhealthyNotificationInfo {
		return map[string]HubUnhealthyNotificationInfo{"h": {HubId: "h", UnhealthySince: t1.Add(time.Duration(minutes) * time.Minute).Unix()}}
	}

	for name, c := range map[string]struct {
		states   []int
		expected map[string]HubUnhealthyNotificationInfo
	}{
		"2,2 keeps the start of the period": {[]int{2, 2}, since(0)},
		"2,2,1 ends the period":             {[]int{2, 2, 1}, map[string]HubUnhealthyNotificationInfo{}},
		"2,0,2 keeps the period":            {[]int{2, 0, 2}, since(0)},
		"0 starts nothing":                  {[]int{0}, map[string]HubUnhealthyNotificationInfo{}},
		"1 starts nothing":                  {[]int{1}, map[string]HubUnhealthyNotificationInfo{}},
		"2,1,2 starts a new period":         {[]int{2, 1, 2}, since(2)},
	} {
		t.Run(name, func(t *testing.T) {
			if got := sequence(c.states...); !reflect.DeepEqual(c.expected, got) {
				t.Errorf("expected %v, got %v", c.expected, got)
			}
		})
	}
}

func TestParseHubUnhealthyNotificationConfig(t *testing.T) {
	conf := func(delay string, interval string) config.Config {
		return config.Config{HubUnhealthyNotificationDelay: delay, HubUnhealthyNotificationInterval: interval}
	}

	for _, delay := range []string{"", "-"} {
		enabled, _, _, err := parseHubUnhealthyNotificationConfig(conf(delay, "garbage"))
		if err != nil || enabled {
			t.Errorf("delay %q: expected disabled without error, got enabled=%v err=%v", delay, enabled, err)
		}
	}

	enabled, delay, interval, err := parseHubUnhealthyNotificationConfig(conf("10m", "1m"))
	if err != nil || !enabled || delay != 10*time.Minute || interval != time.Minute {
		t.Errorf("unexpected result %v %v %v %v", enabled, delay, interval, err)
	}

	for name, c := range map[string]config.Config{
		"unparsable delay":    conf("soon", "1m"),
		"negative delay":      conf("-1m", "1m"),
		"unparsable interval": conf("10m", "often"),
		"empty interval":      conf("10m", ""),
		"zero interval":       conf("10m", "0s"),
	} {
		_, _, _, err := parseHubUnhealthyNotificationConfig(c)
		if err == nil || !strings.Contains(err.Error(), "HubUnhealthyNotification") {
			t.Errorf("%s: expected an error naming the config key, got %v", name, err)
		}
	}
}

func TestNewRejectsInvalidHubUnhealthyNotificationConfig(t *testing.T) {
	// the configuration is validated before any connection is made, so no database is needed
	for name, c := range map[string]config.Config{
		"delay":    {HubUnhealthyNotificationDelay: "soon", HubUnhealthyNotificationInterval: "1m"},
		"interval": {HubUnhealthyNotificationDelay: "10m", HubUnhealthyNotificationInterval: "often"},
	} {
		_, err := New(t.Context(), c)
		if err == nil || !strings.Contains(err.Error(), "HubUnhealthyNotification") {
			t.Errorf("%s: expected New to fail on the hub unhealthy notification config, got %v", name, err)
		}
	}
}

func TestUnhealthyNotificationMessage(t *testing.T) {
	since := time.Date(2026, 10, 27, 14, 0, 0, 0, time.FixedZone("x", 3600))

	got := unhealthyNotificationMessage("my net", "hub-1", since, []string{"module a: unhealthy", "update failed"})
	expected := "network my net (hub-1) has been unhealthy since 2026-10-27T13:00:00Z: module a: unhealthy; update failed"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}

	if got = unhealthyNotificationMessage("n", "h", since, nil); got != "network n (h) has been unhealthy since 2026-10-27T13:00:00Z" {
		t.Errorf("unexpected message without reasons %q", got)
	}

	// truncation counts characters, not bytes
	long := strings.Repeat("ä", 1500)
	got = unhealthyNotificationMessage("n", "h", since, []string{long})
	reasons := strings.TrimPrefix(got, "network n (h) has been unhealthy since 2026-10-27T13:00:00Z: ")
	if reasons != strings.Repeat("ä", 1000)+"..." {
		t.Errorf("expected 1000 characters and an ellipsis, got %d runes", len([]rune(reasons)))
	}

	exact := strings.Repeat("x", 1000)
	got = unhealthyNotificationMessage("n", "h", since, []string{exact})
	if !strings.HasSuffix(got, ": "+exact) {
		t.Errorf("exactly 1000 characters must not be cut")
	}
}
