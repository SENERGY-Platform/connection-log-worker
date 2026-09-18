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
	devicerepo "github.com/SENERGY-Platform/device-repository/lib/client"
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

// TODO DeviceRepository bulk call
func (this *Controller) LogHubs(logs []model.HubLog) error {
	if this.config.Debug {
		for _, log := range logs {
			this.config.GetLogger().Debug("handle hub log update", "hub-log", log)
		}
	}
	ids := make(map[string]struct{})
	for _, log := range logs {
		ids[log.Id] = struct{}{}
	}
	states, err := this.getHubStates(slices.Collect(maps.Keys(ids)))
	if err != nil {
		return err
	}
	newStates, newLogs := handleHubLogs(states, logs)
	err = this.setHubStates(newStates)
	if err != nil {
		return err
	}
	return this.writeHubLogs(newLogs)
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

// TODO DeviceRepository bulk call
// TODO Notifications
func (this *Controller) LogDevices(logs []model.DeviceLog) error {
	if this.config.Debug {
		for _, log := range logs {
			this.config.GetLogger().Debug("handle device log update", "hub-log", log)
		}
	}
	ids := make(map[string]struct{})
	for _, log := range logs {
		ids[log.Id] = struct{}{}
	}
	states, err := this.getDeviceStates(slices.Collect(maps.Keys(ids)))
	if err != nil {
		return err
	}
	newStates, newLogs := handleDeviceLogs(states, logs)
	err = this.setDeviceStates(newStates)
	if err != nil {
		return err
	}
	return this.writeDeviceLogs(newLogs)
}

func handleHubLogs(states map[string]HubState, logs []model.HubLog) ([]HubState, []model.HubLog) {
	logsMap := make(map[string][]model.HubLog)
	for _, log := range logs {
		tmp, ok := logsMap[log.Id]
		if !ok {
			state, ok := states[log.Id]
			if ok && state.Online == log.Connected {
				continue
			}
		} else if tmp[len(tmp)-1].Connected == log.Connected {
			continue
		}
		logsMap[log.Id] = append(tmp, log)
	}
	var newStates []HubState
	for id, lgs := range logsMap {
		lenLogs := len(lgs)
		currentState := lgs[lenLogs-1]
		currentSince := currentState.Time.Unix()
		state, ok := states[id]
		if ok {
			if state.Online == currentState.Connected && lenLogs == 1 {
				delete(states, id)
				continue
			}
		} else {
			state.Gateway = id
		}
		state.Online = currentState.Connected
		state.Since = currentSince
		newStates = append(newStates, state)
	}
	var newLogs []model.HubLog
	for _, state := range newStates {
		newLogs = append(newLogs, logsMap[state.Gateway]...)
	}
	return newStates, newLogs
}

func handleDeviceLogs(states map[string]DeviceState, logs []model.DeviceLog) ([]DeviceState, []model.DeviceLog) {
	logsMap := make(map[string][]model.DeviceLog)
	for _, log := range logs {
		tmp, ok := logsMap[log.Id]
		if !ok {
			state, ok := states[log.Id]
			if ok && state.Online == log.Connected {
				continue
			}
		} else if tmp[len(tmp)-1].Connected == log.Connected {
			continue
		}
		logsMap[log.Id] = append(tmp, log)
	}
	var newStates []DeviceState
	for id, lgs := range logsMap {
		lenLogs := len(lgs)
		currentState := lgs[lenLogs-1]
		currentSince := currentState.Time.Unix()
		state, ok := states[id]
		if ok {
			if state.Online == currentState.Connected && lenLogs == 1 {
				delete(states, id)
				continue
			}
		} else {
			state.Device = id
		}
		state.Online = currentState.Connected
		state.Since = currentSince
		newStates = append(newStates, state)
	}
	var newLogs []model.DeviceLog
	for _, state := range newStates {
		newLogs = append(newLogs, logsMap[state.Device]...)
	}
	return newStates, newLogs
}
