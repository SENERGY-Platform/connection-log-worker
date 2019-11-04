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
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"gopkg.in/mgo.v2/bson"
)

func (this *Controller) setHubState(gatewayLog model.HubLog) (update bool, err error) {
	session, collection := this.getHubStateCollection()
	defer session.Close()
	count, err := collection.Find(bson.M{"gateway": gatewayLog.Id, "online": gatewayLog.Connected}).Limit(1).Count()
	if err != nil {
		return false, err
	}
	update = count == 0
	if update {
		_, err = collection.Upsert(bson.M{"gateway": gatewayLog.Id}, HubState{Gateway: gatewayLog.Id, Online: gatewayLog.Connected})
	}
	return
}

func (this *Controller) setDeviceState(deviceLog model.DeviceLog) (update bool, err error) {
	session, collection := this.getDeviceStateCollection()
	defer session.Close()
	count, err := collection.Find(bson.M{"device": deviceLog.Id, "online": deviceLog.Connected}).Limit(1).Count()
	if err != nil {
		return false, err
	}
	update = count == 0
	if update {
		_, err = collection.Upsert(bson.M{"device": deviceLog.Id}, DeviceState{Device: deviceLog.Id, Online: deviceLog.Connected})
	}
	return
}
