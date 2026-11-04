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
	"slices"
	"strings"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	devicerepo "github.com/SENERGY-Platform/device-repository/v2/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v2/lib/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// defaultHubModuleOutdatedAge applies when HubModuleOutdatedAge is empty.
const defaultHubModuleOutdatedAge = 7 * 24 * time.Hour

// outdatedModulesNotificationMaxChars bounds the module list in the notification text.
const outdatedModulesNotificationMaxChars = 1000

const outdatedModulesNotificationTitle = "Network Modules Outdated"

// HubModuleUpdateInfo tracks one pending module update of a hub, started by the first stored report that
// names a next version and ended by a stored report that does not, or that no longer lists the module.
// A newer next version during the pending period changes neither PendingSince nor Notified, so the owner
// hears about a module once per period.
type HubModuleUpdateInfo struct {
	HubId        string `json:"hub_id" bson:"hub_id"`
	ModuleId     string `json:"module_id" bson:"module_id"`
	Name         string `json:"name" bson:"name"`
	Version      string `json:"version" bson:"version"`
	NextVersion  string `json:"next_version" bson:"next_version"`
	PendingSince int64  `json:"pending_since" bson:"pending_since,truncate"`
	Notified     bool   `json:"notified" bson:"notified"`
}

// parseHubModuleOutdatedConfig validates the age (empty selects the default) and the interval; an empty
// interval or "-" disables the notification and leaves the tracking and the metric running.
func parseHubModuleOutdatedConfig(conf config.Config) (age time.Duration, enabled bool, interval time.Duration, err error) {
	age = defaultHubModuleOutdatedAge
	if conf.HubModuleOutdatedAge != "" {
		age, err = time.ParseDuration(conf.HubModuleOutdatedAge)
		if err != nil {
			return 0, false, 0, fmt.Errorf("invalid HubModuleOutdatedAge: %w", err)
		}
		if age <= 0 {
			return 0, false, 0, fmt.Errorf("invalid HubModuleOutdatedAge: %v must be positive", age)
		}
	}
	if conf.HubModuleOutdatedNotificationInterval == "" || conf.HubModuleOutdatedNotificationInterval == "-" {
		return age, false, 0, nil
	}
	interval, err = time.ParseDuration(conf.HubModuleOutdatedNotificationInterval)
	if err != nil {
		return 0, false, 0, fmt.Errorf("invalid HubModuleOutdatedNotificationInterval: %w", err)
	}
	if interval <= 0 {
		return 0, false, 0, fmt.Errorf("invalid HubModuleOutdatedNotificationInterval: %v must be positive", interval)
	}
	return age, true, interval, nil
}

// hubModuleUpdateChanges decides from the module list of an accepted report which modules have a pending
// update; the report's receive time becomes pending_since if the module is not tracked yet. Every tracked
// module of the hub that is not in the result is removed: it has no next version or is no longer listed.
// Entries without id are skipped and a repeated id counts once. An unknown list is the caller's case: it
// changes nothing and must not be passed here.
func hubModuleUpdateChanges(hubId string, received time.Time, modules []model.ModuleVersion) (pending []HubModuleUpdateInfo) {
	seen := map[string]bool{}
	for _, module := range modules {
		if module.Id == "" || seen[module.Id] {
			continue
		}
		seen[module.Id] = true
		if module.NextVersion == "" {
			continue
		}
		pending = append(pending, HubModuleUpdateInfo{
			HubId:        hubId,
			ModuleId:     module.Id,
			Name:         module.Name,
			Version:      module.Version,
			NextVersion:  module.NextVersion,
			PendingSince: received.Unix(),
			Notified:     false,
		})
	}
	return pending
}

// trackHubModuleUpdates must only be called with reports the health store accepted: an older report that
// the received filter rejected would otherwise reset the tracking to an outdated module list.
func (this *Controller) trackHubModuleUpdates(accepted []model.HubHealthMessage) error {
	for _, message := range accepted {
		if !message.ModuleVersionsKnown {
			continue
		}
		pending := hubModuleUpdateChanges(message.HubId, message.Time.UTC(), message.ModuleVersions)
		pendingIds := make([]string, 0, len(pending))
		models := make([]mongo.WriteModel, 0, len(pending)+1)
		for _, info := range pending {
			pendingIds = append(pendingIds, info.ModuleId)
			models = append(models, mongo.NewUpdateOneModel().
				SetFilter(bson.M{"hub_id": info.HubId, "module_id": info.ModuleId}).
				SetUpdate(bson.M{
					"$setOnInsert": bson.M{"pending_since": info.PendingSince, "notified": false},
					"$set":         bson.M{"name": info.Name, "version": info.Version, "next_version": info.NextVersion},
				}).
				SetUpsert(true))
		}
		models = append(models, mongo.NewDeleteManyModel().
			SetFilter(bson.M{"hub_id": message.HubId, "module_id": bson.M{"$nin": pendingIds}}))
		err := bulkWrite(this.getHubModuleUpdateInfoCollection(), models)
		if err != nil {
			return err
		}
	}
	return nil
}

