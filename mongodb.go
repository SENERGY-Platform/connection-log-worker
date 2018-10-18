/*
 * Copyright 2018 InfAI (CC SES)
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

package main

import (
	"log"
	"sync"

	"gopkg.in/mgo.v2"
)

var mongoDbInstance *mgo.Session
var mongoDbOnce sync.Once

func getMongoDb() *mgo.Session {
	mongoDbOnce.Do(func() {
		session, err := mgo.Dial(Config.MongoUrl)
		if err != nil {
			log.Fatal("error on connection to mongodb: ", err)
		}
		session.SetMode(mgo.Monotonic, true)
		mongoDbInstance = session
	})
	return mongoDbInstance.Copy()
}

func getConnectorGatewayCollection() (session *mgo.Session, collection *mgo.Collection) {
	session = getMongoDb()
	collection = session.DB(Config.MongoTable).C(Config.ConnectorGatewayCollection)
	err := collection.EnsureIndexKey("connector")
	if err != nil {
		log.Fatal("error on ConnectorGatewayCollection connector index: ", err)
	}
	err = collection.EnsureIndexKey("gateway")
	if err != nil {
		log.Fatal("error on ConnectorGatewayCollection gateway index: ", err)
	}
	return
}

func getConnectorDeviceCollection() (session *mgo.Session, collection *mgo.Collection) {
	session = getMongoDb()
	collection = session.DB(Config.MongoTable).C(Config.ConnectorDeviceCollection)
	err := collection.EnsureIndexKey("connector")
	if err != nil {
		log.Fatal("error on ConnectorDeviceCollection connector index: ", err)
	}
	err = collection.EnsureIndexKey("device")
	if err != nil {
		log.Fatal("error on ConnectorDeviceCollection device index: ", err)
	}
	return
}

func getDeviceStateCollection() (session *mgo.Session, collection *mgo.Collection) {
	session = getMongoDb()
	collection = session.DB(Config.MongoTable).C(Config.DeviceStateCollection)
	err := collection.EnsureIndexKey("device")
	if err != nil {
		log.Fatal("error on getDeviceCollection device index: ", err)
	}
	return
}

func getGatewayStateCollection() (session *mgo.Session, collection *mgo.Collection) {
	session = getMongoDb()
	collection = session.DB(Config.MongoTable).C(Config.GatewayStateCollection)
	err := collection.EnsureIndexKey("gateway")
	if err != nil {
		log.Fatal("error on getGatewayStateCollection gateway index: ", err)
	}
	return
}

type ConnectorGatewayConnection struct {
	Connector string `json:"connector,omitempty" bson:"connector,omitempty"`
	Gateway   string `json:"gateway,omitempty" bson:"gateway,omitempty"`
}

type ConnectorDeviceConnection struct {
	Connector string `json:"connector,omitempty" bson:"connector,omitempty"`
	Device    string `json:"device,omitempty" bson:"device,omitempty"`
}

type DeviceState struct {
	Device string `json:"device,omitempty" bson:"device,omitempty"`
	Online bool   `json:"online" bson:"online"`
}

type GatewayState struct {
	Gateway string `json:"gateway,omitempty" bson:"gateway,omitempty"`
	Online  bool   `json:"online" bson:"online"`
}
