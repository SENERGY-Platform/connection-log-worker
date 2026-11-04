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

func TestHubModuleUpdateChanges(t *testing.T) {
	t1 := time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC)

	t.Run("only modules with a next version are pending", func(t *testing.T) {
		got := hubModuleUpdateChanges("h", t1, []model.ModuleVersion{
			{Id: "a", Name: "A", Version: "v1", NextVersion: "v2"},
			{Id: "b", Version: "v1"},
			{Id: "", NextVersion: "v9"},
			{Id: "a", NextVersion: "v3"},
		})
		expected := []HubModuleUpdateInfo{{HubId: "h", ModuleId: "a", Name: "A", Version: "v1", NextVersion: "v2", PendingSince: t1.Unix()}}
		if !reflect.DeepEqual(expected, got) {
			t.Errorf("expected %v, got %v", expected, got)
		}
	})

	t.Run("nothing pending for an empty list", func(t *testing.T) {
		if got := hubModuleUpdateChanges("h", t1, nil); len(got) != 0 {
			t.Errorf("unexpected %v", got)
		}
	})

	// applies the changes like the database does: upsert with insert-only pending_since and notified,
	// then removal of every other module of the hub; nil stands for an unknown list
	sequence := func(lists ...[]model.ModuleVersion) map[string]HubModuleUpdateInfo {
		tracked := map[string]HubModuleUpdateInfo{}
		for i, list := range lists {
			if list == nil {
				continue
			}
			pending := hubModuleUpdateChanges("h", t1.Add(time.Duration(i)*time.Minute), list)
			keep := map[string]bool{}
			for _, info := range pending {
				keep[info.ModuleId] = true
				if existing, ok := tracked[info.ModuleId]; ok {
					existing.Name, existing.Version, existing.NextVersion = info.Name, info.Version, info.NextVersion
					tracked[info.ModuleId] = existing
				} else {
					tracked[info.ModuleId] = info
				}
			}
			for id := range tracked {
				if !keep[id] {
					delete(tracked, id)
				}
			}
		}
		return tracked
	}
	pendingModule := func(id string, next string) []model.ModuleVersion {
		return []model.ModuleVersion{{Id: id, Version: "v1", NextVersion: next}}
	}
	known := []model.ModuleVersion{}
	at := func(id string, version string, next string, minutes int) map[string]HubModuleUpdateInfo {
		return map[string]HubModuleUpdateInfo{id: {HubId: "h", ModuleId: id, Version: version, NextVersion: next, PendingSince: t1.Add(time.Duration(minutes) * time.Minute).Unix()}}
	}

	for name, c := range map[string]struct {
		lists    [][]model.ModuleVersion
		expected map[string]HubModuleUpdateInfo
	}{
		"update appears":                          {[][]model.ModuleVersion{pendingModule("a", "v2")}, at("a", "v1", "v2", 0)},
		"newer next version keeps the period":     {[][]model.ModuleVersion{pendingModule("a", "v2"), pendingModule("a", "v3")}, at("a", "v1", "v3", 0)},
		"unknown list changes nothing":            {[][]model.ModuleVersion{pendingModule("a", "v2"), nil}, at("a", "v1", "v2", 0)},
		"known empty list removes":                {[][]model.ModuleVersion{pendingModule("a", "v2"), known}, map[string]HubModuleUpdateInfo{}},
		"no next version removes":                 {[][]model.ModuleVersion{pendingModule("a", "v2"), pendingModule("a", "")}, map[string]HubModuleUpdateInfo{}},
		"module missing from the list is removed": {[][]model.ModuleVersion{pendingModule("a", "v2"), pendingModule("b", "")}, map[string]HubModuleUpdateInfo{}},
		"updated and outdated again starts anew":  {[][]model.ModuleVersion{pendingModule("a", "v2"), pendingModule("a", ""), pendingModule("a", "v3")}, at("a", "v1", "v3", 2)},
		"unknown between keeps the period":        {[][]model.ModuleVersion{pendingModule("a", "v2"), nil, pendingModule("a", "v3")}, at("a", "v1", "v3", 0)},
	} {
		t.Run(name, func(t *testing.T) {
			if got := sequence(c.lists...); !reflect.DeepEqual(c.expected, got) {
				t.Errorf("expected %v, got %v", c.expected, got)
			}
		})
	}
}