func (this *Controller) getHubModuleUpdateInfoCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.HubModuleUpdateInfoCollection)
}

func (this *Controller) removeHubModuleUpdateInfos(hubIds []string) error {
	if len(hubIds) == 0 {
		return nil
	}
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := this.getHubModuleUpdateInfoCollection().DeleteMany(ctx, inFilter("hub_id", hubIds))
	return err
}

func (this *Controller) notifyOutdatedModuleHubs(ctx context.Context) {
	infos, err := this.getDueHubModuleUpdateInfos(time.Now().Add(-this.hubModuleOutdatedAge))
	if err != nil {
		this.config.GetLogger().Error("unable to get due hub module update infos", "error", err)
		return
	}
	var hubIds []string
	byHub := map[string][]HubModuleUpdateInfo{}
	for _, info := range infos {
		if _, seen := byHub[info.HubId]; !seen {
			hubIds = append(hubIds, info.HubId)
		}
		byHub[info.HubId] = append(byHub[info.HubId], info)
	}
	for _, hubId := range hubIds {
		if ctx.Err() != nil {
			return
		}
		this.notifyOutdatedModuleHub(ctx, hubId, byHub[hubId])
	}
}

// notifyOutdatedModuleHub sends one notification for all due modules of the hub. Each module is claimed
// by flipping notified on exactly the document that was selected, so with several workers only one claims
// a module; the claim is released for exactly the documents claimed. After the claim the documents are read
// again and modules whose document is gone or replaced by a new pending period are dropped.
func (this *Controller) notifyOutdatedModuleHub(ctx context.Context, hubId string, due []HubModuleUpdateInfo) {
	logger := this.config.GetLogger()
	claimed, err := this.claimHubModuleUpdateInfos(due)
	if err != nil {
		logger.Error("unable to claim hub module update notification", "hub-id", hubId, "error", err)
		this.releaseHubModuleUpdateClaim(claimed)
		return
	}
	if len(claimed) == 0 {
		return
	}
	current, err := this.getClaimedHubModuleUpdateInfos(hubId, claimed)
	if err != nil {
		logger.Error("unable to read hub module update infos for notification", "hub-id", hubId, "error", err)
		this.releaseHubModuleUpdateClaim(claimed)
		return
	}
	if len(current) == 0 {
		return
	}
	hub, err, code := this.deviceRepo.ReadHub(hubId, devicerepo.InternalAdminToken, devicerepomodel.READ)
	if err != nil {
		if code == http.StatusNotFound {
			err = this.removeHubModuleUpdateInfos([]string{hubId})
			if err != nil {
				logger.Error("unable to remove hub module update infos of unknown hub", "hub-id", hubId, "error", err)
			}
			return
		}
		logger.Error("unable to read hub for module update notification", "hub-id", hubId, "error", err)
		this.releaseHubModuleUpdateClaim(current)
		return
	}
	if isInactive(hub.Attributes) {
		// the claim stays, so the pending period counts as notified and is not retried
		logger.Debug("hub is inactive, skip module update notification", "hub-id", hubId)
		return
	}
	if hub.OwnerId == "" {
		logger.Debug("hub has no owner, skip module update notification", "hub-id", hubId)
		return
	}
	err = this.postNotification(ctx, Notification{
		UserId:  hub.OwnerId,
		Title:   outdatedModulesNotificationTitle,
		Message: outdatedModulesNotificationMessage(hub.Name, hub.Id, current),
		Topic:   "mgw",
	})
	if err != nil {
		logger.Error("unable to send hub module update notification", "hub-id", hubId, "error", err)
		this.releaseHubModuleUpdateClaim(current)
	}
}

