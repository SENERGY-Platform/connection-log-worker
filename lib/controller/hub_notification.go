/*
 * Copyright 2026 InfAI (CC SES)
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

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	devicerepo "github.com/SENERGY-Platform/device-repository/v2/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v2/lib/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// HubOfflineNotificationInfo tracks one offline period of a hub. A hub log only carries id, state
// and time, so the worker records the period itself and a timer loop notifies the owner once.
type HubOfflineNotificationInfo struct {
	HubId        string `json:"hub_id" bson:"hub_id"`
	OfflineSince int64  `json:"offline_since" bson:"offline_since,truncate"`
	Notified     bool   `json:"notified" bson:"notified"`
}

// parseHubOfflineNotificationConfig reports the feature as disabled when the delay is empty or "-".
func parseHubOfflineNotificationConfig(conf config.Config) (enabled bool, delay time.Duration, interval time.Duration, err error) {
	if conf.HubOfflineNotificationDelay == "" || conf.HubOfflineNotificationDelay == "-" {
		return false, 0, 0, nil
	}
	delay, err = time.ParseDuration(conf.HubOfflineNotificationDelay)
	if err != nil {
		return false, 0, 0, fmt.Errorf("invalid HubOfflineNotificationDelay: %w", err)
	}
	if delay < 0 {
		return false, 0, 0, fmt.Errorf("invalid HubOfflineNotificationDelay: %v must not be negative", delay)
	}
	interval, err = time.ParseDuration(conf.HubOfflineNotificationInterval)
	if err != nil {
		return false, 0, 0, fmt.Errorf("invalid HubOfflineNotificationInterval: %w", err)
	}
	if interval <= 0 {
		return false, 0, 0, fmt.Errorf("invalid HubOfflineNotificationInterval: %v must be positive", interval)
	}
	return true, delay, interval, nil
}

// hubOfflineTrackingChanges decides from the hub states that actually changed which offline periods
// end and which start. Hubs that were already offline before are not in newStates and never notify.
func hubOfflineTrackingChanges(newStates []model.HubLog) (removeIds []string, newInfos []HubOfflineNotificationInfo) {
	for _, state := range newStates {
		if state.Connected {
			removeIds = append(removeIds, state.Id)
		} else {
			newInfos = append(newInfos, HubOfflineNotificationInfo{
				HubId:        state.Id,
				OfflineSince: state.Time.Unix(),
				Notified:     false,
			})
		}
	}
	return removeIds, newInfos
}

// trackHubOfflineStates must only be called with states that were persisted as real transitions.
func (this *Controller) trackHubOfflineStates(newStates []model.HubLog) error {
	if !this.hubOfflineEnabled {
		return nil
	}
	removeIds, newInfos := hubOfflineTrackingChanges(newStates)
	err := this.removeHubOfflineNotificationInfos(removeIds)
	if err != nil {
		return err
	}
	var models []mongo.WriteModel
	for _, info := range newInfos {
		models = append(models, upsertModel(bson.M{"hub_id": info.HubId}, info))
	}
	return bulkWrite(this.getHubOfflineNotificationInfoCollection(), models)
}

func (this *Controller) removeHubOfflineNotificationInfos(hubIds []string) error {
	if !this.hubOfflineEnabled || len(hubIds) == 0 {
		return nil
	}
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := this.getHubOfflineNotificationInfoCollection().DeleteMany(ctx, inFilter("hub_id", hubIds))
	return err
}

func (this *Controller) getHubOfflineNotificationInfoCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.HubOfflineNotificationInfoCollection)
}

// runHubOfflineNotifications never returns before ctx is done: a failing tick is logged and retried on the next one.
func (this *Controller) runHubOfflineNotifications(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			this.notifyOfflineHubsSafely(ctx)
		}
	}
}

func (this *Controller) notifyOfflineHubsSafely(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			this.config.GetLogger().Error("hub offline notification tick panicked", "error", fmt.Sprint(r))
		}
	}()
	this.notifyOfflineHubs(ctx)
}

func (this *Controller) notifyOfflineHubs(ctx context.Context) {
	infos, err := this.getDueHubOfflineNotificationInfos(time.Now().Add(-this.hubOfflineDelay))
	if err != nil {
		this.config.GetLogger().Error("unable to get due hub offline notification infos", "error", err)
		return
	}
	for _, info := range infos {
		if ctx.Err() != nil {
			return
		}
		this.notifyOfflineHub(ctx, info)
	}
}

// notifyOfflineHub claims the offline period before sending, so that concurrent workers and
// later ticks send at most one notification; a failed send releases the claim for a retry.
func (this *Controller) notifyOfflineHub(ctx context.Context, info HubOfflineNotificationInfo) {
	logger := this.config.GetLogger()
	claimed, err := this.setHubOfflineNotified(info, true)
	if err != nil {
		logger.Error("unable to claim hub offline notification", "hub-id", info.HubId, "error", err)
		return
	}
	if !claimed {
		return
	}
	hub, err, code := this.deviceRepo.ReadHub(info.HubId, devicerepo.InternalAdminToken, devicerepomodel.READ)
	if err != nil {
		if code == http.StatusNotFound {
			err = this.removeHubOfflineNotificationInfos([]string{info.HubId})
			if err != nil {
				logger.Error("unable to remove hub offline notification info of unknown hub", "hub-id", info.HubId, "error", err)
			}
			return
		}
		logger.Error("unable to read hub for offline notification", "hub-id", info.HubId, "error", err)
		this.releaseHubOfflineClaim(info)
		return
	}
	if hub.OwnerId == "" {
		logger.Debug("hub has no owner, skip offline notification", "hub-id", info.HubId)
		return
	}
	since := time.Since(time.Unix(info.OfflineSince, 0))
	err = this.postNotification(ctx, Notification{
		UserId:  hub.OwnerId,
		Title:   "Network Offline",
		Message: fmt.Sprintf("network %v (%v) has been offline for %v", hub.Name, hub.Id, since.Round(this.roundTime).String()),
		Topic:   "mgw",
	})
	if err != nil {
		logger.Error("unable to send hub offline notification", "hub-id", info.HubId, "error", err)
		this.releaseHubOfflineClaim(info)
	}
}

func (this *Controller) releaseHubOfflineClaim(info HubOfflineNotificationInfo) {
	_, err := this.setHubOfflineNotified(info, false)
	if err != nil {
		this.config.GetLogger().Error("unable to release hub offline notification claim", "hub-id", info.HubId, "error", err)
	}
}

func (this *Controller) getDueHubOfflineNotificationInfos(offlineSinceBefore time.Time) ([]HubOfflineNotificationInfo, error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getHubOfflineNotificationInfoCollection().Find(ctx, bson.M{
		"notified":      false,
		"offline_since": bson.M{"$lte": offlineSinceBefore.Unix()},
	})
	if err != nil {
		return nil, err
	}
	return decodeAll[HubOfflineNotificationInfo](ctx, cursor, this.config.GetLogger())
}

// setHubOfflineNotified flips the flag only if exactly this offline period still exists in the
// opposite state; it reports whether it did.
func (this *Controller) setHubOfflineNotified(info HubOfflineNotificationInfo, notified bool) (changed bool, err error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	result, err := this.getHubOfflineNotificationInfoCollection().UpdateOne(ctx,
		bson.M{"hub_id": info.HubId, "offline_since": info.OfflineSince, "notified": !notified},
		bson.M{"$set": bson.M{"notified": notified}})
	if err != nil {
		return false, err
	}
	return result.ModifiedCount > 0, nil
}

func (this *Controller) postNotification(ctx context.Context, notification Notification) error {
	body, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(this.config.NotificationUrl, "/")+"/notifications", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respMsg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected response status from notifier %v %v", resp.Status, string(respMsg))
	}
	return nil
}
