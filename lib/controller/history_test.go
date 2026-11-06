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

package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newHistoryController(t *testing.T) *Controller {
	t.Helper()
	wg := &sync.WaitGroup{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	var conf config.Config
	if err := server.StartTimescale(ctx, wg, &conf); err != nil {
		t.Fatal(err)
	}
	pool, err := newPostgresPool(ctx, conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = ensureSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return &Controller{config: conf, db: pool}
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string, id string) (n int) {
	t.Helper()
	if table != deviceTable && table != gatewayTable {
		t.Fatal("unknown table", table)
	}
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE id = $1", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countPolicies(t *testing.T, pool *pgxpool.Pool, table string) (n int) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM timescaledb_information.jobs WHERE hypertable_name = $1 AND proc_name = 'policy_compression'`, table).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConnectionHistory(t *testing.T) {
	c := newHistoryController(t)
	ctx := context.Background()
	// microsecond resolution is what TIMESTAMPTZ stores; it must not be cut to seconds
	now := time.Now().UTC().Truncate(time.Second).Add(123456 * time.Microsecond)

	t.Run("a redelivered message creates no second row", func(t *testing.T) {
		d := model.DeviceLog{Id: "dev-dup", Connected: true, Time: now}
		g := model.HubLog{Id: "gw-dup", Connected: true, Time: now}
		for i := 0; i < 2; i++ {
			if err := c.logDeviceHistory(d); err != nil {
				t.Fatal(err)
			}
			if err := c.logGatewayHistory(g); err != nil {
				t.Fatal(err)
			}
			if err := c.writeDeviceLogs([]model.DeviceLog{d, d}); err != nil {
				t.Fatal(err)
			}
			if err := c.writeHubLogs([]model.HubLog{g, g}); err != nil {
				t.Fatal(err)
			}
		}
		if n := countRows(t, c.db, deviceTable, "dev-dup"); n != 1 {
			t.Errorf("device rows = %d, want 1", n)
		}
		if n := countRows(t, c.db, gatewayTable, "gw-dup"); n != 1 {
			t.Errorf("gateway rows = %d, want 1", n)
		}
		var stored time.Time
		var connected bool
		if err := c.db.QueryRow(ctx, "SELECT time, connected FROM device WHERE id = 'dev-dup'").Scan(&stored, &connected); err != nil {
			t.Fatal(err)
		}
		if !stored.Equal(now) || !connected {
			t.Errorf("stored (%s, %v), want (%s, true)", stored, connected, now)
		}
	})

	t.Run("states of one id at different times are kept", func(t *testing.T) {
		if err := c.writeDeviceLogs([]model.DeviceLog{
			{Id: "dev-flip", Connected: true, Time: now},
			{Id: "dev-flip", Connected: false, Time: now.Add(time.Millisecond)},
		}); err != nil {
			t.Fatal(err)
		}
		if n := countRows(t, c.db, deviceTable, "dev-flip"); n != 2 {
			t.Errorf("rows = %d, want 2", n)
		}
	})

	t.Run("empty batches are no-ops", func(t *testing.T) {
		if err := c.writeDeviceLogs(nil); err != nil {
			t.Error(err)
		}
		if err := c.writeHubLogs(nil); err != nil {
			t.Error(err)
		}
		if err := c.deleteDeviceLogs(nil); err != nil {
			t.Error(err)
		}
		if err := c.deleteGatewayLogs(nil); err != nil {
			t.Error(err)
		}
	})

	t.Run("delete removes all rows of an id and no others", func(t *testing.T) {
		for _, id := range []string{"dev-del-a", "dev-del-b", "dev-keep"} {
			for i := 0; i < 3; i++ {
				if err := c.logDeviceHistory(model.DeviceLog{Id: id, Connected: i%2 == 0, Time: now.Add(time.Duration(i) * time.Second)}); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, id := range []string{"gw-del-a", "gw-del-b", "gw-keep"} {
			for i := 0; i < 3; i++ {
				if err := c.logGatewayHistory(model.HubLog{Id: id, Connected: i%2 == 0, Time: now.Add(time.Duration(i) * time.Second)}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := c.deleteDeviceLog("dev-del-a"); err != nil {
			t.Fatal(err)
		}
		if err := c.deleteGatewayLog("gw-del-a"); err != nil {
			t.Fatal(err)
		}
		if err := c.deleteDeviceLogs([]string{"dev-del-b", "unknown"}); err != nil {
			t.Fatal(err)
		}
		if err := c.deleteGatewayLogs([]string{"gw-del-b", "unknown"}); err != nil {
			t.Fatal(err)
		}
		for id, want := range map[string]int{"dev-del-a": 0, "dev-del-b": 0, "dev-keep": 3} {
			if n := countRows(t, c.db, deviceTable, id); n != want {
				t.Errorf("device %s rows = %d, want %d", id, n, want)
			}
		}
		for id, want := range map[string]int{"gw-del-a": 0, "gw-del-b": 0, "gw-keep": 3} {
			if n := countRows(t, c.db, gatewayTable, id); n != want {
				t.Errorf("gateway %s rows = %d, want %d", id, n, want)
			}
		}
	})

	t.Run("ids with SQL metacharacters are data", func(t *testing.T) {
		evil := `x' OR '1'='1"; DROP TABLE device; --`
		if err := c.logDeviceHistory(model.DeviceLog{Id: evil, Connected: true, Time: now}); err != nil {
			t.Fatal(err)
		}
		if n := countRows(t, c.db, deviceTable, evil); n != 1 {
			t.Errorf("rows of the evil id = %d, want 1", n)
		}
		if err := c.deleteDeviceLog(evil); err != nil {
			t.Fatal(err)
		}
		if n := countRows(t, c.db, deviceTable, evil); n != 0 {
			t.Errorf("rows of the evil id after delete = %d, want 0", n)
		}
		if n := countRows(t, c.db, deviceTable, "dev-keep"); n != 3 {
			t.Errorf("dev-keep rows = %d, want 3", n)
		}
	})
}

func TestEnsureSchemaIsIdempotent(t *testing.T) {
	c := newHistoryController(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-90 * 24 * time.Hour).Truncate(time.Microsecond)

	if err := ensureSchema(ctx, c.db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, table := range []string{deviceTable, gatewayTable} {
		if n := countPolicies(t, c.db, table); n != 1 {
			t.Errorf("%s has %d columnstore policies, want 1", table, n)
		}
		var segmentby string
		err := c.db.QueryRow(ctx,
			`SELECT coalesce(segmentby, '') FROM timescaledb_information.hypertable_columnstore_settings WHERE hypertable::text = $1`, table).Scan(&segmentby)
		if err != nil {
			t.Fatalf("columnstore settings of %s: %v", table, err)
		}
		if segmentby != "id" {
			t.Errorf("%s segmentby = %q, want id", table, segmentby)
		}
	}

	d := model.DeviceLog{Id: "dev-compressed", Connected: true, Time: old}
	if err := c.logDeviceHistory(d); err != nil {
		t.Fatal(err)
	}
	var compressed int
	if err := c.db.QueryRow(ctx, `SELECT count(compress_chunk(c)) FROM show_chunks('device') c`).Scan(&compressed); err != nil {
		t.Fatal(err)
	}
	if compressed == 0 {
		t.Fatal("no chunk was compressed")
	}

	if err := ensureSchema(ctx, c.db); err != nil {
		t.Fatalf("run with compressed chunks: %v", err)
	}
	if n := countPolicies(t, c.db, deviceTable); n != 1 {
		t.Errorf("device has %d columnstore policies after the third run, want 1", n)
	}

	// the redelivery of a row that sits in a compressed chunk must stay a no-op, and delete must reach it
	if err := c.logDeviceHistory(d); err != nil {
		t.Fatalf("redelivery into a compressed chunk: %v", err)
	}
	if n := countRows(t, c.db, deviceTable, "dev-compressed"); n != 1 {
		t.Errorf("rows after redelivery = %d, want 1", n)
	}
	if err := c.deleteDeviceLog("dev-compressed"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, c.db, deviceTable, "dev-compressed"); n != 0 {
		t.Errorf("rows after delete = %d, want 0", n)
	}
}
