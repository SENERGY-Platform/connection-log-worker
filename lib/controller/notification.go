/*
 * Copyright 2025 InfAI (CC SES)
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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (this *Controller) handleNotifications(devicelog model.DeviceLog) {
	if devicelog.Connected {
		err := this.removeDeviceOfflineNotificationInfos(devicelog.Id)
		if err != nil {
			this.config.GetLogger().Error("unable to remove offline notification infos", "device-id", devicelog.Id, "error", err)
			return
		}
	} else {
		info, exists, err := this.getDeviceOfflineNotificationInfos(devicelog.Id)
		if err != nil {
			this.config.GetLogger().Error("unable to get offline notification infos", "device-id", devicelog.Id, "error", err)
			return
		}
		if !exists {
			err = this.setDeviceOfflineNotificationInfos(DeviceOfflineNotificationInfo{
				DeviceId:     devicelog.Id,
				OfflineSince: devicelog.Time.Unix(),
				Notified:     false,
			})
			if err != nil {
				this.config.GetLogger().Error("unable to set offline notification infos", "device-id", devicelog.Id, "error", err)
				return
			}
		} else {
			if info.Notified == true || devicelog.MonitorConnectionState == "" || devicelog.DeviceOwner == "" {
				return
			}
			maxDur, err := time.ParseDuration(devicelog.MonitorConnectionState)
			if err != nil {
				this.sendMonitorParseErrorNotification(devicelog, err)
				this.config.GetLogger().Error("unable to parse MonitorConnectionState as duration", "device-id", devicelog.Id, "error", err)
				return
			}
			since := time.Since(time.Unix(info.OfflineSince, 0))
			if since > maxDur {
				err = this.sendOfflineNotification(devicelog, since)
				if err != nil {
					this.config.GetLogger().Error("unable to send notification", "device-id", devicelog.Id, "error", err)
					return
				}
				info.Notified = true
				err = this.setDeviceOfflineNotificationInfos(info)
				if err != nil {
					this.config.GetLogger().Error("unable to update info with notified flag", "device-id", devicelog.Id, "error", err)
					return
				}
			}
		}
	}
}

func (this *Controller) handleNotificationsBatch(deviceStates []model.DeviceLog) {
	var connected []string
	var disconnected []model.DeviceLog
	for _, deviceState := range deviceStates {
		if deviceState.Connected {
			connected = append(connected, deviceState.Id)
		} else {
			disconnected = append(disconnected, deviceState)
		}
	}
	if len(connected) > 0 {
		err := this.removeDeviceOfflineNotificationInfosBatch(connected)
		if err != nil {
			this.config.GetLogger().Error(
				"unable to remove offline notification infos",
				"device-ids", strings.Join(connected, ","),
				"error", err,
			)
			return
		}
	}
	if len(disconnected) == 0 {
		return
	}
	disconnectedIds := getUniqueStrings(disconnected, func(i model.DeviceLog) string {
		return i.Id
	})
	infos, err := this.getDeviceOfflineNotificationInfosBatch(disconnectedIds)
	if err != nil {
		this.config.GetLogger().Error(
			"unable to get offline notification infos",
			"device-ids", strings.Join(disconnectedIds, ","),
			"error", err,
		)
		return
	}
	var newInfos []DeviceOfflineNotificationInfo
	var changedInfos []DeviceOfflineNotificationInfo
	notifications := make(map[string][][3]string)
	parseErrors := make(map[string][][3]string)
	for _, deviceState := range disconnected {
		info, ok := infos[deviceState.Id]
		if !ok {
			info = DeviceOfflineNotificationInfo{
				DeviceId:     deviceState.Id,
				OfflineSince: deviceState.Time.Unix(),
			}
			newInfos = append(newInfos, info)
			infos[deviceState.Id] = info
		}
		if info.Notified == true || deviceState.MonitorConnectionState == "" || deviceState.DeviceOwner == "" {
			continue
		}
		maxDur, err := time.ParseDuration(deviceState.MonitorConnectionState)
		if err != nil {
			parseErrors[deviceState.DeviceOwner] = append(parseErrors[deviceState.DeviceOwner], [3]string{deviceState.Id, deviceState.DeviceName, err.Error()})
			this.config.GetLogger().Error("unable to parse MonitorConnectionState as duration", "device-id", deviceState.Id, "error", err)
			continue
		}
		since := time.Since(time.Unix(info.OfflineSince, 0))
		if since > maxDur {
			notifications[deviceState.DeviceOwner] = append(notifications[deviceState.DeviceOwner], [3]string{deviceState.Id, deviceState.DeviceName, since.Round(this.roundTime).String()})
			info.Notified = true
			changedInfos = append(changedInfos, info)
		}
	}
	if len(newInfos) > 0 {
		err = this.setDeviceOfflineNotificationInfosBatch(newInfos)
		if err != nil {
			this.config.GetLogger().Error(
				"unable to set offline notification infos",
				"device-ids", strings.Join(getUniqueStrings(newInfos, func(i DeviceOfflineNotificationInfo) string {
					return i.DeviceId
				}), ","),
				"error", err,
			)
			return
		}
	}
	for owner, errs := range parseErrors {
		this.sendMonitorParseErrorNotificationBatch(owner, errs)
	}
	for owner, batch := range notifications {
		err = this.sendOfflineNotificationBatch(owner, batch)
		if err != nil {
			this.config.GetLogger().Error("unable to send notification", "device-ids", getUniqueStrings(batch, func(i [3]string) string {
				return i[0]
			}), "error", err)
			return
		}
	}
	if len(changedInfos) > 0 {
		err = this.setDeviceOfflineNotificationInfosBatch(changedInfos)
		if err != nil {
			this.config.GetLogger().Error(
				"unable to update info with notified flag",
				"device-ids", strings.Join(getUniqueStrings(changedInfos, func(i DeviceOfflineNotificationInfo) string {
					return i.DeviceId
				}), ","),
				"error", err,
			)
			return
		}
	}
}

func (this *Controller) getDeviceOfflineNotificationInfoCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.DeviceOfflineNotificationInfoCollection)
}

type DeviceOfflineNotificationInfo struct {
	DeviceId     string `json:"device_id" bson:"device_id"`
	OfflineSince int64  `json:"offline_since" bson:"offline_since,truncate"`
	Notified     bool   `json:"notified" bson:"notified"`
}

func (this *Controller) removeDeviceOfflineNotificationInfos(deviceid string) error {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := this.getDeviceOfflineNotificationInfoCollection().DeleteMany(ctx, bson.M{"device_id": deviceid})
	if err != nil {
		return err
	}
	return nil
}

func (this *Controller) removeDeviceOfflineNotificationInfosBatch(deviceIds []string) error {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := this.getDeviceOfflineNotificationInfoCollection().DeleteMany(ctx, inFilter("device_id", deviceIds))
	if err != nil {
		return err
	}
	return nil
}

func (this *Controller) getDeviceOfflineNotificationInfos(deviceid string) (info DeviceOfflineNotificationInfo, found bool, err error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getDeviceOfflineNotificationInfoCollection().Find(ctx, bson.M{"device_id": deviceid}, options.Find().SetLimit(1))
	if err != nil {
		return info, false, err
	}
	list, err := decodeAll[DeviceOfflineNotificationInfo](ctx, cursor, this.config.GetLogger())
	if err != nil {
		return info, false, err
	}
	if len(list) == 0 {
		return info, false, nil
	}
	return list[0], true, nil
}

func (this *Controller) getDeviceOfflineNotificationInfosBatch(deviceIds []string) (map[string]DeviceOfflineNotificationInfo, error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getDeviceOfflineNotificationInfoCollection().Find(ctx, inFilter("device_id", deviceIds))
	if err != nil {
		return nil, err
	}
	result, err := decodeAll[DeviceOfflineNotificationInfo](ctx, cursor, this.config.GetLogger())
	if err != nil {
		return nil, err
	}
	return sliceToMap(result, func(v DeviceOfflineNotificationInfo) string {
		return v.DeviceId
	}), nil
}

func (this *Controller) setDeviceOfflineNotificationInfos(info DeviceOfflineNotificationInfo) error {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := this.getDeviceOfflineNotificationInfoCollection().ReplaceOne(ctx, bson.M{"device_id": info.DeviceId}, info, options.Replace().SetUpsert(true))
	if err != nil {
		return err
	}
	return nil
}

func (this *Controller) setDeviceOfflineNotificationInfosBatch(infos []DeviceOfflineNotificationInfo) error {
	var models []mongo.WriteModel
	for _, info := range infos {
		models = append(models, upsertModel(bson.M{"device_id": info.DeviceId}, info))
	}
	return bulkWrite(this.getDeviceOfflineNotificationInfoCollection(), models)
}

type Notification struct {
	UserId  string `json:"userId" bson:"userId"`
	Title   string `json:"title" bson:"title"`
	Message string `json:"message" bson:"message"`
	Topic   string `json:"topic" bson:"topic"`
}

func (this *Controller) sendOfflineNotification(devicelog model.DeviceLog, since time.Duration) error {
	this.config.GetLogger().Debug("send offline notification", "device-log", fmt.Sprintf("%#v", devicelog))
	b := new(bytes.Buffer)
	err := json.NewEncoder(b).Encode(Notification{
		UserId:  devicelog.DeviceOwner,
		Title:   "Device Offline",
		Message: fmt.Sprintf("device %v (%v) has been offline for %v", devicelog.DeviceName, devicelog.Id, since.Round(this.roundTime).String()),
		Topic:   "device_offline",
	})
	if err != nil {
		return err
	}
	endpoint := this.config.NotificationUrl + "/notifications"
	req, err := http.NewRequest("POST", endpoint, b)
	if err != nil {
		return err
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		respMsg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected response status from notifier %v %v", resp.Status, string(respMsg))
	}
	return nil
}

func (this *Controller) sendOfflineNotificationBatch(owner string, batch [][3]string) error {
	this.config.GetLogger().Debug("send offline notification", "device-ids", getUniqueStrings(batch, func(i [3]string) string {
		return i[0]
	}))
	b := new(bytes.Buffer)
	err := json.NewEncoder(b).Encode(Notification{
		UserId: owner,
		Title:  "Devices Offline",
		Message: fmt.Sprintf(
			"offline devices:\n%s",
			strings.Join(func() []string {
				var tmp []string
				for _, item := range batch {
					tmp = append(tmp, fmt.Sprintf("id=%s name=%s since=%s", item[0], item[1], item[2]))
				}
				return tmp
			}(), "\n")),
		Topic: "device_offline",
	})
	if err != nil {
		return err
	}
	endpoint := this.config.NotificationUrl + "/notifications"
	req, err := http.NewRequest("POST", endpoint, b)
	if err != nil {
		return err
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		respMsg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected response status from notifier %v %v", resp.Status, string(respMsg))
	}
	return nil
}

func (this *Controller) sendMonitorParseErrorNotification(devicelog model.DeviceLog, err error) {
	this.config.GetLogger().Debug("send parse error notification", "device-log", fmt.Sprintf("%#v", devicelog))
	b := new(bytes.Buffer)
	err = json.NewEncoder(b).Encode(Notification{
		UserId:  devicelog.DeviceOwner,
		Title:   "Device monitor_connection_state Attribute Invalid",
		Message: fmt.Sprintf("device %v (%v) has an invalid monitor_connection_state attribute (allowed time-shorthands are s,m,h); error = %v", devicelog.DeviceName, devicelog.Id, err.Error()),
		Topic:   "device_offline",
	})
	if err != nil {
		this.config.GetLogger().Error("unable to encode notification", "error", err)
		return
	}
	endpoint := this.config.NotificationUrl + "/notifications?ignore_duplicates_within_seconds=86400"
	req, err := http.NewRequest("POST", endpoint, b)
	if err != nil {
		this.config.GetLogger().Error("unable to create notification request", "error", err)
		return
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		this.config.GetLogger().Error("unable to send notification", "error", err)
		return
	}
	if resp.StatusCode >= 300 {
		respMsg, _ := io.ReadAll(resp.Body)
		this.config.GetLogger().Error("unexpected response status from notifier", "status-code", resp.StatusCode, "error", string(respMsg))
	}
	return
}

func (this *Controller) sendMonitorParseErrorNotificationBatch(owner string, deviceErrs [][3]string) {
	this.config.GetLogger().Debug("send parse error notifications", "device-ids", getUniqueStrings(deviceErrs, func(i [3]string) string {
		return i[0]
	}))
	b := new(bytes.Buffer)
	err := json.NewEncoder(b).Encode(Notification{
		UserId: owner,
		Title:  "Device monitor_connection_state Attributes Invalid",
		Message: fmt.Sprintf(
			"invalid monitor_connection_state attributes (allowed time-shorthands are s,m,h):\n%s",
			strings.Join(func() []string {
				var tmp []string
				for _, item := range deviceErrs {
					tmp = append(tmp, fmt.Sprintf("id=%s name=%s error=%s", item[0], item[1], item[2]))
				}
				return tmp
			}(), "\n")),
		Topic: "device_offline",
	})
	if err != nil {
		this.config.GetLogger().Error("unable to encode notification", "error", err)
		return
	}
	endpoint := this.config.NotificationUrl + "/notifications?ignore_duplicates_within_seconds=86400"
	req, err := http.NewRequest("POST", endpoint, b)
	if err != nil {
		this.config.GetLogger().Error("unable to create notification request", "error", err)
		return
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		this.config.GetLogger().Error("unable to send notification", "error", err)
		return
	}
	if resp.StatusCode >= 300 {
		respMsg, _ := io.ReadAll(resp.Body)
		this.config.GetLogger().Error("unexpected response status from notifier", "status-code", resp.StatusCode, "error", string(respMsg))
	}
	return
}
