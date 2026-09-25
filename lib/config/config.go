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

package config

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	struct_logger "github.com/SENERGY-Platform/go-service-base/struct-logger"
)

type Config struct {
	MongoUrl                                string `config:"secret"`
	MongoUser                               string
	MongoPassword                           string `config:"secret"`
	MongoAuthSource                         string
	MongoDatabase                           string
	DeviceStateCollection                   string
	HubStateCollection                      string
	DeviceOfflineNotificationInfoCollection string

	NotificationUrl string

	InfluxdbUrl     string
	InfluxdbDb      string
	InfluxdbUser    string `config:"secret"`
	InfluxdbPw      string `config:"secret"`
	InfluxdbTimeout int64

	DeviceLogTopic string
	HubLogTopic    string
	DeviceTopic    string
	HubTopic       string

	KafkaUrl           string
	KafkaGroupId       string
	KafkaMaxMessages   int
	KafkaMessageWindow int64
	Debug              bool

	BatchMode bool

	RoundTime string

	DeviceRepositoryUrl string

	ApiDocsProviderBaseUrl string

	InitTopics bool

	LogLevel string
	logger   *slog.Logger `json:"-"`
}

// loads config from json in location and used environment variables (e.g KafkaUrl --> ZOOKEEPER_URL)
func Load(location string) (config Config, err error) {
	file, error := os.Open(location)
	if error != nil {
		log.Println("error on config load: ", error)
		return config, error
	}
	config = Config{
		MongoUrl:        "mongodb://localhost:27017",
		MongoAuthSource: "admin",
		MongoDatabase:   "connection_log",
	}
	decoder := json.NewDecoder(file)
	error = decoder.Decode(&config)
	if error != nil {
		log.Println("invalid config json: ", error)
		return config, error
	}
	handleEnvironmentVars(&config)
	config.GetLogger()
	return config, nil
}

func isSecret(field reflect.StructField) bool {
	return strings.Contains(field.Tag.Get("config"), "secret")
}

// plainConfig has none of Config's methods, so formatting it does not recurse.
type plainConfig Config

// masked returns a copy in which every non-empty field tagged config:"secret" is replaced.
func (this Config) masked() plainConfig {
	v := reflect.ValueOf(&this).Elem()
	for i := 0; i < v.NumField(); i++ {
		if isSecret(v.Type().Field(i)) && v.Field(i).Kind() == reflect.String && v.Field(i).String() != "" {
			v.Field(i).SetString("***")
		}
	}
	return plainConfig(this)
}

func (this Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(this.masked())
}

func (this Config) String() string {
	return fmt.Sprintf("%+v", this.masked())
}

func (this Config) GoString() string {
	return fmt.Sprintf("%#v", this.masked())
}

var camel = regexp.MustCompile("(^[^A-Z]*|[A-Z]*)([A-Z][^A-Z]+|$)")

func fieldNameToEnvName(s string) string {
	var a []string
	for _, sub := range camel.FindAllStringSubmatch(s, -1) {
		if sub[1] != "" {
			a = append(a, sub[1])
		}
		if sub[2] != "" {
			a = append(a, sub[2])
		}
	}
	return strings.ToUpper(strings.Join(a, "_"))
}

// preparations for docker
func handleEnvironmentVars(config *Config) {
	configValue := reflect.Indirect(reflect.ValueOf(config))
	configType := configValue.Type()
	for index := 0; index < configType.NumField(); index++ {
		fieldName := configType.Field(index).Name
		envName := fieldNameToEnvName(fieldName)
		envValue := os.Getenv(envName)
		if envValue != "" {
			if !isSecret(configType.Field(index)) {
				fmt.Println("use environment variable: ", envName, " = ", envValue)
			}
			if configValue.FieldByName(fieldName).Kind() == reflect.Int64 {
				i, _ := strconv.ParseInt(envValue, 10, 64)
				configValue.FieldByName(fieldName).SetInt(i)
			}
			if configValue.FieldByName(fieldName).Kind() == reflect.String {
				configValue.FieldByName(fieldName).SetString(envValue)
			}
			if configValue.FieldByName(fieldName).Kind() == reflect.Bool {
				b, _ := strconv.ParseBool(envValue)
				configValue.FieldByName(fieldName).SetBool(b)
			}
			if configValue.FieldByName(fieldName).Kind() == reflect.Float64 {
				f, _ := strconv.ParseFloat(envValue, 64)
				configValue.FieldByName(fieldName).SetFloat(f)
			}
			if configValue.FieldByName(fieldName).Kind() == reflect.Slice {
				val := []string{}
				for _, element := range strings.Split(envValue, ",") {
					val = append(val, strings.TrimSpace(element))
				}
				configValue.FieldByName(fieldName).Set(reflect.ValueOf(val))
			}
			if configValue.FieldByName(fieldName).Kind() == reflect.Map {
				value := map[string]string{}
				for _, element := range strings.Split(envValue, ",") {
					keyVal := strings.Split(element, ":")
					key := strings.TrimSpace(keyVal[0])
					val := strings.TrimSpace(keyVal[1])
					value[key] = val
				}
				configValue.FieldByName(fieldName).Set(reflect.ValueOf(value))
			}
		}
	}
}

func (this *Config) GetLogger() *slog.Logger {
	if this.logger == nil {
		if this.Debug {
			this.LogLevel = "debug"
		}
		info, ok := debug.ReadBuildInfo()
		project := ""
		org := ""
		if ok {
			if parts := strings.Split(info.Main.Path, "/"); len(parts) > 2 {
				project = strings.Join(parts[2:], "/")
				org = strings.Join(parts[:2], "/")
			}
		}
		this.logger = struct_logger.New(
			struct_logger.Config{
				Handler:    struct_logger.JsonHandlerSelector,
				Level:      this.LogLevel,
				TimeFormat: time.RFC3339Nano,
				TimeUtc:    true,
				AddMeta:    true,
			},
			os.Stdout,
			org,
			project,
		)
		slog.SetDefault(this.logger)
		slog.SetLogLoggerLevel(slog.LevelInfo)
	}
	return this.logger
}
