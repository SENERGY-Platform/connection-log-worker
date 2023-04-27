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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"log"
	"sync"
)

func Connectionlog(ctx context.Context, wg *sync.WaitGroup, mongourl string, permurl string, influxurl string) (hostport string, containerip string, err error) {
	log.Println("start connectionlog")
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/senergy-platform/connectionlog:dev",
			ExposedPorts: []string{"8080/tcp"},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("8080/tcp"),
			),
			Env: map[string]string{
				"MONGO_URL":        mongourl,
				"PERMISSIONS_URL":  permurl,
				"INFLUXDB_URL":     influxurl,
				"INFLUXDB_TIMEOUT": "3",
				"INFLUXDB_USER":    "user",
				"INFLUXDB_PW":      "pw",
			},
		},
		Started: true,
	})
	if err != nil {
		return "", "", err
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		log.Println("DEBUG: remove container connectionlog", c.Terminate(context.Background()))
	}()

	containerip, err = c.ContainerIP(ctx)
	if err != nil {
		return "", "", err
	}
	temp, err := c.MappedPort(ctx, "8080/tcp")
	if err != nil {
		return "", "", err
	}
	hostport = temp.Port()

	return hostport, containerip, err
}

func Influxdb(ctx context.Context, wg *sync.WaitGroup) (hostport string, containerip string, err error) {
	log.Println("start connectionlog influx")
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "influxdb:1.6.3",
			ExposedPorts: []string{"8086/tcp"},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("8086/tcp"),
			),
			Env: map[string]string{
				"INFLUXDB_DB":             "connectionlog",
				"INFLUXDB_ADMIN_ENABLED":  "true",
				"INFLUXDB_ADMIN_USER":     "user",
				"INFLUXDB_ADMIN_PASSWORD": "pw",
			},
		},
		Started: true,
	})
	if err != nil {
		return "", "", err
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		log.Println("DEBUG: remove container connectionlog influx", c.Terminate(context.Background()))
	}()

	containerip, err = c.ContainerIP(ctx)
	if err != nil {
		return "", "", err
	}
	temp, err := c.MappedPort(ctx, "8086/tcp")
	if err != nil {
		return "", "", err
	}
	hostport = temp.Port()

	return hostport, containerip, err
}
