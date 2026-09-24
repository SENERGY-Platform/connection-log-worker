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
	"time"

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
		_, err = collection.Upsert(bson.M{"gateway": gatewayLog.Id}, HubState{Gateway: gatewayLog.Id, Online: gatewayLog.Connected, Since: time.Now().Unix()})
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
		_, err = collection.Upsert(bson.M{"device": deviceLog.Id}, DeviceState{Device: deviceLog.Id, Online: deviceLog.Connected, Since: time.Now().Unix()})
	}
	return
}

func (this *Controller) setHubStates(hubLogs []model.HubLog) (err error) {
	session, collection := this.getHubStateCollection()
	defer session.Close()
	bulk := collection.Bulk()
	for _, hubLog := range hubLogs {
		bulk.Upsert(bson.M{"gateway": hubLog.Id}, HubState{
			Gateway: hubLog.Id,
			Online:  hubLog.Connected,
			Since:   hubLog.Time.Unix(),
		})
	}
	_, err = bulk.Run()
	return
}

func (this *Controller) setDeviceStates(deviceLogs []model.DeviceLog) (err error) {
	session, collection := this.getDeviceStateCollection()
	defer session.Close()
	bulk := collection.Bulk()
	for _, log := range deviceLogs {
		bulk.Upsert(bson.M{"device": log.Id}, DeviceState{
			Device: log.Id,
			Online: log.Connected,
			Since:  log.Time.Unix(),
		})
	}
	_, err = bulk.Run()
	return
}

func (this *Controller) getHubStates(ids []string) (map[string]HubState, error) {
	session, collection := this.getHubStateCollection()
	defer session.Close()
	var result []HubState
	err := collection.Find(bson.M{"gateway": bson.M{"$in": ids}}).All(&result)
	if err != nil {
		return nil, err
	}
	return sliceToMap(result, func(v HubState) string {
		return v.Gateway
	}), nil
}

func (this *Controller) getDeviceStates(ids []string) (map[string]DeviceState, error) {
	session, collection := this.getDeviceStateCollection()
	defer session.Close()
	var result []DeviceState
	err := collection.Find(bson.M{"device": bson.M{"$in": ids}}).All(&result)
	if err != nil {
		return nil, err
	}
	return sliceToMap(result, func(v DeviceState) string {
		return v.Device
	}), nil
}

func (this *Controller) deleteHubState(gwId string) (err error) {
	session, collection := this.getHubStateCollection()
	defer session.Close()
	_, err = collection.RemoveAll(bson.M{"gateway": gwId})
	return
}

func (this *Controller) deleteDeviceState(deviceId string) (err error) {
	session, collection := this.getDeviceStateCollection()
	defer session.Close()
	_, err = collection.RemoveAll(bson.M{"device": deviceId})
	return
}

func (this *Controller) deleteHubStates(ids []string) (err error) {
	session, collection := this.getHubStateCollection()
	defer session.Close()
	_, err = collection.RemoveAll(bson.M{"gateway": bson.M{"$in": ids}})
	return
}

func (this *Controller) deleteDeviceStates(ids []string) (err error) {
	session, collection := this.getDeviceStateCollection()
	defer session.Close()
	_, err = collection.RemoveAll(bson.M{"device": bson.M{"$in": ids}})
	return
}

func sliceToMap[T any](sl []T, keyFunc func(v T) string) map[string]T {
	result := make(map[string]T)
	for _, item := range sl {
		result[keyFunc(item)] = item
	}
	return result
}
