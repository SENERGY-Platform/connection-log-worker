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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// mongoDuplicateKeyCode is raised when a conditional upsert finds a newer document that blocks the insert.
const mongoDuplicateKeyCode = 11000

// HubHealth is the latest health report of one hub.
type HubHealth struct {
	HubId      string    `json:"hub_id" bson:"hub_id"`
	State      int       `json:"state" bson:"state"`
	Reasons    []string  `json:"reasons" bson:"reasons"`
	Report     bson.M    `json:"report" bson:"report"`
	ReportTime string    `json:"report_time" bson:"report_time"`
	Received   time.Time `json:"received" bson:"received"`
}

// LogHubHealth stores one health report.
func (this *Controller) LogHubHealth(message model.HubHealthMessage) error {
	return this.LogHubHealths([]model.HubHealthMessage{message})
}

// LogHubHealths stores a batch of health reports; only the newest report per hub is written.
func (this *Controller) LogHubHealths(messages []model.HubHealthMessage) error {
	newest := newestHubHealthPerHub(messages)
	if len(newest) == 0 {
		return nil
	}
	var models []mongo.WriteModel
	ids := make([]string, 0, len(newest))
	for _, message := range newest {
		doc, err := toHubHealth(message)
		if err != nil {
			// a report that cannot be stored is dropped like any other malformed message
			this.config.GetLogger().Warn("skip hub health message with unusable report", "hub-id", message.HubId, "error", err)
			continue
		}
		ids = append(ids, doc.HubId)
		// the filter keeps an older report from replacing a newer one; the insert it then attempts hits the unique index
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(bson.M{"hub_id": doc.HubId, "received": bson.M{"$lte": doc.Received}}).
			SetReplacement(doc).
			SetUpsert(true))
	}
	if len(models) == 0 {
		return nil
	}
	err := this.bulkWriteSkippingRejected(models, ids)
	if err != nil {
		return err
	}
	return this.trackHubUnhealthyStates(ids)
}

// bulkWriteSkippingRejected tolerates per-document write errors: duplicate keys are the expected outcome of an
// outdated report, and any other rejected report is logged and dropped, since returning it would end in the
// consumer's fatal retry path and let one gateway stop the worker. ids holds the hub id of each model.
func (this *Controller) bulkWriteSkippingRejected(models []mongo.WriteModel, ids []string) error {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := this.getHubHealthCollection().BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if err == nil {
		return nil
	}
	var bulkErr mongo.BulkWriteException
	if errors.As(err, &bulkErr) && bulkErr.WriteConcernError == nil && len(bulkErr.WriteErrors) > 0 {
		for _, writeErr := range bulkErr.WriteErrors {
			if writeErr.Code == mongoDuplicateKeyCode {
				continue
			}
			hubId := ""
			if writeErr.Index >= 0 && writeErr.Index < len(ids) {
				hubId = ids[writeErr.Index]
			}
			this.config.GetLogger().Warn("skip hub health report rejected by mongodb", "hub-id", hubId, "code", writeErr.Code, "error", writeErr.Message)
		}
		return nil
	}
	return err
}

// newestHubHealthPerHub keeps the report with the latest connector time per hub; on equal times the later one wins.
// The result is in order of first appearance.
func newestHubHealthPerHub(messages []model.HubHealthMessage) []model.HubHealthMessage {
	index := map[string]int{}
	var result []model.HubHealthMessage
	for _, message := range messages {
		i, seen := index[message.HubId]
		if !seen {
			index[message.HubId] = len(result)
			result = append(result, message)
		} else if !message.Time.Before(result[i].Time) {
			result[i] = message
		}
	}
	return result
}

func toHubHealth(message model.HubHealthMessage) (HubHealth, error) {
	report, err := jsonObjectToBson(message.Report)
	if err != nil {
		return HubHealth{}, err
	}
	reasons := message.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	return HubHealth{
		HubId:      message.HubId,
		State:      message.State,
		Reasons:    reasons,
		Report:     report,
		ReportTime: message.ReportTime,
		Received:   message.Time.UTC(),
	}, nil
}

// jsonObjectToBson converts without interpreting extended JSON markers and keeps integers integral.
func jsonObjectToBson(raw []byte) (bson.M, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("report is not valid json")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("report has trailing data")
	}
	if err := checkJsonValue(value, 0); err != nil {
		return nil, err
	}
	converted, ok := convertJsonValue(value).(bson.M)
	if !ok {
		return nil, errors.New("report is not an object")
	}
	return converted, nil
}

// maxReportDepth stays well below the nesting limit of 100 that MongoDB enforces for documents.
const maxReportDepth = 50

// checkJsonValue rejects what BSON cannot encode client side (NUL in keys) and what MongoDB would reject for depth.
func checkJsonValue(value any, depth int) error {
	if depth > maxReportDepth {
		return errors.New("report is nested too deeply")
	}
	switch v := value.(type) {
	case map[string]any:
		for key, element := range v {
			if strings.ContainsRune(key, 0) {
				return errors.New("report has a key containing NUL")
			}
			if err := checkJsonValue(element, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, element := range v {
			if err := checkJsonValue(element, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func convertJsonValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		result := make(bson.M, len(v))
		for key, element := range v {
			result[key] = convertJsonValue(element)
		}
		return result
	case []any:
		result := make(bson.A, len(v))
		for i, element := range v {
			result[i] = convertJsonValue(element)
		}
		return result
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String()
	default:
		return v
	}
}

func (this *Controller) getHubHealthCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.HubHealthCollection)
}

func (this *Controller) getHubHealths(ids []string) ([]HubHealth, error) {
	ctx, cancel := mongoOperationContext()
	defer cancel()
	cursor, err := this.getHubHealthCollection().Find(ctx, inFilter("hub_id", ids))
	if err != nil {
		return nil, err
	}
	return decodeAll[HubHealth](ctx, cursor, this.config.GetLogger())
}

// getHubHealth reports exists=false when the hub has no stored report.
func (this *Controller) getHubHealth(id string) (health HubHealth, exists bool, err error) {
	healths, err := this.getHubHealths([]string{id})
	if err != nil {
		return health, false, err
	}
	if len(healths) == 0 {
		return health, false, nil
	}
	return healths[0], true, nil
}

// removeHubHealthData deletes the stored report and the unhealthy tracking of deleted hubs.
func (this *Controller) removeHubHealthData(hubIds []string) error {
	if len(hubIds) == 0 {
		return nil
	}
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := this.getHubHealthCollection().DeleteMany(ctx, inFilter("hub_id", hubIds))
	if err != nil {
		return fmt.Errorf("unable to delete hub health: %w", err)
	}
	return this.removeHubUnhealthyNotificationInfos(hubIds)
}
