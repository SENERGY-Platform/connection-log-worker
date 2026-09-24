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

package controller

import (
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	devicerepo "github.com/SENERGY-Platform/device-repository/v2/lib/client"
	"github.com/influxdata/influxdb/client/v2"
	"gopkg.in/mgo.v2"
)

type Controller struct {
	config           config.Config
	mongoDbInstance  *mgo.Session
	mongoDbOnce      sync.Once
	influxdbInstance client.Client
	influxdbOnce     sync.Once
	roundTime        time.Duration
	deviceRepo       devicerepo.Interface
}

func New(config config.Config) *Controller {
	roundTime, err := time.ParseDuration(config.RoundTime)
	if err != nil {
		roundTime = time.Minute
	}
	return &Controller{config: config, roundTime: roundTime, deviceRepo: devicerepo.NewClient(config.DeviceRepositoryUrl, nil)}
}

func (this *Controller) LogHub(hublog model.HubLog) error {
	this.config.GetLogger().Debug("handle hub log update", "hub-log", hublog)
	if this.config.DeviceRepositoryUrl != "" && this.config.DeviceRepositoryUrl != "-" {
		err, _ := this.deviceRepo.SetHubConnectionState(devicerepo.InternalAdminToken, hublog.Id, hublog.Connected)
		if err != nil {
			return err
		}
	}
	updated, err := this.setHubState(hublog)
	if err != nil {
		return err
	}
	if updated {
		err = this.logGatewayHistory(hublog)
	}
	return err
}

func (this *Controller) LogHubs(logs []model.HubLog) error {
	if this.config.Debug {
		for _, log := range logs {
			this.config.GetLogger().Debug("handle hub log update", "hub-log", log)
		}
	}
	ids := getUniqueStrings(logs, func(i model.HubLog) string {
		return i.Id
	})
	states, err := this.getHubStates(ids)
	if err != nil {
		return err
	}
	newStates, newLogs := handleHubLogs(states, logs)
	if len(newStates) > 0 {
		if this.config.DeviceRepositoryUrl != "" && this.config.DeviceRepositoryUrl != "-" {
			statesMap := make(map[string]bool)
			for _, state := range newStates {
				statesMap[state.Gateway] = state.Online
			}
			err, _ = this.deviceRepo.SetHubConnectionStates(devicerepo.InternalAdminToken, statesMap)
			if err != nil {
				return err
			}
		}
		err = this.setHubStates(newStates)
		if err != nil {
			return err
		}
	}
	if len(newLogs) > 0 {
		return this.writeHubLogs(newLogs)
	}
	return nil
}

func (this *Controller) LogDevice(devicelog model.DeviceLog) error {
	this.config.GetLogger().Debug("handle device log update", "device-log", devicelog)
	if this.config.DeviceRepositoryUrl != "" && this.config.DeviceRepositoryUrl != "-" {
		err, _ := this.deviceRepo.SetDeviceConnectionState(devicerepo.InternalAdminToken, devicelog.Id, devicelog.Connected)
		if err != nil {
			return err
		}
	}
	updated, err := this.setDeviceState(devicelog)
	if err != nil {
		return err
	}
	if updated {
		err = this.logDeviceHistory(devicelog)
		if err != nil {
			return err
		}
	}
	if time.Since(devicelog.Time) < time.Hour {
		this.handleNotifications(devicelog)
	} else {
		this.config.GetLogger().Debug("devicelog older than an hour -> ignore for handleNotifications")
	}

	return err
}

func (this *Controller) LogDevices(logs []model.DeviceLog) error {
	if this.config.Debug {
		for _, log := range logs {
			this.config.GetLogger().Debug("handle device log update", "device-log", log)
		}
	}
	ids := getUniqueStrings(logs, func(i model.DeviceLog) string {
		return i.Id
	})
	states, err := this.getDeviceStates(ids)
	if err != nil {
		return err
	}
	newStates, newLogs := handleDeviceLogs(states, logs)
	if len(newStates) > 0 {
		if this.config.DeviceRepositoryUrl != "" && this.config.DeviceRepositoryUrl != "-" {
			statesMap := make(map[string]bool)
			for _, state := range newStates {
				statesMap[state.Device] = state.Online
			}
			err, _ = this.deviceRepo.SetDeviceConnectionStates(devicerepo.InternalAdminToken, statesMap)
			if err != nil {
				return err
			}
		}
		err = this.setDeviceStates(newStates)
		if err != nil {
			return err
		}
	}
	if len(newLogs) > 0 {
		err = this.writeDeviceLogs(newLogs)
		if err != nil {
			return err
		}
		for _, log := range newLogs {
			if time.Since(log.Time) < time.Hour {
				this.handleNotifications(log)
			} else {
				this.config.GetLogger().Debug("devicelog older than an hour -> ignore for handleNotifications")
			}
		}
	}
	return nil
}

func handleHubLogs(states map[string]HubState, logs []model.HubLog) ([]HubState, []model.HubLog) {
	return handleConnectionLogs(
		states,
		logs,
		func(l model.HubLog) string { return l.Id },
		func(l model.HubLog) bool { return l.Connected },
		func(l model.HubLog) time.Time { return l.Time },
		func(s HubState) bool { return s.Online },
		func(id string, online bool, since int64) HubState {
			return HubState{Gateway: id, Online: online, Since: since}
		},
	)
}

