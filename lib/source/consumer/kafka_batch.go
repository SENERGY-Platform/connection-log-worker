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

func RunBatchConsumer(ctx context.Context, zk string, groupid string, topic string, initTopic bool, maxMessages int, timeframe time.Duration, listener func(topic string, msgs [][]byte) error, errorhandler func(err error, consumer *BatchConsumer)) (err error) {
	consumer := &BatchConsumer{groupId: groupid, zkUrl: zk, topic: topic, listener: listener, errorhandler: errorhandler, ctx: ctx, initTopic: initTopic, maxMessages: maxMessages, timeframe: timeframe}
	err = consumer.start()
	return
}

type BatchConsumer struct {
	zkUrl        string
	groupId      string
	topic        string
	ctx          context.Context
	cancel       context.CancelFunc
	listener     func(topic string, msgs [][]byte) error
	errorhandler func(err error, consumer *BatchConsumer)
	mux          sync.Mutex
	initTopic    bool
	maxMessages  int
	timeframe    time.Duration
}

func (this *BatchConsumer) start() error {
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
		Logger:         log.New(io.Discard, "", 0),
		ErrorLogger:    log.New(io.Discard, "", 0),
		StartOffset:    kafka.LastOffset,
	})
	go func() {
		defer r.Close()
		defer slog.Info("close kafka topic consumer", "topic", this.topic)
		for {
			select {
			case <-this.ctx.Done():
				return
			default:
				batch, values, err := this.fetchBatch(r)
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					return
				}
				if err != nil {
					slog.Error("unable to fetch kafka message", "topic", this.topic, "error", err)
					this.errorhandler(err, this)
					return
				}
				if len(batch) == 0 {
					continue
				}

				err = retry(func() error {
					return this.listener(this.topic, values)
				}, func(n int64) time.Duration {
					return time.Duration(n) * time.Second
				}, 10*time.Minute)

				if err != nil {
					slog.Error("unable to handle messages (no commit)", "topic", this.topic, "count", len(batch), "error", err)
					this.errorhandler(err, this)
				} else {
					err = r.CommitMessages(this.ctx, batch...)
					if err != nil {
						slog.Error("unable to commit kafka messages", "topic", this.topic, "count", len(batch), "error", err)
					}
				}
			}
		}
	}()
	return err
}

func (this *BatchConsumer) fetchBatch(r *kafka.Reader) (batch []kafka.Message, values [][]byte, err error) {
	batchCtx, cancel := context.WithTimeout(this.ctx, this.timeframe)
	defer cancel()
	for len(batch) < this.maxMessages {
		m, err := r.FetchMessage(batchCtx)
		if err != nil {
			if this.ctx.Err() != nil {
				return batch, values, this.ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return batch, values, nil
			}
			return batch, values, err
		}
		batch = append(batch, m)
		values = append(values, m.Value)
	}
	return batch, values, nil
}
