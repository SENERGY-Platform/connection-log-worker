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
package test

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/controller"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	"github.com/SENERGY-Platform/models/go/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestHubInactiveCommands(t *testing.T) {
	wg := &sync.WaitGroup{}
	defer wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conf, err := config.Load("../config.json")
	if err != nil {
		t.Fatal(err)
	}
	conf.HubOfflineNotificationDelay = "-"
	conf.HubUnhealthyNotificationDelay = "-"
	// DELETE also removes the hub's connection log from InfluxDB
	influxPort, _, err := server.Influxdb(ctx, wg)
	if err != nil {
		t.Fatal(err)
	}
	conf.InfluxdbUrl = "http://localhost:" + influxPort
	conf.InfluxdbDb = "connectionlog"
	conf.InfluxdbUser = "user"
	conf.InfluxdbPw = "pw"
	conf.InfluxdbTimeout = 3
	mongoPort, _, err := server.MongoDB(ctx, wg)
	if err != nil {
		t.Fatal(err)
	}
	conf.MongoUrl = "mongodb://localhost:" + mongoPort

	control, err := controller.New(ctx, conf)
	if err != nil {
		t.Fatal(err)
	}
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(conf.MongoUrl))
	if err != nil {
		t.Fatal(err)
	}
	defer mongoClient.Disconnect(context.Background())
	inactive := mongoClient.Database(conf.MongoDatabase).Collection(conf.HubInactiveCollection)

	storedIds := func(t *testing.T) []string {
		t.Helper()
		cursor, err := inactive.Find(ctx, bson.M{})
		if err != nil {
			t.Fatal(err)
		}
		var docs []struct {
			HubId string `bson:"hub_id"`
		}
		if err = cursor.All(ctx, &docs); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, doc := range docs {
			ids = append(ids, doc.HubId)
		}
		sort.Strings(ids)
		return ids
	}
	expectIds := func(t *testing.T, expected ...string) {
		t.Helper()
		sort.Strings(expected)
		got := storedIds(t)
		if len(got) != len(expected) {
			t.Fatalf("expected inactive hubs %v, got %v", expected, got)
		}
		for i := range got {
			if got[i] != expected[i] {
				t.Fatalf("expected inactive hubs %v, got %v", expected, got)
			}
		}
	}
	reset := func(t *testing.T) {
		t.Helper()
		if _, err := inactive.DeleteMany(ctx, bson.M{}); err != nil {
			t.Fatal(err)
		}
	}
	put := func(id string, attributes ...models.Attribute) model.HubCommand {
		return model.HubCommand{Command: "PUT", Id: id, Hub: models.Hub{Id: id, Name: "n", Attributes: attributes}}
	}
	flag := models.Attribute{Key: "inactive", Value: "true", Origin: "web-ui"}
	del := func(id string) model.HubCommand { return model.HubCommand{Command: "DELETE", Id: id} }

	t.Run("single mode", func(t *testing.T) {
		reset(t)
		if err := control.UpdateHub(put("h1", flag)); err != nil {
			t.Fatal(err)
		}
		expectIds(t, "h1")

		// storing again must not add a second document
		if err := control.UpdateHub(put("h1", models.Attribute{Key: "x", Value: "y"}, flag)); err != nil {
			t.Fatal(err)
		}
		expectIds(t, "h1")

		if err := control.UpdateHub(put("h2", flag)); err != nil {
			t.Fatal(err)
		}
		if err := control.UpdateHub(put("h1")); err != nil {
			t.Fatal(err)
		}
		expectIds(t, "h2")

		if err := control.UpdateHub(put("h2", models.Attribute{Key: "inactive", Value: "false"})); err != nil {
			t.Fatal(err)
		}
		expectIds(t)

		if err := control.UpdateHub(put("h3", flag)); err != nil {
			t.Fatal(err)
		}
		if err := control.UpdateHub(del("h3")); err != nil {
			t.Fatal(err)
		}
		expectIds(t)
	})

	t.Run("batch mode, the last command per hub wins", func(t *testing.T) {
		reset(t)
		err := control.UpdateHubs([]model.HubCommand{
			put("inactive-last", models.Attribute{Key: "other", Value: "x"}),
			put("inactive-last", flag),
			put("active-last", flag),
			put("active-last"),
			put("deleted-last", flag),
			del("deleted-last"),
			put("stays", flag),
			put("other-hub"),
		})
		if err != nil {
			t.Fatal(err)
		}
		expectIds(t, "inactive-last", "stays")

		err = control.UpdateHubs([]model.HubCommand{
			del("stays"),
			put("stays", flag),
			put("inactive-last"),
			del("never-existed"),
		})
		if err != nil {
			t.Fatal(err)
		}
		expectIds(t, "stays")
	})

	t.Run("batch without commands changes nothing", func(t *testing.T) {
		reset(t)
		if err := control.UpdateHub(put("h1", flag)); err != nil {
			t.Fatal(err)
		}
		if err := control.UpdateHubs(nil); err != nil {
			t.Fatal(err)
		}
		expectIds(t, "h1")
	})
}
