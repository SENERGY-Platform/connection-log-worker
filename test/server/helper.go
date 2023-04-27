/*
 * Copyright 2023 InfAI (CC SES)
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
	"errors"
	"fmt"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"io"
	"log"
	"net"
	"os"
	"time"
)

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func Dockerlog(ctx context.Context, container testcontainers.Container, name string) error {
	l, err := container.Logs(ctx)
	if err != nil {
		return err
	}
	out := &LogWriter{logger: log.New(os.Stdout, "["+name+"] ", log.LstdFlags)}
	go func() {
		_, err := io.Copy(out, l)
		if err != nil {
			log.Println("ERROR: unable to copy docker log", err)
		}
	}()
	return nil
}

type LogWriter struct {
	logger *log.Logger
}

func (this *LogWriter) Write(p []byte) (n int, err error) {
	this.logger.Print(string(p))
	return len(p), nil
}

func waitretry(timeout time.Duration, f func(ctx context.Context, target wait.StrategyTarget) error) func(ctx context.Context, target wait.StrategyTarget) error {
	return func(ctx context.Context, target wait.StrategyTarget) (err error) {
		return retry(timeout, func() error {
			return f(ctx, target)
		})
	}
}

func retry(timeout time.Duration, f func() error) (err error) {
	err = errors.New("initial")
	start := time.Now()
	for i := int64(1); err != nil && time.Since(start) < timeout; i++ {
		err = f()
		if err != nil {
			log.Println("ERROR: :", err)
			wait := time.Duration(i) * time.Second
			if time.Since(start)+wait < timeout {
				log.Println("ERROR: retry after:", wait.String())
				time.Sleep(wait)
			} else {
				time.Sleep(time.Since(start) + wait - timeout)
				return f()
			}
		}
	}
	return err
}

func Forward(ctx context.Context, fromPort int, toAddr string) error {
	log.Println("forward", fromPort, "to", toAddr)
	incoming, err := net.Listen("tcp", fmt.Sprintf(":%d", fromPort))
	if err != nil {
		return err
	}
	go func() {
		defer log.Println("closed forward incoming")
		<-ctx.Done()
		incoming.Close()
	}()
	go func() {
		for {
			client, err := incoming.Accept()
			if err != nil {
				log.Println("FORWARD ERROR:", err)
				return
			}
			go handleForwardClient(client, toAddr)
		}
	}()
	return nil
}

func handleForwardClient(client net.Conn, addr string) {
	//log.Println("new forward client")
	target, err := net.Dial("tcp", addr)
	if err != nil {
		log.Println("FORWARD ERROR:", err)
		return
	}
	go func() {
		defer target.Close()
		defer client.Close()
		io.Copy(target, client)
	}()
	go func() {
		defer target.Close()
		defer client.Close()
		io.Copy(client, target)
	}()
}
