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
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	devicerepo "github.com/SENERGY-Platform/device-repository/lib/client"
	"github.com/influxdata/influxdb/client/v2"
	"gopkg.in/mgo.v2"
	"log"
	"sync"
	"time"
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
	if this.config.Debug {
		log.Println("DEBUG: handle hub log update", hublog)
	}
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

func (this *Controller) LogDevice(devicelog model.DeviceLog) error {
	if this.config.Debug {
		log.Printf("DEBUG: handle device log update %#v\n", devicelog)
	}
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
	} else if this.config.Debug {
		log.Printf("DEBUG: devicelog older than an our -> ignore for handleNotifications")
	}

	return err
}
