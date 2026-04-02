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

package consumer

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"sync"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/source/util"
	"github.com/segmentio/kafka-go"
)

func RunConsumer(ctx context.Context, zk string, groupid string, topic string, initTopic bool, listener func(topic string, msg []byte) error, errorhandler func(err error, consumer *Consumer)) (err error) {
	consumer := &Consumer{groupId: groupid, zkUrl: zk, topic: topic, listener: listener, errorhandler: errorhandler, ctx: ctx, initTopic: initTopic}
	err = consumer.start()
	return
}

type Consumer struct {
	count        int
	zkUrl        string
	groupId      string
	topic        string
	ctx          context.Context
	cancel       context.CancelFunc
	listener     func(topic string, msg []byte) error
	errorhandler func(err error, consumer *Consumer)
	mux          sync.Mutex
	initTopic    bool
}

func (this *Consumer) start() error {
	slog.Debug("start kafka topic consumer", "topic", this.topic, "group-id", this.groupId)
	broker, err := util.GetBroker(this.zkUrl)
	if err != nil {
		slog.Error("unable to get broker list", "error", err)
		return err
	}
	if this.initTopic {
		err = util.InitTopic(this.zkUrl, this.topic)
		if err != nil {
			slog.Error("unable to create topic", "topic", this.topic, "error", err)
			return err
		}
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		CommitInterval: 0, //synchronous commits
		Brokers:        broker,
		GroupID:        this.groupId,
		Topic:          this.topic,
		MaxWait:        1 * time.Second,
		Logger:         log.New(io.Discard, "", 0),
		ErrorLogger:    log.New(io.Discard, "", 0),
	})
	go func() {
		defer r.Close()
		defer slog.Info("close kafka topic consumer", "topic", this.topic)
		for {
			select {
			case <-this.ctx.Done():
				return
			default:
				m, err := r.FetchMessage(this.ctx)
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					return
				}
				if err != nil {
					slog.Error("unable to fetch kafka message", "topic", this.topic, "error", err)
					this.errorhandler(err, this)
					return
				}

				err = retry(func() error {
					return this.listener(m.Topic, m.Value)
				}, func(n int64) time.Duration {
					return time.Duration(n) * time.Second
				}, 10*time.Minute)

				if err != nil {
					slog.Error("unable to handle message (no commit)", "topic", this.topic, "error", err)
					this.errorhandler(err, this)
				} else {
					err = r.CommitMessages(this.ctx, m)
				}
			}
		}
	}()
	return err
}

func retry(f func() error, waitProvider func(n int64) time.Duration, timeout time.Duration) (err error) {
	err = errors.New("")
	start := time.Now()
	for i := int64(1); err != nil && time.Since(start) < timeout; i++ {
		err = f()
		if err != nil {
			slog.Error("kafka listener error", "error", err)
			wait := waitProvider(i)
			if time.Since(start)+wait < timeout {
				slog.Error("retry after", "wait", wait.String())
				time.Sleep(wait)
			} else {
				return err
			}
		}
	}
	return err
}
