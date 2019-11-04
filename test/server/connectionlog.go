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

package server

import (
	"context"
	"github.com/influxdata/influxdb/client/v2"
	"github.com/ory/dockertest"
	"log"
	"net/http"
	"time"
)

func Connectionlog(pool *dockertest.Pool, ctx context.Context, mongourl string, permurl string, influxurl string) (hostPort string, ipAddress string, err error) {
	log.Println("start connectionlog")
	repo, err := pool.Run("fgseitsrancher.wifa.intern.uni-leipzig.de:5000/connection-log", "dev", []string{
		"MONGO_URL=" + mongourl,
		"PERMISSIONS_URL=" + permurl,
		"INFLUXDB_URL=" + influxurl,
		"INFLUXDB_TIMEOUT=3",
		"INFLUXDB_USER=user",
		"INFLUXDB_PW=pw",
	})
	if err != nil {
		return "", "", err
	}
	go func() {
		<-ctx.Done()
		repo.Close()
	}()
	go Dockerlog(pool, ctx, repo, "CONNECTIONLOG")
	hostPort = repo.GetPort("8080/tcp")
	err = pool.Retry(func() error {
		log.Println("try connectionlog connection...")
		_, err := http.Get("http://" + repo.Container.NetworkSettings.IPAddress + ":8080")
		if err != nil {
			log.Println(err)
		}
		return err
	})
	return hostPort, repo.Container.NetworkSettings.IPAddress, err
}

func Influxdb(pool *dockertest.Pool, ctx context.Context) (hostPort string, ipAddress string, err error) {
	log.Println("start connectionlog")
	repo, err := pool.Run("influxdb", "1.6.3", []string{
		"INFLUXDB_DB=connectionlog",
		"INFLUXDB_ADMIN_ENABLED=true",
		"INFLUXDB_ADMIN_USER=user",
		"INFLUXDB_ADMIN_PASSWORD=pw",
	})
	if err != nil {
		return "", "", err
	}
	go func() {
		<-ctx.Done()
		repo.Close()
	}()
	hostPort = repo.GetPort("8086/tcp")
	err = pool.Retry(func() error {
		log.Println("try connectionlog connection...")
		client, err := client.NewHTTPClient(client.HTTPConfig{
			Addr:     "http://" + repo.Container.NetworkSettings.IPAddress + ":8086",
			Username: "user",
			Password: "pw",
			Timeout:  time.Duration(1) * time.Second,
		})
		if err != nil {
			log.Println(err)
			return err
		}
		defer client.Close()
		_, _, err = client.Ping(1 * time.Second)
		if err != nil {
			log.Println(err)
			return err
		}
		return err
	})
	return hostPort, repo.Container.NetworkSettings.IPAddress, err
}