// outdatedModulesNotificationMessage lists the modules ordered by module id, with the oldest pending_since,
// and cuts the module list at outdatedModulesNotificationMaxChars characters.
func outdatedModulesNotificationMessage(hubName string, hubId string, infos []HubModuleUpdateInfo) string {
	infos = slices.Clone(infos)
	slices.SortFunc(infos, func(a, b HubModuleUpdateInfo) int { return strings.Compare(a.ModuleId, b.ModuleId) })
	oldest := int64(0)
	entries := make([]string, 0, len(infos))
	for i, info := range infos {
		if i == 0 || info.PendingSince < oldest {
			oldest = info.PendingSince
		}
		name := info.Name
		if name == "" {
			name = info.ModuleId
		}
		entries = append(entries, fmt.Sprintf("%v %v -> %v", name, info.Version, info.NextVersion))
	}
	joined := strings.Join(entries, "; ")
	if runes := []rune(joined); len(runes) > outdatedModulesNotificationMaxChars {
		joined = string(runes[:outdatedModulesNotificationMaxChars]) + "..."
	}
	return fmt.Sprintf("network %v (%v) has module updates pending since %v: %v",
		hubName, hubId, time.Unix(oldest, 0).UTC().Format(time.RFC3339), joined)
}

func (this *Controller) getDueHubModuleUpdateInfos(pendingSinceBefore time.Time) ([]HubModuleUpdateInfo, error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getHubModuleUpdateInfoCollection().Find(ctx, bson.M{
		"notified":      false,
		"pending_since": bson.M{"$lte": pendingSinceBefore.Unix()},
	})
	if err != nil {
		return nil, err
	}
	return decodeAll[HubModuleUpdateInfo](ctx, cursor, this.config.GetLogger())
}

// claimHubModuleUpdateInfos returns the documents this worker flipped; on an error those flipped so far are
// returned too, so that the caller can release them.
func (this *Controller) claimHubModuleUpdateInfos(infos []HubModuleUpdateInfo) (claimed []HubModuleUpdateInfo, err error) {
	for _, info := range infos {
		changed, err := this.setHubModuleUpdateNotified(info, true)
		if err != nil {
			return claimed, err
		}
		if changed {
			claimed = append(claimed, info)
		}
	}
	return claimed, nil
}

func (this *Controller) releaseHubModuleUpdateClaim(infos []HubModuleUpdateInfo) {
	for _, info := range infos {
		_, err := this.setHubModuleUpdateNotified(info, false)
		if err != nil {
			this.config.GetLogger().Error("unable to release hub module update notification claim", "hub-id", info.HubId, "module-id", info.ModuleId, "error", err)
		}
	}
}

// getClaimedHubModuleUpdateInfos returns the current content of the claimed documents; a document that was
// removed or recreated with another pending_since meanwhile is not part of the result.
func (this *Controller) getClaimedHubModuleUpdateInfos(hubId string, claimed []HubModuleUpdateInfo) ([]HubModuleUpdateInfo, error) {
	moduleIds := make([]string, 0, len(claimed))
	since := map[string]int64{}
	for _, info := range claimed {
		moduleIds = append(moduleIds, info.ModuleId)
		since[info.ModuleId] = info.PendingSince
	}
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getHubModuleUpdateInfoCollection().Find(ctx, bson.M{
		"hub_id":    hubId,
		"module_id": bson.M{"$in": moduleIds},
		"notified":  true,
	})
	if err != nil {
		return nil, err
	}
	all, err := decodeAll[HubModuleUpdateInfo](ctx, cursor, this.config.GetLogger())
	if err != nil {
		return nil, err
	}
	var result []HubModuleUpdateInfo
	for _, info := range all {
		if s, ok := since[info.ModuleId]; ok && s == info.PendingSince {
			result = append(result, info)
		}
	}
	return result, nil
}

// setHubModuleUpdateNotified flips the flag only if exactly this pending period of the module still exists
// in the opposite state; it reports whether it did.
func (this *Controller) setHubModuleUpdateNotified(info HubModuleUpdateInfo, notified bool) (changed bool, err error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	result, err := this.getHubModuleUpdateInfoCollection().UpdateOne(ctx,
		bson.M{"hub_id": info.HubId, "module_id": info.ModuleId, "pending_since": info.PendingSince, "notified": !notified},
		bson.M{"$set": bson.M{"notified": notified}})
	if err != nil {
		return false, err
	}
	return result.ModifiedCount > 0, nil
}
