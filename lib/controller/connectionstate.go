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
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (this *Controller) setHubState(gatewayLog model.HubLog) (update bool, err error) {
	collection := this.getHubStateCollection()
	ctx, cancel := mongoOperationContext()
	defer cancel()
	count, err := collection.CountDocuments(ctx, bson.M{"gateway": gatewayLog.Id, "online": gatewayLog.Connected}, options.Count().SetLimit(1))
	if err != nil {
		return false, err
	}
	update = count == 0
	if update {
		_, err = collection.ReplaceOne(ctx, bson.M{"gateway": gatewayLog.Id}, HubState{Gateway: gatewayLog.Id, Online: gatewayLog.Connected, Since: time.Now().Unix()}, options.Replace().SetUpsert(true))
	}
	return
}

func (this *Controller) setDeviceState(deviceLog model.DeviceLog) (update bool, err error) {
	collection := this.getDeviceStateCollection()
	ctx, cancel := mongoOperationContext()
	defer cancel()
	count, err := collection.CountDocuments(ctx, bson.M{"device": deviceLog.Id, "online": deviceLog.Connected}, options.Count().SetLimit(1))
	if err != nil {
		return false, err
	}
	update = count == 0
	if update {
		_, err = collection.ReplaceOne(ctx, bson.M{"device": deviceLog.Id}, DeviceState{Device: deviceLog.Id, Online: deviceLog.Connected, Since: time.Now().Unix()}, options.Replace().SetUpsert(true))
	}
	return
}

func (this *Controller) setHubStates(hubLogs []model.HubLog) (err error) {
	var models []mongo.WriteModel
	for _, hubLog := range hubLogs {
		models = append(models, upsertModel(bson.M{"gateway": hubLog.Id}, HubState{
			Gateway: hubLog.Id,
			Online:  hubLog.Connected,
			Since:   hubLog.Time.Unix(),
		}))
	}
	return bulkWrite(this.getHubStateCollection(), models)
}

func (this *Controller) setDeviceStates(deviceLogs []model.DeviceLog) (err error) {
	var models []mongo.WriteModel
	for _, log := range deviceLogs {
		models = append(models, upsertModel(bson.M{"device": log.Id}, DeviceState{
			Device: log.Id,
			Online: log.Connected,
			Since:  log.Time.Unix(),
		}))
	}
	return bulkWrite(this.getDeviceStateCollection(), models)
}

func (this *Controller) getHubStates(ids []string) (map[string]HubState, error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getHubStateCollection().Find(ctx, inFilter("gateway", ids))
	if err != nil {
		return nil, err
	}
	result, err := decodeAll[HubState](ctx, cursor, this.config.GetLogger())
	if err != nil {
		return nil, err
	}
	return sliceToMap(result, func(v HubState) string {
		return v.Gateway
	}), nil
}

func (this *Controller) getDeviceStates(ids []string) (map[string]DeviceState, error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getDeviceStateCollection().Find(ctx, inFilter("device", ids))
	if err != nil {
		return nil, err
	}
	result, err := decodeAll[DeviceState](ctx, cursor, this.config.GetLogger())
	if err != nil {
		return nil, err
	}
	return sliceToMap(result, func(v DeviceState) string {
		return v.Device
	}), nil
}

func (this *Controller) deleteHubState(gwId string) (err error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err = this.getHubStateCollection().DeleteMany(ctx, bson.M{"gateway": gwId})
	return
}

func (this *Controller) deleteDeviceState(deviceId string) (err error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err = this.getDeviceStateCollection().DeleteMany(ctx, bson.M{"device": deviceId})
	return
}

func (this *Controller) deleteHubStates(ids []string) (err error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err = this.getHubStateCollection().DeleteMany(ctx, inFilter("gateway", ids))
	return
}

func (this *Controller) deleteDeviceStates(ids []string) (err error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err = this.getDeviceStateCollection().DeleteMany(ctx, inFilter("device", ids))
	return
}

func sliceToMap[T any](sl []T, keyFunc func(v T) string) map[string]T {
	result := make(map[string]T)
	for _, item := range sl {
		result[keyFunc(item)] = item
	}
	return result
}
