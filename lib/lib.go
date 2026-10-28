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

package lib

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/controller"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/consumer"
)

func Start(ctx context.Context, config config.Config, runtimeErrorHandler func(err error, consumer *consumer.Consumer)) error {
	control, err := controller.New(ctx, config)
	if err != nil {
		return err
	}
	err = startMetricsServer(ctx, config, control)
	if err != nil {
		return err
	}
	return consumer.Start(ctx, config, control, runtimeErrorHandler)
}

func StartBatch(
	ctx context.Context,
	config config.Config,
	runtimeErrorHandler func(err error, consumer *consumer.BatchConsumer),
) error {
	control, err := controller.New(ctx, config)
	if err != nil {
		return err
	}
	err = startMetricsServer(ctx, config, control)
	if err != nil {
		return err
	}
	return consumer.StartBulk(ctx, config, control, runtimeErrorHandler)
}

// startMetricsServer serves /metrics until ctx is done; empty or "-" as PrometheusPort disables it.
// The port is bound before returning, so an occupied port fails the start.
func startMetricsServer(ctx context.Context, conf config.Config, control *controller.Controller) error {
	if conf.PrometheusPort == "" || conf.PrometheusPort == "-" {
		return nil
	}
	listener, err := net.Listen("tcp", ":"+conf.PrometheusPort)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", control.MetricsHandler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			conf.GetLogger().Error("metrics server stopped", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return nil
}