func handleDeviceLogs(states map[string]DeviceState, logs []model.DeviceLog) ([]DeviceState, []model.DeviceLog) {
	return handleConnectionLogs(
		states,
		logs,
		func(l model.DeviceLog) string { return l.Id },
		func(l model.DeviceLog) bool { return l.Connected },
		func(l model.DeviceLog) time.Time { return l.Time },
		func(s DeviceState) bool { return s.Online },
		func(id string, online bool, since int64) DeviceState {
			return DeviceState{Device: id, Online: online, Since: since}
		},
	)
}

// handleConnectionLogs reduces a batch of connection logs (hub or device) against the
// currently stored state per id, and returns only the states that actually changed
// together with the log entries that caused those changes.
//
// It works in two steps:
//
//  1. Grouping and duplicate filtering. Logs are grouped by id, in the order they
//     appear in the input slice (the slice is assumed to already be chronological).
//     Within each id's group, a log is dropped if it doesn't represent a change:
//     - the first log seen for an id is dropped if that id already has stored
//     state (states[id]) whose Online value equals the log's Connected value -
//     i.e. it's a duplicate report of what's already known, not a new event.
//     - every later log for that id is dropped if it reports the same Connected
//     value as the last log that was kept for that id - i.e. repeated
//     "still connected"/"still disconnected" heartbeats collapse into the log
//     that started that run.
//     What survives is one log per real transition, in order, for each id.
//
//  2. Deciding what to persist. For each id that has at least one surviving log,
//     the last one is used to build a new state (Online = its Connected value,
//     Since = its Time). If that id already had stored state AND its group has
//     exactly one surviving log AND that log's Connected value matches the stored
//     Online value, the id is skipped entirely - no real change happened, the
//     single surviving log was only kept because it was the first one seen in the
//     batch, not because anything changed. Every other id is included in the
//     result, along with all of its surviving logs (not just the last one), so a
//     transition-and-back within one batch still gets its intermediate points
//     written to history.
//
// Examples (stored state per id shown as "online since <Since>"; logs shown as
// "<Connected>@<Time>" in batch order):
//
//   - Brand new id, no stored state:
//     stored: (none)        logs: [true@t1]
//     -> kept: state{Online:true, Since:t1}, logs:[true@t1]
//
//   - Single duplicate log, value matches the stored state:
//     stored: true since t0  logs: [true@t1]
//     -> dropped entirely, id absent from both results
//
//   - Every log in the batch duplicates the stored state:
//     stored: true since t0  logs: [true@t1, true@t2]
//     -> dropped entirely, same as the single-duplicate case
//
//   - Real single transition:
//     stored: true since t0  logs: [false@t1]
//     -> kept: state{Online:false, Since:t1}, logs:[false@t1]
//
//   - Leading duplicate followed by real transitions within the batch:
//     stored: true since t0  logs: [true@t1, false@t2, true@t3]
//     -> true@t1 is dropped as a duplicate of the stored state; false@t2 and
//     true@t3 are each kept because they differ from the previously kept log
//     -> kept: state{Online:true, Since:t3}, logs:[false@t2, true@t3]
//
//   - Transitions back to the original value within the batch - not a no-op,
//     because a real transition happened in between:
//     stored: true since t0  logs: [false@t1, true@t2]
//     -> kept: state{Online:true, Since:t2}, logs:[false@t1, true@t2]
//     (this differs from the single-duplicate case only in having more than one
//     surviving log - that's what marks it as a real change worth persisting,
//     even though Online ends up equal to what was already stored)
//
//   - Multiple ids in one batch, handled independently:
//     stored: {id1: true since t0, id2: false since t0}
//     logs:   [id1 true@t1, id2 true@t2, id3 false@t2]
//     -> id1's log duplicates its stored state -> dropped
//     -> id2 changed -> kept: state{Online:true, Since:t2}, logs:[true@t2]
//     -> id3 is new -> kept: state{Online:false, Since:t2}, logs:[false@t2]
//
//   - An id present in states but absent from logs is ignored: the algorithm
//     only ever looks at ids that appear in the input logs, so it's neither
//     read nor touched.
func handleConnectionLogs[S any, L any](
	states map[string]S,
	logs []L,
	getLogId func(L) string,
	getLogConnection func(L) bool,
	getLogTime func(L) time.Time,
	getStateOnline func(S) bool,
	newState func(id string, online bool, since int64) S,
) ([]S, []L) {
	logsMap := make(map[string][]L)
	for _, log := range logs {
		id := getLogId(log)
		tmp, ok := logsMap[id]
		if !ok {
			state, ok := states[id]
			if ok && getStateOnline(state) == getLogConnection(log) {
				continue
			}
		} else if getLogConnection(tmp[len(tmp)-1]) == getLogConnection(log) {
			continue
		}
		logsMap[id] = append(tmp, log)
	}
	var newStates []S
	var newLogs []L
	for id, lgs := range logsMap {
		lenLogs := len(lgs)
		lastEntry := lgs[lenLogs-1]
		state, ok := states[id]
		if ok && getStateOnline(state) == getLogConnection(lastEntry) && lenLogs == 1 {
			continue
		}
		newStates = append(newStates, newState(id, getLogConnection(lastEntry), getLogTime(lastEntry).Unix()))
		newLogs = append(newLogs, lgs...)
	}
	return newStates, newLogs
}

func getUniqueStrings[T any](sl []T, valFunc func(i T) string) []string {
	tmp := make(map[string]struct{})
	for _, i := range sl {
		tmp[valFunc(i)] = struct{}{}
	}
	return slices.Collect(maps.Keys(tmp))
}
