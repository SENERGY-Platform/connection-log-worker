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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib"
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/consumer"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/util"
	"github.com/SENERGY-Platform/connection-log-worker/test/helper"
	"github.com/segmentio/kafka-go"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestHubHealth(t *testing.T) {
	t.Run("batch mode", func(t *testing.T) { testHubHealth(t, true) })
	t.Run("single mode", func(t *testing.T) { testHubHealth(t, false) })
}

func testHubHealth(t *testing.T, batchMode bool) {
	wg := &sync.WaitGroup{}
	defer wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defaultConfig, err := config.Load("../config.json")
	if err != nil {
		t.Fatal(err)
	}
	defaultConfig.Debug = true
	defaultConfig.RoundTime = "1s"
	defaultConfig.InitTopics = true
	defaultConfig.KafkaMessageWindow = 1
	defaultConfig.HubHealthTopic = "gateway_health"
	defaultConfig.HubOfflineNotificationDelay = "-"
	defaultConfig.HubUnhealthyNotificationDelay = hubOfflineTestDelay.String()
	defaultConfig.HubUnhealthyNotificationInterval = "500ms"

	conf, err := startHubNotificationDependencies(ctx, wg, defaultConfig)
	if err != nil {
		t.Fatal(err)
	}

	notifier := &fakeNotifier{}
	notifierServer := httptest.NewServer(notifier)
	defer notifierServer.Close()
	conf.NotificationUrl = notifierServer.URL

	repoServer := httptest.NewServer(http.HandlerFunc(fakeHubRepository))
	defer repoServer.Close()
	conf.DeviceRepositoryUrl = repoServer.URL

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(conf.MongoUrl))
	if err != nil {
		t.Fatal(err)
	}
	defer mongoClient.Disconnect(context.Background())
	db := mongoClient.Database(conf.MongoDatabase)

	if batchMode {
		err = lib.StartBatch(ctx, conf, func(err error, consumer *consumer.BatchConsumer) { t.Error(err) })
	} else {
		err = lib.Start(ctx, conf, func(err error, consumer *consumer.Consumer) { t.Error(err) })
	}
	if err != nil {
		t.Fatal(err)
	}

	broker, err := util.GetBroker(conf.KafkaUrl)
	if err != nil {
		t.Fatal(err)
	}
	healthProducer, err := helper.GetProducer(broker, conf.HubHealthTopic, true)
	if err != nil {
		t.Fatal(err)
	}
	defer healthProducer.Close()
	hubProducer, err := helper.GetProducer(broker, conf.HubTopic, true)
	if err != nil {
		t.Fatal(err)
	}
	defer hubProducer.Close()

	// the consumers start at the latest offset, so give them time to be assigned
	time.Sleep(3 * time.Second)

	healths := db.Collection(conf.HubHealthCollection)
	infos := db.Collection(conf.HubUnhealthyNotificationInfoCollection)
	hasDoc := func(c *mongo.Collection, id string) bool {
		n, err := c.CountDocuments(ctx, bson.M{"hub_id": id})
		if err != nil {
			t.Error(err)
		}
		return n > 0
	}
	storedState := func(id string) (state int32, found bool) {
		var doc struct {
			State int32 `bson:"state"`
		}
		err := healths.FindOne(ctx, bson.M{"hub_id": id}).Decode(&doc)
		if err == mongo.ErrNoDocuments {
			return 0, false
		}
		if err != nil {
			t.Error(err)
		}
		return doc.State, true
	}
	sendRaw := func(id string, value []byte) {
		err := healthProducer.WriteMessages(ctx, kafka.Message{Key: []byte(id), Value: value})
		if err != nil {
			t.Error(err)
		}
	}
	sendReport := func(id string, at time.Time, state int, reasons ...string) {
		report, _ := json.Marshal(map[string]any{"version": 1, "time": at.Format(time.RFC3339), "state": state, "reasons": reasons, "modules": map[string]any{"github.com/x/y": map[string]any{"restarts": 4}}})
		b, err := json.Marshal(model.HubHealthMessage{HubId: id, UserId: "user", Time: at, Report: report})
		if err != nil {
			t.Error(err)
			return
		}
		sendRaw(id, b)
	}
	sendDelete := func(id string) {
		b, err := json.Marshal(model.HubCommand{Command: "DELETE", Id: id})
		if err != nil {
			t.Error(err)
			return
		}
		err = hubProducer.WriteMessages(ctx, kafka.Message{Key: []byte(id), Value: b})
		if err != nil {
			t.Error(err)
		}
	}
	waitPastDelay := func() { time.Sleep(hubOfflineTestDelay + 3*time.Second) }

	t.Run("scenarios", func(t *testing.T) {
		t.Run("stays unhealthy", func(t *testing.T) {
			t.Parallel()
			id := "hub-stays-unhealthy"
			sendReport(id, time.Now(), 2, "module a: unhealthy", "update failed")
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 1 })
			n := notifier.forHub(id)[0]
			if n.UserId != hubOfflineTestOwner || n.Topic != "mgw" || n.Title != "Network Unhealthy" ||
				!strings.Contains(n.Message, "network name of "+id+" ("+id+") has been unhealthy since ") ||
				!strings.HasSuffix(n.Message, ": module a: unhealthy; update failed") {
				t.Errorf("unexpected notification %#v", n)
			}
			var doc bson.M
			if err := healths.FindOne(ctx, bson.M{"hub_id": id}).Decode(&doc); err != nil {
				t.Fatal(err)
			}
			report, _ := doc["report"].(bson.M)
			modules, _ := report["modules"].(bson.M)
			if doc["state"] != int32(2) && doc["state"] != int64(2) || report == nil || modules == nil || doc["report_time"] == nil || doc["received"] == nil {
				t.Errorf("unexpected health document %#v", doc)
			}
			sendReport(id, time.Now(), 2, "module a: unhealthy")
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 1 {
				t.Errorf("expected exactly one notification, got %#v", got)
			}
		})

		t.Run("healthy before the delay", func(t *testing.T) {
			t.Parallel()
			id := "hub-healthy-again"
			sendReport(id, time.Now(), 2, "x")
			eventually(t, 10*time.Second, func() bool { return hasDoc(infos, id) })
			sendReport(id, time.Now(), 1)
			eventually(t, 10*time.Second, func() bool { return !hasDoc(infos, id) })
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
			if state, found := storedState(id); !found || state != 1 {
				t.Errorf("expected the healthy report to be stored, got state=%v found=%v", state, found)
			}
		})

		t.Run("older report does not overwrite a newer one", func(t *testing.T) {
			t.Parallel()
			id := "hub-out-of-order"
			now := time.Now()
			sendReport(id, now, 1)
			eventually(t, 10*time.Second, func() bool { state, found := storedState(id); return found && state == 1 })
			sendReport(id, now.Add(-time.Hour), 2, "old")
			time.Sleep(3 * time.Second)
			if state, _ := storedState(id); state != 1 {
				t.Errorf("expected the newer healthy report to stay, got state %v", state)
			}
			if hasDoc(infos, id) {
				t.Error("an outdated report must not start an unhealthy period")
			}
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
			n, err := healths.CountDocuments(ctx, bson.M{"hub_id": id})
			if err != nil || n != 1 {
				t.Errorf("expected exactly one health document, got %v %v", n, err)
			}
		})

		t.Run("malformed messages do not stop the consumer", func(t *testing.T) {
			t.Parallel()
			id := "hub-after-malformed"
			sendRaw("x", []byte("not json"))
			sendRaw("x", []byte(`{"hub_id":"`+id+`","time":"2026-10-27T14:00:00Z","report":{"state":9}}`))
			sendRaw("x", []byte(`{"time":"2026-10-27T14:00:00Z","report":{"state":1}}`))
			sendRaw("x", []byte(`{"hub_id":"`+id+`","time":"never","report":{"state":1}}`))
			sendRaw(id+"-nul", []byte(`{"hub_id":"`+id+`-nul","time":"2026-10-27T14:00:00Z","report":{"state":2,"a\u0000b":1}}`))
			sendRaw(id+"-deep", []byte(`{"hub_id":"`+id+`-deep","time":"2026-10-27T14:00:00Z","report":{"state":2,"x":`+strings.Repeat(`{"x":`, 120)+`1`+strings.Repeat(`}`, 120)+`}}`))
			sendRaw(id+"-dollar", []byte(`{"hub_id":"`+id+`-dollar","time":"2026-10-27T14:00:00Z","report":{"state":2,"core":{"$where":1,"a.b":{"$":1}}}}`))
			sendReport(id, time.Now(), 1)
			eventually(t, 30*time.Second, func() bool { state, found := storedState(id); return found && state == 1 })
		})

		t.Run("inactive hub", func(t *testing.T) {
			t.Parallel()
			id := "inactive-hub-unhealthy-" + strconv.FormatBool(batchMode)
			sendReport(id, time.Now(), 2, "x")
			eventually(t, 10*time.Second, func() bool { return hasDoc(infos, id) })
			eventually(t, 30*time.Second, func() bool { return hubReadCount(id) > 0 })
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
			// the period counts as notified, so later ticks neither read the hub again nor send
			if n := hubReadCount(id); n != 1 {
				t.Errorf("expected the hub to be read once, got %d", n)
			}
			if n, err := infos.CountDocuments(ctx, bson.M{"hub_id": id, "notified": true}); err != nil || n != 1 {
				t.Errorf("expected the tracking to be marked as notified, got %v %v", n, err)
			}
			if state, found := storedState(id); !found || state != 2 {
				t.Errorf("expected the report to be stored, got state=%v found=%v", state, found)
			}
		})

		t.Run("deleted hub", func(t *testing.T) {
			t.Parallel()
			id := "hub-deleted"
			sendReport(id, time.Now(), 2, "x")
			eventually(t, 10*time.Second, func() bool { return hasDoc(healths, id) && hasDoc(infos, id) })
			sendDelete(id)
			eventually(t, 10*time.Second, func() bool { return !hasDoc(healths, id) && !hasDoc(infos, id) })
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
		})

		t.Run("tracking of a hub that recovered meanwhile", func(t *testing.T) {
			t.Parallel()
			id := "hub-recovered-meanwhile"
			// the state a race leaves behind: a due tracking document next to a healthy report
			_, err := healths.InsertOne(ctx, bson.M{"hub_id": id, "state": 1, "reasons": bson.A{}, "report": bson.M{"state": 1}, "report_time": "", "received": time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = infos.InsertOne(ctx, bson.M{"hub_id": id, "unhealthy_since": time.Now().Add(-time.Hour).Unix(), "notified": false})
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, 30*time.Second, func() bool { return !hasDoc(infos, id) })
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
		})

		t.Run("unknown to the device-repository", func(t *testing.T) {
			t.Parallel()
			id := "unknown-hub"
			sendReport(id, time.Now(), 2, "x")
			eventually(t, 10*time.Second, func() bool { return hasDoc(infos, id) })
			eventually(t, 30*time.Second, func() bool { return !hasDoc(infos, id) })
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
		})
	})
}