func TestParseHubModuleOutdatedConfig(t *testing.T) {
	conf := func(age string, interval string) config.Config {
		return config.Config{HubModuleOutdatedAge: age, HubModuleOutdatedNotificationInterval: interval}
	}

	age, enabled, interval, err := parseHubModuleOutdatedConfig(conf("168h", "1m"))
	if err != nil || !enabled || age != 168*time.Hour || interval != time.Minute {
		t.Errorf("unexpected result %v %v %v %v", age, enabled, interval, err)
	}

	age, enabled, _, err = parseHubModuleOutdatedConfig(conf("", ""))
	if err != nil || enabled || age != 7*24*time.Hour {
		t.Errorf("empty values must select the default age and disable the notification, got %v %v %v", age, enabled, err)
	}

	for _, interval := range []string{"", "-"} {
		age, enabled, _, err := parseHubModuleOutdatedConfig(conf("1h", interval))
		if err != nil || enabled || age != time.Hour {
			t.Errorf("interval %q: expected disabled with the age kept, got %v %v %v", interval, age, enabled, err)
		}
	}

	for name, c := range map[string]config.Config{
		"unparsable age":      conf("soon", "1m"),
		"zero age":            conf("0s", "1m"),
		"negative age":        conf("-1h", "1m"),
		"unparsable interval": conf("1h", "often"),
		"zero interval":       conf("1h", "0s"),
		"negative interval":   conf("1h", "-1m"),
	} {
		_, _, _, err := parseHubModuleOutdatedConfig(c)
		if err == nil || !strings.Contains(err.Error(), "HubModuleOutdated") {
			t.Errorf("%s: expected an error naming the config key, got %v", name, err)
		}
	}
}

func TestNewRejectsInvalidHubModuleOutdatedConfig(t *testing.T) {
	// the configuration is validated before any connection is made, so no database is needed
	for name, c := range map[string]config.Config{
		"age":      {HubModuleOutdatedAge: "soon", HubModuleOutdatedNotificationInterval: "1m"},
		"interval": {HubModuleOutdatedAge: "1h", HubModuleOutdatedNotificationInterval: "often"},
	} {
		_, err := New(t.Context(), c)
		if err == nil || !strings.Contains(err.Error(), "HubModuleOutdated") {
			t.Errorf("%s: expected New to fail on the module outdated config, got %v", name, err)
		}
	}
}

func TestOutdatedModulesNotificationMessage(t *testing.T) {
	t1 := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	infos := []HubModuleUpdateInfo{
		{ModuleId: "github.com/x/b", Name: "", Version: "v1.0", NextVersion: "v1.1", PendingSince: t1.Add(time.Hour).Unix()},
		{ModuleId: "github.com/x/a", Name: "MQTT Device Connector", Version: "v1.0.36", NextVersion: "v1.0.40", PendingSince: t1.Unix()},
	}
	got := outdatedModulesNotificationMessage("my net", "hub-1", infos)
	expected := "network my net (hub-1) has module updates pending since 2026-10-20T14:00:00Z: MQTT Device Connector v1.0.36 -> v1.0.40; github.com/x/b v1.0 -> v1.1"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
	if infos[0].ModuleId != "github.com/x/b" {
		t.Error("the input must not be reordered")
	}

	// truncation counts characters, not bytes
	long := []HubModuleUpdateInfo{{ModuleId: "a", Name: strings.Repeat("ä", 1500), Version: "v1", NextVersion: "v2", PendingSince: t1.Unix()}}
	got = outdatedModulesNotificationMessage("n", "h", long)
	list := strings.TrimPrefix(got, "network n (h) has module updates pending since 2026-10-20T14:00:00Z: ")
	if !strings.HasPrefix(list, strings.Repeat("ä", 1000)) || len([]rune(list)) != 1003 || !strings.HasSuffix(list, "...") {
		t.Errorf("expected 1000 characters and an ellipsis, got %d runes", len([]rune(list)))
	}
}
