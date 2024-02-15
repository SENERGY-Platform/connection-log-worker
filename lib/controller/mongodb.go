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
	"log"

	"gopkg.in/mgo.v2"
)

func (this *Controller) getMongoDb() *mgo.Session {
	this.mongoDbOnce.Do(func() {
		session, err := mgo.Dial(this.config.MongoUrl)
		if err != nil {
			log.Fatal("error on connection to mongodb: ", err)
		}
		session.SetMode(mgo.Monotonic, true)
		this.mongoDbInstance = session
	})
	return this.mongoDbInstance.Copy()
}

func (this *Controller) getDeviceStateCollection() (session *mgo.Session, collection *mgo.Collection) {
	session = this.getMongoDb()
	collection = session.DB(this.config.MongoTable).C(this.config.DeviceStateCollection)
	err := collection.EnsureIndexKey("device")
	if err != nil {
		log.Fatal("error on getDeviceCollection device index: ", err)
	}
	return
}

func (this *Controller) getHubStateCollection() (session *mgo.Session, collection *mgo.Collection) {
	session = this.getMongoDb()
	collection = session.DB(this.config.MongoTable).C(this.config.HubStateCollection)
	err := collection.EnsureIndexKey("gateway")
	if err != nil {
		log.Fatal("error on getHubStateCollection gateway index: ", err)
	}
	return
}

type DeviceState struct {
	Device string `json:"device,omitempty" bson:"device,omitempty"`
	Online bool   `json:"online" bson:"online"`
	Since  int64  `json:"since" bson:"since"`
}

type HubState struct {
	Gateway string `json:"gateway,omitempty" bson:"gateway,omitempty"`
	Online  bool   `json:"online" bson:"online"`
	Since   int64  `json:"since" bson:"since"`
}
