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
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
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

// handleNotificationsBatch mirrors handleNotifications, but for a whole batch of raw
// device logs at once. It operates on the raw logs straight from LogDevices - not on
// handleDeviceLogs' deduplicated result, which only ever carries the final state per id -
// so a device that stays offline across many batches keeps getting re-evaluated on every
// "still offline" report, exactly like the single-message path does.
//
// The decision logic itself is pure (groupDeviceLogsForNotifications and
// computeOfflineNotificationChanges below) so it can be unit tested without a live
// Mongo/HTTP dependency; this method only wires that decision to the actual reads,
// writes and outgoing notifications.
func (this *Controller) handleNotificationsBatch(deviceLogs []model.DeviceLog) {
	now := time.Now()
	groups, orderedIds := groupDeviceLogsForNotifications(deviceLogs, now)
	if len(orderedIds) == 0 {
		return
	}

	infos, err := this.getDeviceOfflineNotificationInfosBatch(orderedIds)
	if err != nil {
		this.config.GetLogger().Error("unable to get offline notification infos", "device-ids", strings.Join(orderedIds, ","), "error", err)
		return
	}

	removeIds, newInfos, changedInfos, notifications, parseErrors :=
		computeOfflineNotificationChanges(infos, groups, orderedIds, now, this.roundTime)

	if len(removeIds) > 0 {
		err = this.removeDeviceOfflineNotificationInfosBatch(removeIds)
		if err != nil {
			this.config.GetLogger().Error("unable to remove offline notification infos", "device-ids", strings.Join(removeIds, ","), "error", err)
			return
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
		for _, deviceErr := range errs {
			this.config.GetLogger().Error("unable to parse MonitorConnectionState as duration", "device-id", deviceErr[0], "error", deviceErr[2])
		}
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

// groupDeviceLogsForNotifications drops logs older than an hour (mirroring LogDevice's
// per-log recency check) and groups the rest by device id, preserving order, collapsing
// consecutive logs that report the same Connected value. orderedIds lists the ids in the
// order their first surviving log appeared, so callers can look up stored info in one
// batched, deterministic call.
func groupDeviceLogsForNotifications(logs []model.DeviceLog, now time.Time) (groups map[string][]model.DeviceLog, orderedIds []string) {
	groups = make(map[string][]model.DeviceLog)
	for _, l := range logs {
		if now.Sub(l.Time) >= time.Hour {
			continue
		}
		group, ok := groups[l.Id]
		if ok && group[len(group)-1].Connected == l.Connected {
			continue
		}
		if !ok {
			orderedIds = append(orderedIds, l.Id)
		}
		groups[l.Id] = append(group, l)
	}
	return groups, orderedIds
}

// computeOfflineNotificationChanges replays each id's (already grouped, chronological)
// logs against its currently stored DeviceOfflineNotificationInfo, if any, and decides
// what needs to change. It never sends anything or touches storage itself.
//
// For each id, a local "tracked" flag starts out reflecting whatever infos[id] says
// (found or not), and is then updated log by log:
//   - a connected log clears tracking: if something was tracked, its id is added to
//     removeIds and tracked becomes false. A device that was never tracked is a no-op.
//   - a disconnected log, when nothing is tracked, starts a new tracked period: a new
//     DeviceOfflineNotificationInfo{OfflineSince: this log's Time} is added to newInfos,
//     and nothing else happens for that log - in particular it is never immediately
//     checked against the duration threshold, since it just started.
//   - a disconnected log, when something is already tracked, is checked: if it was
//     already Notified, or has no MonitorConnectionState/DeviceOwner, nothing happens.
//     If MonitorConnectionState fails to parse as a duration, the error is recorded
//     under parseErrors[DeviceOwner] and nothing further happens for that log. Otherwise,
//     if now minus the tracked OfflineSince exceeds that duration, the device is recorded
//     under notifications[DeviceOwner], the tracked info is marked Notified and added to
//     changedInfos.
//
// Because a connected log always clears tracking first, a device that reconnects and
// disconnects again within the same batch starts a genuinely new (un-Notified) tracked
// period - any stale, already-Notified info from before is cleared, not reused. And
// because a device already marked Notified is skipped before the duration check ever
// runs again, an ongoing offline period is never notified twice, no matter how many
// batches - or how many logs within one batch - it spans.
func computeOfflineNotificationChanges(
	infos map[string]DeviceOfflineNotificationInfo,
	groups map[string][]model.DeviceLog,
	orderedIds []string,
	now time.Time,
	roundTime time.Duration,
) (removeIds []string, newInfos []DeviceOfflineNotificationInfo, changedInfos []DeviceOfflineNotificationInfo, notifications map[string][][3]string, parseErrors map[string][][3]string) {
	notifications = make(map[string][][3]string)
	parseErrors = make(map[string][][3]string)

	for _, id := range orderedIds {
		info, tracked := infos[id]
		for _, l := range groups[id] {
			if l.Connected {
				if tracked {
					removeIds = append(removeIds, id)
					tracked = false
				}
				continue
			}
			if !tracked {
				info = DeviceOfflineNotificationInfo{
					DeviceId:     id,
					OfflineSince: l.Time.Unix(),
				}
				newInfos = append(newInfos, info)
				tracked = true
				continue
			}
			if info.Notified || l.MonitorConnectionState == "" || l.DeviceOwner == "" {
				continue
			}
			maxDur, err := time.ParseDuration(l.MonitorConnectionState)
			if err != nil {
				parseErrors[l.DeviceOwner] = append(parseErrors[l.DeviceOwner], [3]string{l.Id, l.DeviceName, err.Error()})
				continue
			}
			since := now.Sub(time.Unix(info.OfflineSince, 0))
			if since > maxDur {
				notifications[l.DeviceOwner] = append(notifications[l.DeviceOwner], [3]string{l.Id, l.DeviceName, since.Round(roundTime).String()})
				info.Notified = true
				changedInfos = append(changedInfos, info)
			}
		}
	}
	return removeIds, newInfos, changedInfos, notifications, parseErrors
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

func (this *Controller) removeDeviceOfflineNotificationInfosBatch(deviceIds []string) error {
	session, collection := this.getDeviceOfflineNotificationInfoCollection()
	defer session.Close()
	_, err := collection.RemoveAll(bson.M{"device_id": bson.M{"$in": deviceIds}})
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

func (this *Controller) getDeviceOfflineNotificationInfosBatch(deviceIds []string) (map[string]DeviceOfflineNotificationInfo, error) {
	session, collection := this.getDeviceOfflineNotificationInfoCollection()
	defer session.Close()
	var result []DeviceOfflineNotificationInfo
	err := collection.Find(bson.M{"device_id": bson.M{"$in": deviceIds}}).All(&result)
	if err != nil {
		return nil, err
	}
	return sliceToMap(result, func(v DeviceOfflineNotificationInfo) string {
		return v.DeviceId
	}), nil
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

func (this *Controller) setDeviceOfflineNotificationInfosBatch(infos []DeviceOfflineNotificationInfo) error {
	session, collection := this.getDeviceOfflineNotificationInfoCollection()
	defer session.Close()
	bulk := collection.Bulk()
	for _, info := range infos {
		bulk.Upsert(bson.M{"device_id": info.DeviceId}, info)
	}
	_, err := bulk.Run()
	return err
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
