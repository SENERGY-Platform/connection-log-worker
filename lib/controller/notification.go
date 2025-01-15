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
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"io"
	"log"
	"net/http"
	"time"
)

func (this *Controller) handleNotifications(devicelog model.DeviceLog) {
	if devicelog.Connected {
		err := this.removeDeviceOfflineNotificationInfos(devicelog.Id)
		if err != nil {
			log.Println("ERROR: removeDeviceOfflineNotificationInfos()", err)
			return
		}
	} else {
		info, exists, err := this.getDeviceOfflineNotificationInfos(devicelog.Id)
		if err != nil {
			log.Println("ERROR: removeDeviceOfflineNotificationInfos()", err)
			return
		}
		if !exists {
			err = this.setDeviceOfflineNotificationInfos(DeviceOfflineNotificationInfo{
				DeviceId:     devicelog.Id,
				OfflineSince: devicelog.Time.Unix(),
				Notified:     false,
			})
			if err != nil {
				log.Println("ERROR: setDeviceOfflineNotificationInfos()", err)
				return
			}
		} else {
			if info.Notified == true || devicelog.MonitorConnectionState == "" || devicelog.DeviceOwner == "" {
				return
			}
			maxDur, err := time.ParseDuration(devicelog.MonitorConnectionState)
			if err != nil {
				log.Println("ERROR: ParseDuration()", err)
				return
			}
			since := time.Since(time.Unix(info.OfflineSince, 0))
			if since > maxDur {
				err = this.sendOfflineNotification(devicelog, since)
				if err != nil {
					log.Println("ERROR: unable to send notification", err)
					return
				}
				info.Notified = true
				err = this.setDeviceOfflineNotificationInfos(info)
				if err != nil {
					log.Println("ERROR: unable to update info with notified flag", err)
					return
				}
			}
		}
	}
}

func (this *Controller) getDeviceOfflineNotificationInfoCollection() (session *mgo.Session, collection *mgo.Collection) {
	session = this.getMongoDb()
	collection = session.DB(this.config.MongoTable).C(this.config.DeviceOfflineNotificationInfoCollection)
	err := collection.EnsureIndexKey("device_id")
	if err != nil {
		log.Fatal("error on getDeviceCollection device index: ", err)
	}
	return
}

type DeviceOfflineNotificationInfo struct {
	DeviceId     string `json:"device_id" bson:"device_id"`
	OfflineSince int64  `json:"offline_since" bson:"offline_since"`
	Notified     bool   `json:"notified" bson:"notified"`
}

func (this *Controller) removeDeviceOfflineNotificationInfos(deviceid string) error {
	session, collection := this.getDeviceOfflineNotificationInfoCollection()
	defer session.Close()
	_, err := collection.RemoveAll(bson.M{"device_id": deviceid})
	if err != nil {
		return err
	}
	return nil
}

func (this *Controller) getDeviceOfflineNotificationInfos(deviceid string) (info DeviceOfflineNotificationInfo, found bool, err error) {
	session, collection := this.getDeviceOfflineNotificationInfoCollection()
	defer session.Close()
	list := []DeviceOfflineNotificationInfo{}
	err = collection.Find(bson.M{"device_id": deviceid}).Limit(1).All(&list)
	if err != nil {
		return info, false, err
	}
	if len(list) == 0 {
		return info, false, nil
	}
	return list[0], true, nil
}

func (this *Controller) setDeviceOfflineNotificationInfos(info DeviceOfflineNotificationInfo) error {
	session, collection := this.getDeviceOfflineNotificationInfoCollection()
	defer session.Close()
	_, err := collection.Upsert(bson.M{"device_id": info.DeviceId}, info)
	if err != nil {
		return err
	}
	return nil
}

type Notification struct {
	UserId  string `json:"userId" bson:"userId"`
	Title   string `json:"title" bson:"title"`
	Message string `json:"message" bson:"message"`
	Topic   string `json:"topic" bson:"topic"`
}

func (this *Controller) sendOfflineNotification(devicelog model.DeviceLog, since time.Duration) error {
	if this.config.Debug {
		log.Printf("DEBUG: send notification for %#v\n", devicelog)
	}
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
	endpoint := this.config.NotificationUrl + "/notifications?ignore_duplicates_within_seconds=3600"
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
