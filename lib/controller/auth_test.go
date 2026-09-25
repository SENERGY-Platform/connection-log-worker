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
	"crypto/rand"
	"encoding/hex"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// authTestServer needs a throwaway server with access control; MONGO_AUTH_TEST_USER and
// MONGO_AUTH_TEST_PASSWORD are root credentials, used to create and remove the test users.
func authTestServer(t *testing.T) (url string, root *mongo.Client, rootPassword string) {
	url, rootUser, rootPassword := os.Getenv("MONGO_AUTH_TEST_URL"), os.Getenv("MONGO_AUTH_TEST_USER"), os.Getenv("MONGO_AUTH_TEST_PASSWORD")
	if testing.Short() || url == "" || rootUser == "" || rootPassword == "" {
		t.Skip("needs MONGO_AUTH_TEST_URL, MONGO_AUTH_TEST_USER and MONGO_AUTH_TEST_PASSWORD, not in -short")
	}
	root, err := mongo.Connect(context.Background(), options.Client().ApplyURI(url).SetAuth(options.Credential{Username: rootUser, Password: rootPassword, AuthSource: "admin"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Disconnect(context.Background()) })
	return url, root, rootPassword
}

func authTestConfig(url, user, password, database string) config.Config {
	conf := testMongoConfig()
	conf.MongoUrl, conf.MongoUser, conf.MongoPassword, conf.MongoDatabase = url, user, password, database
	return conf
}

func TestMongoStartAuthenticates(t *testing.T) {
	url, root, rootPassword := authTestServer(t)
	suffix := randomHex(t)
	testDB, otherDB := "connection_log_auth_test_"+suffix, "connection_log_auth_other_"+suffix
	svcUser, svcPassword := "clw-test-"+suffix, randomHex(t)
	otherUser, otherPassword := "clw-other-"+suffix, randomHex(t)
	readUser, readPassword := "clw-read-"+suffix, randomHex(t)
	createUser(t, root, svcUser, svcPassword, "readWrite", testDB)
	createUser(t, root, otherUser, otherPassword, "readWrite", otherDB)
	createUser(t, root, readUser, readPassword, "read", testDB)
	passwords := []string{svcPassword, otherPassword, readPassword, rootPassword}

	t.Run("New with correct credentials creates the indexes", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctrl, err := New(ctx, authTestConfig(url, svcUser, svcPassword, testDB))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ctrl.getDeviceStates([]string{"x"}); err != nil {
			t.Errorf("query as the service user: %v", err)
		}
		assertIndexes(t, root, testDB, "devicestate", "device_1")
		assertIndexes(t, root, testDB, "gatewaystate", "gateway_1")
		assertIndexes(t, root, testDB, "device_offline_notification_info", "device_id_1")
		// A second start against the existing indexes must not fail.
		if _, err = New(ctx, authTestConfig(url, svcUser, svcPassword, testDB)); err != nil {
			t.Errorf("restart: %v", err)
		}
	})

	cases := []struct {
		name, user, password string
		wantErr              string
	}{
		{"correct credentials", svcUser, svcPassword, ""},
		{"no credentials", "", "", "mongo startup check failed: "},
		{"user of another database", otherUser, otherPassword, "mongo startup check failed: "},
		{"wrong password", svcUser, svcPassword + "-wrong", "mongo startup check failed: "},
		// listCollections passes, index creation does not.
		{"read-only user", readUser, readPassword, "mongo index creation on "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conf := authTestConfig(url, c.user, c.password, testDB)
			if err := validateMongoConfig(conf); err != nil {
				t.Fatal(err)
			}
			pools := &poolCounter{}
			ctx, cancel := context.WithCancel(context.Background())
			client, err := startMongo(ctx, conf, mongoClientOptions(conf).SetPoolMonitor(pools.monitor()), mongoStartupTimeout)
			if c.wantErr == "" {
				cancel()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				waitFor(t, func() bool { return pools.closed.Load() == pools.created.Load() })
				pools.assertAllClosed(t)
				return
			}
			defer cancel()
			if err == nil {
				_ = client.Disconnect(context.Background())
				t.Fatalf("expected an error containing %q", c.wantErr)
			}
			t.Logf("startup error: %v", err)
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("unexpected error: %v", err)
			}
			for _, pw := range passwords {
				if strings.Contains(err.Error(), pw) {
					t.Error("error text contains a password")
				}
			}
			pools.assertAllClosed(t)
		})
	}
}

