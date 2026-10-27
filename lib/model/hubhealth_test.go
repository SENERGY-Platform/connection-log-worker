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
