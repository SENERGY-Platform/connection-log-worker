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

package model

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseHubHealthMessage(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		raw := `{"hub_id":"h1","user_id":"u1","time":"2026-10-27T14:00:00.123456789Z","report":{"version":1,"time":"2026-10-27T13:59:58Z","state":2,"reasons":["a","b"],"core":null}}`
		msg, err := ParseHubHealthMessage([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if msg.HubId != "h1" || msg.UserId != "u1" || msg.State != 2 || msg.ReportTime != "2026-10-27T13:59:58Z" ||
			!reflect.DeepEqual(msg.Reasons, []string{"a", "b"}) ||
			!msg.Time.Equal(time.Date(2026, 10, 27, 14, 0, 0, 123456789, time.UTC)) {
			t.Errorf("unexpected message %#v", msg)
		}
		if !strings.Contains(string(msg.Report), `"core":null`) {
			t.Errorf("report must stay verbatim, got %s", msg.Report)
		}
	})

	t.Run("all states are accepted", func(t *testing.T) {
		for _, state := range []string{"0", "1", "2"} {
			raw := `{"hub_id":"h1","time":"2026-10-27T14:00:00Z","report":{"state":` + state + `}}`
			if _, err := ParseHubHealthMessage([]byte(raw)); err != nil {
				t.Errorf("state %s: %v", state, err)
			}
		}
	})

	t.Run("invalid messages are rejected", func(t *testing.T) {
		okTime := `"time":"2026-10-27T14:00:00Z"`
		for name, raw := range map[string]string{
			"no json":           `not json`,
			"empty":             ``,
			"missing hub_id":    `{` + okTime + `,"report":{"state":1}}`,
			"empty hub_id":      `{"hub_id":"",` + okTime + `,"report":{"state":1}}`,
			"missing time":      `{"hub_id":"h1","report":{"state":1}}`,
			"unparseable time":  `{"hub_id":"h1","time":"yesterday","report":{"state":1}}`,
			"missing report":    `{"hub_id":"h1",` + okTime + `}`,
			"report not object": `{"hub_id":"h1",` + okTime + `,"report":"x"}`,
			"missing state":     `{"hub_id":"h1",` + okTime + `,"report":{"reasons":[]}}`,
			"state too high":    `{"hub_id":"h1",` + okTime + `,"report":{"state":3}}`,
			"state negative":    `{"hub_id":"h1",` + okTime + `,"report":{"state":-1}}`,
			"state not integer": `{"hub_id":"h1",` + okTime + `,"report":{"state":1.5}}`,
			"state as string":   `{"hub_id":"h1",` + okTime + `,"report":{"state":"2"}}`,
			"reasons not list":  `{"hub_id":"h1",` + okTime + `,"report":{"state":2,"reasons":"x"}}`,
		} {
			if _, err := ParseHubHealthMessage([]byte(raw)); err == nil {
				t.Errorf("%s: expected an error", name)
			}
		}
	})

	t.Run("errors do not contain the message content", func(t *testing.T) {
		_, err := ParseHubHealthMessage([]byte(`{"hub_id":"secret-hub","time":"secret-time","report":{"state":1}}`))
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("unexpected error %v", err)
		}
	})
}

func TestParseHubHealthMessageModuleVersions(t *testing.T) {
	parse := func(t *testing.T, extra string) HubHealthMessage {
		t.Helper()
		raw := `{"hub_id":"h1","time":"2026-10-27T14:00:00Z","report":{"state":1` + extra + `}}`
		msg, err := ParseHubHealthMessage([]byte(raw))
		if err != nil {
			t.Fatalf("the report must be stored whatever module_versions holds: %v", err)
		}
		return msg
	}

	t.Run("typed entries", func(t *testing.T) {
		msg := parse(t, `,"module_versions":[{"id":"github.com/x/a","name":"A","source":"s","channel":"main","version":"v1","next_version":"v2"},{"id":"github.com/x/b","version":"v3","next_version":""}],"module_versions_error":""`)
		expected := []ModuleVersion{
			{Id: "github.com/x/a", Name: "A", Source: "s", Channel: "main", Version: "v1", NextVersion: "v2"},
			{Id: "github.com/x/b", Version: "v3"},
		}
		if !msg.ModuleVersionsKnown || !reflect.DeepEqual(expected, msg.ModuleVersions) {
			t.Errorf("unexpected %v %#v", msg.ModuleVersionsKnown, msg.ModuleVersions)
		}
	})

	t.Run("empty list is known", func(t *testing.T) {
		msg := parse(t, `,"module_versions":[]`)
		if !msg.ModuleVersionsKnown || len(msg.ModuleVersions) != 0 {
			t.Errorf("unexpected %v %#v", msg.ModuleVersionsKnown, msg.ModuleVersions)
		}
	})

	t.Run("absent and null are unknown", func(t *testing.T) {
		for name, extra := range map[string]string{
			"absent": ``,
			"null":   `,"module_versions":null,"module_versions_error":"module manager unreachable"`,
		} {
			if msg := parse(t, extra); msg.ModuleVersionsKnown || msg.ModuleVersions != nil {
				t.Errorf("%s: expected unknown, got %v %#v", name, msg.ModuleVersionsKnown, msg.ModuleVersions)
			}
		}
	})

	t.Run("malformed is unknown and keeps the report", func(t *testing.T) {
		for name, extra := range map[string]string{
			"object":        `,"module_versions":{"id":"x"}`,
			"string":        `,"module_versions":"x"`,
			"wrong field":   `,"module_versions":[{"id":5}]`,
			"wrong element": `,"module_versions":["x"]`,
		} {
			msg := parse(t, extra)
			if msg.ModuleVersionsKnown || msg.ModuleVersions != nil || msg.State != 1 {
				t.Errorf("%s: expected unknown with the state kept, got %v %#v state %d", name, msg.ModuleVersionsKnown, msg.ModuleVersions, msg.State)
			}
		}
	})

	t.Run("entries without id are skipped", func(t *testing.T) {
		msg := parse(t, `,"module_versions":[{"name":"no id","next_version":"v2"},null,{"id":"","next_version":"v2"},{"id":"a","next_version":"v2"}]`)
		if !msg.ModuleVersionsKnown || !reflect.DeepEqual([]ModuleVersion{{Id: "a", NextVersion: "v2"}}, msg.ModuleVersions) {
			t.Errorf("unexpected %v %#v", msg.ModuleVersionsKnown, msg.ModuleVersions)
		}
	})
}
