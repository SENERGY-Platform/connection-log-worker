/*
 * Copyright 2019 InfAI (CC SES)
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
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mongoStartupTimeout = 10 * time.Second
	// mongoOperationTimeout bounds each operation; mgo had no operation deadline, only a one-minute socket timeout.
	mongoOperationTimeout = time.Minute
)

var (
	errEmptyDatabase   = errors.New("mongo database name must not be empty")
	errMissingPassword = errors.New("mongo password must not be empty when a mongo user is set")
)

func validateMongoConfig(conf config.Config) error {
	if conf.MongoDatabase == "" {
		return errEmptyDatabase
	}
	if conf.MongoUser != "" && conf.MongoPassword == "" {
		return errMissingPassword
	}
	return nil
}

// mongoClientOptions applies the credentials after the URI so they replace any given in MONGO_URL.
func mongoClientOptions(conf config.Config) *options.ClientOptions {
	opts := options.Client().ApplyURI(conf.MongoUrl)
	if conf.MongoUser != "" {
		opts.SetAuth(options.Credential{
			Username:   conf.MongoUser,
			Password:   conf.MongoPassword,
			AuthSource: conf.MongoAuthSource,
		})
	}
	return opts
}

func newMongoClient(ctx context.Context, conf config.Config) (*mongo.Client, error) {
	if err := validateMongoConfig(conf); err != nil {
		return nil, err
	}
	return startMongo(ctx, conf, mongoClientOptions(conf), mongoStartupTimeout)
}

// startMongo disconnects the client on every failure path, so a failed startup leaves nothing connected.
// On success the client is disconnected when ctx is done.
func startMongo(ctx context.Context, conf config.Config, opts *options.ClientOptions, timeout time.Duration) (*mongo.Client, error) {
	client, err := connectMongo(ctx, opts, conf.MongoDatabase, timeout)
	if err != nil {
		return nil, err
	}
	if err = ensureMongoIndexes(ctx, client, conf); err != nil {
		disconnectMongo(client, timeout)
		return nil, err
	}
	go func() {
		<-ctx.Done()
		disconnectMongo(client, timeout)
	}()
	return client, nil
}

// connectMongo runs listCollections on the service's database because Connect is lazy and ping needs no
// authentication; unreachable servers and wrong or missing credentials then fail at startup.
func connectMongo(ctx context.Context, opts *options.ClientOptions, database string, timeout time.Duration) (*mongo.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, err
	}
	listOpts := options.ListCollections().SetNameOnly(true).SetAuthorizedCollections(true)
	if _, err = client.Database(database).ListCollectionNames(ctx, bson.D{}, listOpts); err != nil {
		disconnectMongo(client, timeout)
		return nil, fmt.Errorf("mongo startup check failed: %w", err)
	}
	return client, nil
}

func disconnectMongo(client *mongo.Client, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = client.Disconnect(ctx)
}

// ensureMongoIndexes creates the indexes mgo's EnsureIndexKey created; the default names (device_1, ...)
// match the existing ones, so on an existing database this is a no-op.
func ensureMongoIndexes(ctx context.Context, client *mongo.Client, conf config.Config) error {
	ctx, cancel := context.WithTimeout(ctx, mongoOperationTimeout)
	defer cancel()
	db := client.Database(conf.MongoDatabase)
	for _, index := range []struct{ collection, key string }{
		{conf.DeviceStateCollection, "device"},
		{conf.HubStateCollection, "gateway"},
		{conf.DeviceOfflineNotificationInfoCollection, "device_id"},
	} {
		_, err := db.Collection(index.collection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: index.key, Value: 1}}})
		if err != nil {
			return fmt.Errorf("mongo index creation on %s.%s failed: %w", conf.MongoDatabase, index.collection, err)
		}
	}
	return nil
}

func mongoOperationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), mongoOperationTimeout)
}

// inFilter builds {field: {$in: values}}; a nil slice would encode as null, which the server rejects,
// where mgo encoded it as an empty array.
func inFilter(field string, values []string) bson.M {
	if values == nil {
		values = []string{}
	}
	return bson.M{field: bson.M{"$in": values}}
}

// decodeAll skips documents that do not decode into T instead of failing the whole read: mgo left
// mismatched fields zero, this driver returns an error, and one bad document would block every batch.
func decodeAll[T any](ctx context.Context, cursor *mongo.Cursor, logger *slog.Logger) ([]T, error) {
	defer cursor.Close(ctx)
	var result []T
	for cursor.Next(ctx) {
		var item T
		if err := cursor.Decode(&item); err != nil {
			logger.Error("skip undecodable mongo document", "id", cursor.Current.Lookup("_id").String(), "error", err)
			continue
		}
		result = append(result, item)
	}
	return result, cursor.Err()
}

// bulkWrite runs the models in order and stops at the first failure, like mgo's default Bulk.
// An empty batch is a no-op, as it was with mgo; BulkWrite would return ErrEmptySlice.
func bulkWrite(collection *mongo.Collection, models []mongo.WriteModel) error {
	if len(models) == 0 {
		return nil
	}
	ctx, cancel := mongoOperationContext()
	defer cancel()
	_, err := collection.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(true))
	return err
}

// upsertModel replaces the whole matching document, as mgo's Upsert did with a document without operators.
func upsertModel(filter bson.M, replacement interface{}) mongo.WriteModel {
	return mongo.NewReplaceOneModel().SetFilter(filter).SetReplacement(replacement).SetUpsert(true)
}

func (this *Controller) getDeviceStateCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.DeviceStateCollection)
}

func (this *Controller) getHubStateCollection() *mongo.Collection {
	return this.mongo.Database(this.config.MongoDatabase).Collection(this.config.HubStateCollection)
}

// DeviceState and HubState tag since with truncate to keep mgo's behaviour of cutting a stored double
// down to int64 instead of failing the decode.
type DeviceState struct {
	Device string `json:"device,omitempty" bson:"device,omitempty"`
	Online bool   `json:"online" bson:"online"`
	Since  int64  `json:"since" bson:"since,truncate"`
}

type HubState struct {
	Gateway string `json:"gateway,omitempty" bson:"gateway,omitempty"`
	Online  bool   `json:"online" bson:"online"`
	Since   int64  `json:"since" bson:"since,truncate"`
}
