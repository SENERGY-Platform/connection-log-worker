/*
 * Copyright 2019 InfAI (CC SES)
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
	"encoding/json"
	"errors"
	"time"

	"github.com/SENERGY-Platform/models/go/models"
)

type HubLog struct {
	Id        string    `json:"id"`
	Connected bool      `json:"connected"`
	Time      time.Time `json:"time"`
}

type DeviceLog struct {
	Id                     string    `json:"id"`
	Connected              bool      `json:"connected"`
	Time                   time.Time `json:"time"`
	MonitorConnectionState string    `json:"monitor_connection_state"`
	DeviceOwner            string    `json:"device_owner"`
	DeviceName             string    `json:"device_name"`
}

type DeviceCommand struct {
	Command string `json:"command"`
	Id      string `json:"id"`
	Owner   string `json:"owner"`
}

// HubCommand is a message of the hubs topic; Hub is the full hub on PUT.
type HubCommand struct {
	Command string     `json:"command"`
	Id      string     `json:"id"`
	Owner   string     `json:"owner"`
	Hub     models.Hub `json:"hub"`
}

// HubHealthMessage is a health report as the platform-connector publishes it. Time is when the
// connector accepted the publish and orders reports; the gateway's own clock is only in Report.
// State, Reasons and ReportTime are filled by ParseHubHealthMessage from Report.
type HubHealthMessage struct {
	HubId  string          `json:"hub_id"`
	UserId string          `json:"user_id"`
	Time   time.Time       `json:"time"`
	Report json.RawMessage `json:"report"`

	State      int      `json:"-"`
	Reasons    []string `json:"-"`
	ReportTime string   `json:"-"`
}

const (
	HubHealthUndetermined = 0
	HubHealthHealthy      = 1
	HubHealthUnhealthy    = 2
)

// ParseHubHealthMessage rejects messages the worker cannot store; callers skip those instead of failing.
// Error texts are fixed strings, so they are safe to log.
func ParseHubHealthMessage(raw []byte) (msg HubHealthMessage, err error) {
	if err = json.Unmarshal(raw, &msg); err != nil {
		return msg, errors.New("invalid json or unparseable time")
	}
	if msg.HubId == "" {
		return msg, errors.New("missing hub_id")
	}
	if msg.Time.IsZero() {
		return msg, errors.New("missing time")
	}
	if len(msg.Report) == 0 || msg.Report[0] != '{' {
		return msg, errors.New("report is not an object")
	}
	var report struct {
		State   *int     `json:"state"`
		Reasons []string `json:"reasons"`
		Time    string   `json:"time"`
	}
	if err = json.Unmarshal(msg.Report, &report); err != nil {
		return msg, errors.New("invalid report.state, report.reasons or report.time")
	}
	if report.State == nil {
		return msg, errors.New("missing report.state")
	}
	if *report.State < HubHealthUndetermined || *report.State > HubHealthUnhealthy {
		return msg, errors.New("report.state out of range")
	}
	msg.State, msg.Reasons, msg.ReportTime = *report.State, report.Reasons, report.Time
	return msg, nil
}
