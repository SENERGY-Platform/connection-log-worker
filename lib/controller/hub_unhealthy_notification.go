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
	"context"
	"fmt"
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

// unhealthyNotificationMaxReasonChars bounds the reasons in the notification text.
const unhealthyNotificationMaxReasonChars = 1000

// HubUnhealthyNotificationInfo tracks one unhealthy period of a hub, started by the first stored
// report with state unhealthy and ended by a healthy one.
type HubUnhealthyNotificationInfo struct {
	HubId          string `json:"hub_id" bson:"hub_id"`
	UnhealthySince int64  `json:"unhealthy_since" bson:"unhealthy_since,truncate"`
	Notified       bool   `json:"notified" bson:"notified"`
}

// parseHubUnhealthyNotificationConfig reports the feature as disabled when the delay is empty or "-".
func parseHubUnhealthyNotificationConfig(conf config.Config) (enabled bool, delay time.Duration, interval time.Duration, err error) {
	return parseNotificationTimings("HubUnhealthyNotification", conf.HubUnhealthyNotificationDelay, conf.HubUnhealthyNotificationInterval)
}

// hubUnhealthyTrackingChanges decides from the stored reports which unhealthy periods end and which
// may start. Starting is insert-if-absent, so a running period keeps its start; undetermined changes nothing.
func hubUnhealthyTrackingChanges(stored []HubHealth) (removeIds []string, startInfos []HubUnhealthyNotificationInfo) {
	for _, health := range stored {
		switch health.State {
		case model.HubHealthHealthy:
			removeIds = append(removeIds, health.HubId)
		case model.HubHealthUnhealthy:
			startInfos = append(startInfos, HubUnhealthyNotificationInfo{
				HubId:          health.HubId,
				UnhealthySince: health.Received.Unix(),
				Notified:       false,
			})
		}
	}
	return removeIds, startInfos
}

// trackHubUnhealthyStates works on the stored reports instead of the incoming ones, so that an outdated
// report that was not stored cannot change the tracking.
func (this *Controller) trackHubUnhealthyStates(hubIds []string) error {
	if !this.hubUnhealthyEnabled {
		return nil
	}
	stored, err := this.getHubHealths(hubIds)
	if err != nil {
		return err
	}
	removeIds, startInfos := hubUnhealthyTrackingChanges(stored)
	err = this.removeHubUnhealthyNotificationInfos(removeIds)
	if err != nil {
		return err
	}
	var models []mongo.WriteModel
	for _, info := range startInfos {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"hub_id": info.HubId}).
			SetUpdate(bson.M{"$setOnInsert": info}).
			SetUpsert(true))
	}
	return bulkWrite(this.getHubUnhealthyNotificationInfoCollection(), models)
}

func (this *Controller) removeHubUnhealthyNotificationInfos(hubIds []string) error {
	if !this.hubUnhealthyEnabled || len(hubIds) == 0 {
		return nil
	}
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := this.getHubUnhealthyNotificationInfoCollection().DeleteMany(ctx, inFilter("hub_id", hubIds))
	return err
}

func (this *Controller) getHubUnhealthyNotificationInfoCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.HubUnhealthyNotificationInfoCollection)
}

func (this *Controller) notifyUnhealthyHubs(ctx context.Context) {
	infos, err := this.getDueHubUnhealthyNotificationInfos(time.Now().Add(-this.hubUnhealthyDelay))
	if err != nil {
		this.config.GetLogger().Error("unable to get due hub unhealthy notification infos", "error", err)
		return
	}
	for _, info := range infos {
		if ctx.Err() != nil {
			return
		}
		this.notifyUnhealthyHub(ctx, info)
	}
}

