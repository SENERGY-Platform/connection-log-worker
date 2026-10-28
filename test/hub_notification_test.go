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
	"github.com/SENERGY-Platform/connection-log-worker/lib/controller"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/consumer"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/util"
	"github.com/SENERGY-Platform/connection-log-worker/test/helper"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/segmentio/kafka-go"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	hubOfflineTestOwner = "hub-owner"
	// The delay is longer than two batch windows plus the interval, so that a hub which comes back
	// online right after its offline log was processed does so before the delay has passed.
	hubOfflineTestDelay = 4 * time.Second
)

func TestHubOfflineNotifications(t *testing.T) {
	t.Run("batch mode", func(t *testing.T) { testHubOfflineNotifications(t, true) })
	t.Run("single mode", func(t *testing.T) { testHubOfflineNotifications(t, false) })
}

func testHubOfflineNotifications(t *testing.T, batchMode bool) {
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
	defaultConfig.HubOfflineNotificationDelay = hubOfflineTestDelay.String()
	defaultConfig.HubOfflineNotificationInterval = "500ms"

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

	// stored as offline before the worker runs, like a hub that was offline before the feature existed
	_, err = db.Collection(conf.HubStateCollection).InsertOne(ctx, bson.M{"gateway": "hub-already-offline", "online": false, "since": time.Now().Add(-time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}

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
	logProducer, err := helper.GetProducer(broker, conf.HubLogTopic, true)
	if err != nil {
		t.Fatal(err)
	}
	defer logProducer.Close()
	hubProducer, err := helper.GetProducer(broker, conf.HubTopic, true)
	if err != nil {
		t.Fatal(err)
	}
	defer hubProducer.Close()

	// the consumers start at the latest offset, so give them time to be assigned
	time.Sleep(3 * time.Second)

	infos := db.Collection(conf.HubOfflineNotificationInfoCollection)
	hasInfo := func(id string) bool {
		n, err := infos.CountDocuments(ctx, bson.M{"hub_id": id})
		if err != nil {
			t.Error(err)
		}
		return n > 0
	}
	sendLog := func(id string, connected bool) {
		b, err := json.Marshal(model.HubLog{Id: id, Connected: connected, Time: time.Now()})
		if err != nil {
			t.Error(err)
			return
		}
		err = logProducer.WriteMessages(ctx, kafka.Message{Key: []byte(id), Value: b})
		if err != nil {
			t.Error(err)
		}
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
	// waitPastDelay lets several more notification ticks run after the delay has passed
	waitPastDelay := func() { time.Sleep(hubOfflineTestDelay + 3*time.Second) }

	// the group returns only after its parallel scenarios have finished, so the deferred cleanup runs after them
	t.Run("scenarios", func(t *testing.T) {
		t.Run("stays offline", func(t *testing.T) {
			t.Parallel()
			id := "hub-stays-offline"
			sendLog(id, false)
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 1 })
			n := notifier.forHub(id)[0]
			if n.UserId != hubOfflineTestOwner || n.Topic != "mgw" || n.Title != "Network Offline" ||
				!strings.Contains(n.Message, "network name of "+id+" ("+id+") has been offline for ") {
				t.Errorf("unexpected notification %#v", n)
			}
			sendLog(id, false)
			waitPastDelay()
			sendLog(id, false)
			time.Sleep(time.Second)
			if got := notifier.forHub(id); len(got) != 1 {
				t.Errorf("expected exactly one notification, got %#v", got)
			}
		})

		t.Run("back online before the delay", func(t *testing.T) {
			t.Parallel()
			id := "hub-back-online"
			sendLog(id, false)
			eventually(t, 10*time.Second, func() bool { return hasInfo(id) })
			sendLog(id, true)
			eventually(t, 10*time.Second, func() bool { return !hasInfo(id) })
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
		})

		t.Run("offline again after being notified", func(t *testing.T) {
			t.Parallel()
			id := "hub-offline-twice"
			sendLog(id, false)
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 1 })
			sendLog(id, true)
			eventually(t, 10*time.Second, func() bool { return !hasInfo(id) })
			sendLog(id, false)
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 2 })
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 2 {
				t.Errorf("expected exactly two notifications, got %#v", got)
			}
		})

		t.Run("already offline before tracking", func(t *testing.T) {
			t.Parallel()
			id := "hub-already-offline"
			sendLog(id, false)
			time.Sleep(3 * time.Second)
			if hasInfo(id) {
				t.Error("expected no tracking info for a hub that did not change its state")
			}
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
		})

		t.Run("deleted while offline", func(t *testing.T) {
			t.Parallel()
			id := "hub-deleted"
			sendLog(id, false)
			eventually(t, 10*time.Second, func() bool { return hasInfo(id) })
			sendDelete(id)
			eventually(t, 10*time.Second, func() bool { return !hasInfo(id) })
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
		})

		t.Run("inactive hub", func(t *testing.T) {
			t.Parallel()
			id := "inactive-hub-offline-" + strconv.FormatBool(batchMode)
			sendLog(id, false)
			eventually(t, 10*time.Second, func() bool { return hasInfo(id) })
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
		})

		t.Run("unknown to the device-repository", func(t *testing.T) {
			t.Parallel()
			id := "unknown-hub"
			sendLog(id, false)
			eventually(t, 10*time.Second, func() bool { return hasInfo(id) })
			eventually(t, 30*time.Second, func() bool { return !hasInfo(id) })
			waitPastDelay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
		})
	})
}

