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
	"testing"

	"github.com/segmentio/kafka-go"
)

func TestGroupReaderConfigWatchesPartitionChanges(t *testing.T) {
	config := groupReaderConfig([]string{"broker:9092"}, "group", "topic")
	if !config.WatchPartitionChanges {
		t.Fatal("a reader of the shared group must watch its topic, or a missing topic leaves the whole group without partitions until a restart")
	}
	if config.GroupID != "group" || config.Topic != "topic" || len(config.Brokers) != 1 || config.Brokers[0] != "broker:9092" {
		t.Fatalf("unexpected reader config: %#v", config)
	}
	if config.CommitInterval != 0 {
		t.Fatal("commits must stay synchronous")
	}
	if config.StartOffset != 0 && config.StartOffset != kafka.FirstOffset {
		t.Fatal("the shared config must not set a start offset; the batch reader sets its own")
	}
}
