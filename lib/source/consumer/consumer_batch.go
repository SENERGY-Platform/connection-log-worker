package consumer

import (
	"context"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/consumer/listener_batch"
)

func StartBatch(
	ctx context.Context,
	config config.Config,
	controller listener_batch.Controller,
	runtimeErrorHandler func(err error, consumer *BatchConsumer),
) (err error) {
	for _, factory := range listener_batch.Factories {
		topic, handler, err := factory(config, controller)
		if err != nil {
			config.GetLogger().Error("unable to create listener", "topic", topic, "error", err)
			return err
		}
		err = RunBatchConsumer(
			ctx,
			config.KafkaUrl,
			config.KafkaGroupId,
			topic,
			config.InitTopics,
			config.KafkaMaxMessages,
			time.Duration(config.KafkaMessageWindow)*time.Second,
			func(topic string, messages [][]byte) error {
				for _, message := range messages {
					config.GetLogger().Debug("consume", "topic", topic, "msg", string(message))
				}
				return handler(messages)
			},
			runtimeErrorHandler,
		)
		if err != nil {
			return err
		}
	}
	return err
}
