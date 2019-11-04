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

package main

import (
	"context"
	"flag"
	"github.com/SENERGY-Platform/connection-log-worker/lib"
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/consumer"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configLocation := flag.String("config", "config.json", "configuration file")
	flag.Parse()

	conf, err := config.Load(*configLocation)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background() //no desire to cancel or stop running program; for tests you can use context.WithCancel(context.Background())
	err = lib.Start(ctx, conf, func(err error, consumer *consumer.Consumer) {
		log.Fatal("FATAL ERROR:", err)
	})
	if err != nil {
		log.Fatal("FATAL error:", err)
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	sig := <-shutdown
	log.Println("received shutdown signal", sig)
}
