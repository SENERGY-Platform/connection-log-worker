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
			this.config.GetLogger().Debug("handle device log update", "hub-log", log)
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
