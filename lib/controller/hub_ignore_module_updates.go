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

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
	"go.mongodb.org/mongo-driver/mongo"
)

// hubIgnoreModuleUpdatesAttributeKey is the hub attribute that switches off the outdated-module metric and
// notification for a hub, with the value "true".
const hubIgnoreModuleUpdatesAttributeKey = "ignore-module-updates"

func ignoresModuleUpdates(attributes []models.Attribute) bool {
	return hasTrueAttribute(attributes, hubIgnoreModuleUpdatesAttributeKey)
}

func (this *Controller) getHubIgnoreModuleUpdatesCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.HubIgnoreModuleUpdatesCollection)
}

func (this *Controller) updateHubIgnoreModuleUpdates(commands []model.HubCommand) error {
	return updateHubFlag(this.getHubIgnoreModuleUpdatesCollection(), commands, ignoresModuleUpdates)
}

// getHubIgnoreModuleUpdatesIds loads the ids of all hubs that ignore module updates within ctx.
func (this *Controller) getHubIgnoreModuleUpdatesIds(ctx context.Context) (map[string]bool, error) {
	return getHubFlagIds(ctx, this.getHubIgnoreModuleUpdatesCollection())
}