// notifyUnhealthyHub claims the unhealthy period before sending, like notifyOfflineHub. The stored report
// is read after the claim, because the hub may have recovered since the tracking was written.
func (this *Controller) notifyUnhealthyHub(ctx context.Context, info HubUnhealthyNotificationInfo) {
	logger := this.config.GetLogger()
	claimed, err := this.setHubUnhealthyNotified(info, true)
	if err != nil {
		logger.Error("unable to claim hub unhealthy notification", "hub-id", info.HubId, "error", err)
		return
	}
	if !claimed {
		return
	}
	health, exists, err := this.getHubHealth(info.HubId)
	if err != nil {
		logger.Error("unable to read hub health for unhealthy notification", "hub-id", info.HubId, "error", err)
		this.releaseHubUnhealthyClaim(info)
		return
	}
	if !exists || health.State != model.HubHealthUnhealthy {
		err = this.removeHubUnhealthyNotificationInfos([]string{info.HubId})
		if err != nil {
			logger.Error("unable to remove hub unhealthy notification info of recovered hub", "hub-id", info.HubId, "error", err)
		}
		return
	}
	hub, err, code := this.deviceRepo.ReadHub(info.HubId, devicerepo.InternalAdminToken, devicerepomodel.READ)
	if err != nil {
		if code == http.StatusNotFound {
			err = this.removeHubUnhealthyNotificationInfos([]string{info.HubId})
			if err != nil {
				logger.Error("unable to remove hub unhealthy notification info of unknown hub", "hub-id", info.HubId, "error", err)
			}
			return
		}
		logger.Error("unable to read hub for unhealthy notification", "hub-id", info.HubId, "error", err)
		this.releaseHubUnhealthyClaim(info)
		return
	}
	if hub.OwnerId == "" {
		logger.Debug("hub has no owner, skip unhealthy notification", "hub-id", info.HubId)
		return
	}
	err = this.postNotification(ctx, Notification{
		UserId:  hub.OwnerId,
		Title:   "Network Unhealthy",
		Message: unhealthyNotificationMessage(hub.Name, hub.Id, time.Unix(info.UnhealthySince, 0), health.Reasons),
		Topic:   "mgw",
	})
	if err != nil {
		logger.Error("unable to send hub unhealthy notification", "hub-id", info.HubId, "error", err)
		this.releaseHubUnhealthyClaim(info)
	}
}

// unhealthyNotificationMessage cuts the reasons at unhealthyNotificationMaxReasonChars characters.
func unhealthyNotificationMessage(name string, id string, since time.Time, reasons []string) string {
	message := fmt.Sprintf("network %v (%v) has been unhealthy since %v", name, id, since.UTC().Format(time.RFC3339))
	if len(reasons) == 0 {
		return message
	}
	joined := strings.Join(reasons, "; ")
	if runes := []rune(joined); len(runes) > unhealthyNotificationMaxReasonChars {
		joined = string(runes[:unhealthyNotificationMaxReasonChars]) + "..."
	}
	return message + ": " + joined
}

func (this *Controller) releaseHubUnhealthyClaim(info HubUnhealthyNotificationInfo) {
	_, err := this.setHubUnhealthyNotified(info, false)
	if err != nil {
		this.config.GetLogger().Error("unable to release hub unhealthy notification claim", "hub-id", info.HubId, "error", err)
	}
}

func (this *Controller) getDueHubUnhealthyNotificationInfos(unhealthySinceBefore time.Time) ([]HubUnhealthyNotificationInfo, error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getHubUnhealthyNotificationInfoCollection().Find(ctx, bson.M{
		"notified":        false,
		"unhealthy_since": bson.M{"$lte": unhealthySinceBefore.Unix()},
	})
	if err != nil {
		return nil, err
	}
	return decodeAll[HubUnhealthyNotificationInfo](ctx, cursor, this.config.GetLogger())
}

// setHubUnhealthyNotified flips the flag only if exactly this unhealthy period still exists in the
// opposite state; it reports whether it did.
func (this *Controller) setHubUnhealthyNotified(info HubUnhealthyNotificationInfo, notified bool) (changed bool, err error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	result, err := this.getHubUnhealthyNotificationInfoCollection().UpdateOne(ctx,
		bson.M{"hub_id": info.HubId, "unhealthy_since": info.UnhealthySince, "notified": !notified},
		bson.M{"$set": bson.M{"notified": notified}})
	if err != nil {
		return false, err
	}
	return result.ModifiedCount > 0, nil
}