// startHubNotificationDependencies is server.NewPartial with MongoDB and InfluxDB addressed through their
// mapped host ports instead of the container IPs, so that it also works where the bridge network is not routable
// from the host (rootless Docker).
func startHubNotificationDependencies(ctx context.Context, wg *sync.WaitGroup, defaults config.Config) (conf config.Config, err error) {
	conf = defaults
	conf.KafkaUrl, err = server.Kafka(ctx, wg)
	if err != nil {
		return conf, err
	}
	influxPort, _, err := server.Influxdb(ctx, wg)
	if err != nil {
		return conf, err
	}
	conf.InfluxdbUrl = "http://localhost:" + influxPort
	conf.InfluxdbDb = "connectionlog"
	conf.InfluxdbUser = "user"
	conf.InfluxdbPw = "pw"
	conf.InfluxdbTimeout = 3
	mongoPort, _, err := server.MongoDB(ctx, wg)
	if err != nil {
		return conf, err
	}
	conf.MongoUrl = "mongodb://localhost:" + mongoPort
	return conf, nil
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type fakeNotifier struct {
	mux           sync.Mutex
	notifications []controller.Notification
}

func (this *fakeNotifier) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var n controller.Notification
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	this.notifications = append(this.notifications, n)
}

// forHub selects by the id the message carries in parentheses.
func (this *fakeNotifier) forHub(id string) (result []controller.Notification) {
	this.mux.Lock()
	defer this.mux.Unlock()
	for _, n := range this.notifications {
		if strings.Contains(n.Message, "("+id+")") {
			result = append(result, n)
		}
	}
	return result
}

var (
	hubReadsMux sync.Mutex
	hubReads    = map[string]int{}
)

// hubReadCount is how often fakeHubRepository served a read of the hub; ids must be unique per test run.
func hubReadCount(id string) int {
	hubReadsMux.Lock()
	defer hubReadsMux.Unlock()
	return hubReads[id]
}

// fakeHubRepository answers hub reads, with 404 for ids starting with "unknown" and the attribute inactive=true
// for ids starting with "inactive", and accepts everything else.
func fakeHubRepository(w http.ResponseWriter, r *http.Request) {
	id, isHubRead := strings.CutPrefix(r.URL.Path, "/hubs/")
	if r.Method != http.MethodGet || !isHubRead || strings.Contains(id, "/") {
		w.WriteHeader(http.StatusOK)
		return
	}
	if strings.HasPrefix(id, "unknown") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	hubReadsMux.Lock()
	hubReads[id]++
	hubReadsMux.Unlock()
	hub := models.Hub{Id: id, Name: "name of " + id, OwnerId: hubOfflineTestOwner}
	if strings.HasPrefix(id, "inactive") {
		hub.Attributes = []models.Attribute{{Key: "inactive", Value: "true", Origin: "web-ui"}}
	}
	json.NewEncoder(w).Encode(hub)
}