// TestMongoOperations pins the semantics the mgo implementation had, run as the service user.
func TestMongoOperations(t *testing.T) {
	url, root, _ := authTestServer(t)
	suffix := randomHex(t)
	testDB := "connection_log_ops_test_" + suffix
	user, password := "clw-ops-"+suffix, randomHex(t)
	createUser(t, root, user, password, "readWrite", testDB)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl, err := New(ctx, authTestConfig(url, user, password, testDB))
	if err != nil {
		t.Fatal(err)
	}
	rootDB := root.Database(testDB)
	now := time.Unix(1700000000, 0)

	t.Run("setDeviceState reports an update only on change", func(t *testing.T) {
		for i, step := range []struct{ connected, wantUpdate bool }{{true, true}, {true, false}, {false, true}, {false, false}} {
			update, err := ctrl.setDeviceState(model.DeviceLog{Id: "d1", Connected: step.connected})
			if err != nil {
				t.Fatal(err)
			}
			if update != step.wantUpdate {
				t.Errorf("step %d: update = %v, want %v", i, update, step.wantUpdate)
			}
		}
		assertCount(t, rootDB.Collection("devicestate"), bson.M{"device": "d1"}, 1)
	})

	t.Run("setHubState reports an update only on change", func(t *testing.T) {
		for i, step := range []struct{ connected, wantUpdate bool }{{false, true}, {false, false}, {true, true}} {
			update, err := ctrl.setHubState(model.HubLog{Id: "h1", Connected: step.connected})
			if err != nil {
				t.Fatal(err)
			}
			if update != step.wantUpdate {
				t.Errorf("step %d: update = %v, want %v", i, update, step.wantUpdate)
			}
		}
		assertCount(t, rootDB.Collection("gatewaystate"), bson.M{"gateway": "h1"}, 1)
	})

	t.Run("bulk upserts replace whole documents", func(t *testing.T) {
		_, err := rootDB.Collection("devicestate").InsertOne(context.Background(), bson.M{"device": "d2", "online": false, "since": int64(1), "extra": "x"})
		if err != nil {
			t.Fatal(err)
		}
		err = ctrl.setDeviceStates([]model.DeviceLog{{Id: "d2", Connected: true, Time: now}, {Id: "d3", Connected: false, Time: now}})
		if err != nil {
			t.Fatal(err)
		}
		states, err := ctrl.getDeviceStates([]string{"d2", "d3", "missing"})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]DeviceState{
			"d2": {Device: "d2", Online: true, Since: now.Unix()},
			"d3": {Device: "d3", Online: false, Since: now.Unix()},
		}
		if !reflect.DeepEqual(states, want) {
			t.Errorf("states = %+v, want %+v", states, want)
		}
		assertCount(t, rootDB.Collection("devicestate"), bson.M{"extra": bson.M{"$exists": true}}, 0)

		if err = ctrl.setHubStates([]model.HubLog{{Id: "h2", Connected: true, Time: now}}); err != nil {
			t.Fatal(err)
		}
		hubs, err := ctrl.getHubStates([]string{"h2"})
		if err != nil {
			t.Fatal(err)
		}
		if want := map[string]HubState{"h2": {Gateway: "h2", Online: true, Since: now.Unix()}}; !reflect.DeepEqual(hubs, want) {
			t.Errorf("hubs = %+v, want %+v", hubs, want)
		}
	})

	t.Run("empty and nil batches are no-ops", func(t *testing.T) {
		if err := ctrl.setDeviceStates(nil); err != nil {
			t.Errorf("setDeviceStates(nil): %v", err)
		}
		if err := ctrl.setHubStates([]model.HubLog{}); err != nil {
			t.Errorf("setHubStates(empty): %v", err)
		}
		if err := ctrl.setDeviceOfflineNotificationInfosBatch(nil); err != nil {
			t.Errorf("setDeviceOfflineNotificationInfosBatch(nil): %v", err)
		}
		if states, err := ctrl.getDeviceStates(nil); err != nil || len(states) != 0 {
			t.Errorf("getDeviceStates(nil) = %v, %v", states, err)
		}
		if states, err := ctrl.getHubStates(nil); err != nil || len(states) != 0 {
			t.Errorf("getHubStates(nil) = %v, %v", states, err)
		}
		if infos, err := ctrl.getDeviceOfflineNotificationInfosBatch(nil); err != nil || len(infos) != 0 {
			t.Errorf("getDeviceOfflineNotificationInfosBatch(nil) = %v, %v", infos, err)
		}
		if err := ctrl.deleteDeviceStates(nil); err != nil {
			t.Errorf("deleteDeviceStates(nil): %v", err)
		}
		if err := ctrl.deleteHubStates(nil); err != nil {
			t.Errorf("deleteHubStates(nil): %v", err)
		}
		if err := ctrl.removeDeviceOfflineNotificationInfosBatch(nil); err != nil {
			t.Errorf("removeDeviceOfflineNotificationInfosBatch(nil): %v", err)
		}
		assertCount(t, rootDB.Collection("devicestate"), bson.M{}, 3)
	})

	t.Run("an undecodable document is skipped, the rest is returned", func(t *testing.T) {
		_, err := rootDB.Collection("devicestate").InsertOne(context.Background(), bson.M{"device": "bad", "online": "yes", "since": int64(1)})
		if err != nil {
			t.Fatal(err)
		}
		states, err := ctrl.getDeviceStates([]string{"bad", "d2"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := states["d2"]; !ok || len(states) != 1 {
			t.Errorf("states = %+v, want only d2", states)
		}
		// The next state write for the device replaces the broken document.
		if err = ctrl.setDeviceStates([]model.DeviceLog{{Id: "bad", Connected: true, Time: now}}); err != nil {
			t.Fatal(err)
		}
		states, err = ctrl.getDeviceStates([]string{"bad"})
		if err != nil {
			t.Fatal(err)
		}
		if want := (DeviceState{Device: "bad", Online: true, Since: now.Unix()}); states["bad"] != want {
			t.Errorf("state = %+v, want %+v", states["bad"], want)
		}
	})

	t.Run("deletes", func(t *testing.T) {
		if err := ctrl.deleteDeviceState("d1"); err != nil {
			t.Fatal(err)
		}
		if err := ctrl.deleteDeviceStates([]string{"d2", "d3"}); err != nil {
			t.Fatal(err)
		}
		if err := ctrl.deleteHubState("h1"); err != nil {
			t.Fatal(err)
		}
		if err := ctrl.deleteHubStates([]string{"h2"}); err != nil {
			t.Fatal(err)
		}
		assertCount(t, rootDB.Collection("devicestate"), bson.M{"device": bson.M{"$in": []string{"d1", "d2", "d3"}}}, 0)
		assertCount(t, rootDB.Collection("gatewaystate"), bson.M{}, 0)
		// Deleting what is not there is not an error.
		if err := ctrl.deleteDeviceState("d1"); err != nil {
			t.Error(err)
		}
	})

	t.Run("offline notification infos", func(t *testing.T) {
		_, found, err := ctrl.getDeviceOfflineNotificationInfos("n1")
		if err != nil || found {
			t.Fatalf("missing info: found = %v, err = %v", found, err)
		}
		info := DeviceOfflineNotificationInfo{DeviceId: "n1", OfflineSince: 5}
		if err = ctrl.setDeviceOfflineNotificationInfos(info); err != nil {
			t.Fatal(err)
		}
		info.Notified = true
		if err = ctrl.setDeviceOfflineNotificationInfos(info); err != nil {
			t.Fatal(err)
		}
		got, found, err := ctrl.getDeviceOfflineNotificationInfos("n1")
		if err != nil || !found || got != info {
			t.Fatalf("got %+v, found = %v, err = %v, want %+v", got, found, err, info)
		}
		assertCount(t, rootDB.Collection("device_offline_notification_info"), bson.M{"device_id": "n1"}, 1)

		batch := []DeviceOfflineNotificationInfo{{DeviceId: "n2", OfflineSince: 6}, {DeviceId: "n3", OfflineSince: 7, Notified: true}}
		if err = ctrl.setDeviceOfflineNotificationInfosBatch(batch); err != nil {
			t.Fatal(err)
		}
		infos, err := ctrl.getDeviceOfflineNotificationInfosBatch([]string{"n1", "n2", "n3", "missing"})
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for id := range infos {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if !reflect.DeepEqual(ids, []string{"n1", "n2", "n3"}) || infos["n3"] != batch[1] {
			t.Errorf("infos = %+v", infos)
		}

		if err = ctrl.removeDeviceOfflineNotificationInfos("n1"); err != nil {
			t.Fatal(err)
		}
		if err = ctrl.removeDeviceOfflineNotificationInfosBatch([]string{"n2", "n3"}); err != nil {
			t.Fatal(err)
		}
		assertCount(t, rootDB.Collection("device_offline_notification_info"), bson.M{}, 0)
	})
}

func assertCount(t *testing.T, collection *mongo.Collection, filter bson.M, want int64) {
	t.Helper()
	n, err := collection.CountDocuments(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Errorf("%s: %d documents match %v, want %d", collection.Name(), n, filter, want)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

// createUser registers the cleanup first, so a partly failed creation is removed as well.
func createUser(t *testing.T, root *mongo.Client, user, password, role, db string) {
	t.Helper()
	admin := root.Database("admin")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = admin.RunCommand(ctx, bson.D{{Key: "dropUser", Value: user}}).Err()
		_ = root.Database(db).Drop(ctx)
	})
	cmd := bson.D{
		{Key: "createUser", Value: user},
		{Key: "pwd", Value: password},
		{Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: role}, {Key: "db", Value: db}}}},
	}
	if err := admin.RunCommand(context.Background(), cmd).Err(); err != nil {
		t.Fatalf("create user: %v", err)
	}
}

func assertIndexes(t *testing.T, root *mongo.Client, db, collection string, want ...string) {
	t.Helper()
	specs, err := root.Database(db).Collection(collection).Indexes().ListSpecifications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, s := range specs {
		names = append(names, s.Name)
	}
	for _, w := range want {
		if !slices.Contains(names, w) {
			t.Errorf("index %q missing on %s, have %v", w, collection, names)
		}
	}
}

func randomHex(t *testing.T) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
