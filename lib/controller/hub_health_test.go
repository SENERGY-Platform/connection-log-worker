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
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"go.mongodb.org/mongo-driver/bson"
)

func healthMessage(hub string, at time.Time, state int) model.HubHealthMessage {
	return model.HubHealthMessage{HubId: hub, Time: at, State: state, Report: json.RawMessage(`{"state":1}`)}
}

func TestNewestHubHealthPerHub(t *testing.T) {
	t1 := time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Second)
	t3 := t1.Add(2 * time.Second)

	t.Run("nothing for empty input", func(t *testing.T) {
		if got := newestHubHealthPerHub(nil); len(got) != 0 {
			t.Errorf("expected nothing, got %v", got)
		}
	})

	t.Run("newest wins regardless of position", func(t *testing.T) {
		got := newestHubHealthPerHub([]model.HubHealthMessage{
			healthMessage("h1", t2, 2),
			healthMessage("h1", t3, 1),
			healthMessage("h1", t1, 0),
		})
		if len(got) != 1 || got[0].State != 1 || !got[0].Time.Equal(t3) {
			t.Errorf("expected the report at t3, got %v", got)
		}
	})

	t.Run("equal times: the later message wins", func(t *testing.T) {
		got := newestHubHealthPerHub([]model.HubHealthMessage{healthMessage("h1", t1, 1), healthMessage("h1", t1, 2)})
		if len(got) != 1 || got[0].State != 2 {
			t.Errorf("expected the second report, got %v", got)
		}
	})

	t.Run("hubs are independent and keep first-appearance order", func(t *testing.T) {
		got := newestHubHealthPerHub([]model.HubHealthMessage{
			healthMessage("h2", t1, 1),
			healthMessage("h1", t2, 2),
			healthMessage("h2", t3, 2),
		})
		if len(got) != 2 || got[0].HubId != "h2" || got[0].State != 2 || got[1].HubId != "h1" || got[1].State != 2 {
			t.Errorf("unexpected result %v", got)
		}
	})
}

func TestToHubHealth(t *testing.T) {
	at := time.Date(2026, 10, 27, 14, 0, 0, 123456789, time.FixedZone("x", 3600))
	msg := model.HubHealthMessage{
		HubId:      "h1",
		Time:       at,
		State:      2,
		ReportTime: "2026-10-27T13:59:58Z",
		Report:     json.RawMessage(`{"state":2,"count":4,"ratio":0.5,"big":12345678901234,"core":null,"modules":{"github.com/x/y":{"restarts":4,"list":[1,"a"]}}}`),
	}
	got, err := toHubHealth(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got.HubId != "h1" || got.State != 2 || got.ReportTime != msg.ReportTime || !got.Received.Equal(at) || got.Received.Location() != time.UTC {
		t.Errorf("unexpected document %#v", got)
	}
	if got.Reasons == nil || len(got.Reasons) != 0 {
		t.Errorf("reasons must be an empty list, not null: %#v", got.Reasons)
	}
	expected := bson.M{
		"state": int64(2), "count": int64(4), "ratio": 0.5, "big": int64(12345678901234), "core": nil,
		"modules": bson.M{"github.com/x/y": bson.M{"restarts": int64(4), "list": bson.A{int64(1), "a"}}},
	}
	if !reflect.DeepEqual(expected, got.Report) {
		t.Errorf("expected report %#v, got %#v", expected, got.Report)
	}

	t.Run("extended json markers stay plain data", func(t *testing.T) {
		msg.Report = json.RawMessage(`{"state":1,"x":{"$numberLong":"5"}}`)
		got, err := toHubHealth(msg)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(bson.M{"state": int64(1), "x": bson.M{"$numberLong": "5"}}, got.Report) {
			t.Errorf("unexpected report %#v", got.Report)
		}
	})

	t.Run("report that is not an object", func(t *testing.T) {
		msg.Report = json.RawMessage(`[1]`)
		if _, err := toHubHealth(msg); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestJsonObjectToBsonRejectsUnstorableReports(t *testing.T) {
	deep := strings.Repeat(`{"x":`, maxReportDepth+1) + `1` + strings.Repeat(`}`, maxReportDepth+1)
	for name, raw := range map[string]string{
		"nul in key":     `{"a\u0000b":1}`,
		"nul nested":     `{"a":[{"b\u0000":1}]}`,
		"nested too far": deep,
	} {
		if _, err := jsonObjectToBson([]byte(raw)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	ok := strings.Repeat(`{"x":`, maxReportDepth) + `1` + strings.Repeat(`}`, maxReportDepth)
	if _, err := jsonObjectToBson([]byte(ok)); err != nil {
		t.Errorf("depth %d must be accepted: %v", maxReportDepth, err)
	}
}
