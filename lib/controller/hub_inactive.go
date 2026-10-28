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
	"strings"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// hubInactiveAttributeKey is the hub attribute that marks a hub as inactive, with the value "true".
const hubInactiveAttributeKey = "inactive"

// HubInactive marks one hub as inactive. The flag comes from the hub attributes on the hubs topic;
// the metrics read it from here instead of asking the device-repository on every scrape.
type HubInactive struct {
	HubId string `json:"hub_id" bson:"hub_id"`
}

func isInactive(attributes []models.Attribute) bool {
	for _, attribute := range attributes {
		if attribute.Key == hubInactiveAttributeKey && strings.EqualFold(strings.TrimSpace(attribute.Value), "true") {
			return true
		}
	}
	return false
}

func (this *Controller) getHubInactiveCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.HubInactiveCollection)
}

// updateHubInactive applies the last PUT or DELETE per hub: inactive PUTs store the flag, everything else removes it.
func (this *Controller) updateHubInactive(commands []model.HubCommand) error {
	last := map[string]model.HubCommand{}
	var order []string
	for _, command := range commands {
		if command.Id == "" || (command.Command != "PUT" && command.Command != "DELETE") {
			continue
		}
		if _, seen := last[command.Id]; !seen {
			order = append(order, command.Id)
		}
		last[command.Id] = command
	}
	var writes []mongo.WriteModel
	for _, id := range order {
		command := last[id]
		if command.Command == "PUT" && isInactive(command.Hub.Attributes) {
			writes = append(writes, mongo.NewUpdateOneModel().
				SetFilter(bson.M{"hub_id": id}).
				SetUpdate(bson.M{"$set": HubInactive{HubId: id}}).
				SetUpsert(true))
		} else {
			writes = append(writes, mongo.NewDeleteManyModel().SetFilter(bson.M{"hub_id": id}))
		}
	}
	return bulkWrite(this.getHubInactiveCollection(), writes)
}

// getHubInactiveIds loads the ids of all inactive hubs within ctx.
func (this *Controller) getHubInactiveIds(ctx context.Context) (map[string]bool, error) {
	opts := options.Find().SetProjection(bson.M{"_id": 0, "hub_id": 1})
	cursor, err := this.getHubInactiveCollection().Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	var docs []HubInactive
	if err = cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(docs))
	for _, doc := range docs {
		result[doc.HubId] = true
	}
	return result, nil
}
