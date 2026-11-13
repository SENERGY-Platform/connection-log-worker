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
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Connectionlog reads the states the worker writes, so it gets the worker's database under both its current
// setting name (MONGO_TABLE) and the contract name (MONGO_DATABASE).
func Connectionlog(ctx context.Context, wg *sync.WaitGroup, mongourl string, mongoDatabase string, permV2Url string) (hostport string, containerip string, err error) {
	log.Println("start connectionlog")
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/senergy-platform/connectionlog:dev",
			ExposedPorts: []string{"8080/tcp"},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("8080/tcp"),
			),
			Env: map[string]string{
				"MONGO_URL":          mongourl,
				"MONGO_TABLE":        mongoDatabase,
				"MONGO_DATABASE":     mongoDatabase,
				"PERMISSIONS_V2_URL": permV2Url,
			},
			AlwaysPullImage: true,
		},
		Started: true,
	})
	if err != nil {
		return "", "", err
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			log.Println("DEBUG: remove container connectionlog", c.Terminate(context.Background()))
		}()
		<-ctx.Done()
		reader, err := c.Logs(context.Background())
		if err != nil {
			log.Println("ERROR: unable to get container log")
			return
		}
		buf := new(strings.Builder)
		io.Copy(buf, reader)
		fmt.Println("CONNECTIONLOG LOGS: ------------------------------------------")
		fmt.Println(buf.String())
		fmt.Println("\n---------------------------------------------------------------")
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

const (
	// PostgresDb, PostgresUser and PostgresPw are what the Timescale fixture provisions for the worker.
	PostgresDb   = "connectionlog"
	PostgresUser = "connectionlog"
	PostgresPw   = "pw"
)

// Timescale starts the image of the production serving layer and provisions what the worker expects to find:
// the role connectionlog, the database connectionlog owned by it and the timescaledb extension in it.
// The worker creates its tables itself.
func Timescale(ctx context.Context, wg *sync.WaitGroup) (hostport string, containerip string, err error) {
	log.Println("start connectionlog timescale")
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "timescale/timescaledb-ha:pg18.6-ts2.29.2",
			ExposedPorts: []string{"5432/tcp"},
			WaitingFor:   wait.ForListeningPort("5432/tcp"),
			Env: map[string]string{
				"POSTGRES_PASSWORD": "superuser-pw",
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
		log.Println("DEBUG: remove container connectionlog timescale", c.Terminate(context.Background()))
	}()

	// psql goes over TCP: the entrypoint's temporary init server listens on the socket only and shuts down afterwards,
	// so only the final server answers here
	psql := func(database string, statement string) (string, error) {
		code, reader, err := c.Exec(ctx, []string{"psql", "-h", "127.0.0.1", "-U", "postgres", "-d", database, "-v", "ON_ERROR_STOP=1", "-tA", "-c", statement}, tcexec.WithEnv([]string{"PGPASSWORD=superuser-pw"}))
		if err != nil {
			return "", err
		}
		out, _ := io.ReadAll(reader)
		if code != 0 {
			return string(out), fmt.Errorf("psql exited with %d: %s", code, out)
		}
		return string(out), nil
	}
	deadline := time.Now().Add(120 * time.Second)
	for {
		_, err = psql("postgres", "SELECT 1")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("timescale does not accept connections: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, step := range []struct{ database, statement string }{
		{"postgres", "CREATE ROLE " + PostgresUser + " LOGIN PASSWORD '" + PostgresPw + "'"},
		{"postgres", "CREATE DATABASE " + PostgresDb + " OWNER " + PostgresUser},
		{PostgresDb, "CREATE EXTENSION IF NOT EXISTS timescaledb"},
	} {
		if _, err = psql(step.database, step.statement); err != nil {
			return "", "", err
		}
	}

	containerip, err = c.ContainerIP(ctx)
	if err != nil {
		return "", "", err
	}
	temp, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return "", "", err
	}
	hostport = temp.Port()

	return hostport, containerip, err
}

// SetPostgres points the config at a Timescale container; host and port are either the container's IP and 5432
// or localhost and the mapped port.
func SetPostgres(conf *config.Config, host string, port string) error {
	p, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		return err
	}
	conf.PostgresHost = host
	conf.PostgresPort = p
	conf.PostgresUser = PostgresUser
	conf.PostgresPw = PostgresPw
	conf.PostgresDb = PostgresDb
	return nil
}

// StartTimescale starts a container and points the config at it through its mapped host port.
func StartTimescale(ctx context.Context, wg *sync.WaitGroup, conf *config.Config) error {
	port, _, err := Timescale(ctx, wg)
	if err != nil {
		return err
	}
	return SetPostgres(conf, "localhost", port)
}
