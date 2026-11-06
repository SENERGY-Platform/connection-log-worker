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
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The tables are the contract with the connection-log service, which reads them.
const (
	deviceTable  = "device"
	gatewayTable = "gateway"
)

// schemaLockKey serializes the schema setup of concurrently starting workers (pg_advisory_xact_lock).
const schemaLockKey int64 = 5053_0001

// newPostgresPool connects to the history database and verifies the connection.
func newPostgresPool(ctx context.Context, conf config.Config) (*pgxpool.Pool, error) {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(conf.PostgresUser, conf.PostgresPw),
		Host:   net.JoinHostPort(conf.PostgresHost, strconv.FormatInt(conf.PostgresPort, 10)),
		Path:   "/" + conf.PostgresDb,
	}
	poolConf, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		// the parse error may echo the connection string, which contains the password
		return nil, errors.New("invalid postgres connection settings")
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConf)
	if err != nil {
		return nil, fmt.Errorf("unable to create postgres pool for %s:%d/%s: %w", conf.PostgresHost, conf.PostgresPort, conf.PostgresDb, err)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("unable to connect to postgres %s:%d/%s as %s: %w", conf.PostgresHost, conf.PostgresPort, conf.PostgresDb, conf.PostgresUser, err)
	}
	return pool, nil
}

// ensureSchema creates the history tables, hypertables and columnstore policies if they are missing.
// It is idempotent. The database, the role and the timescaledb extension are provisioned outside the service.
func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", schemaLockKey); err != nil {
		return err
	}
	var hasExtension bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')").Scan(&hasExtension); err != nil {
		return fmt.Errorf("unable to check for the timescaledb extension: %w", err)
	}
	if !hasExtension {
		return errors.New("the timescaledb extension is not installed in the history database; it must be provisioned outside the service (CREATE EXTENSION timescaledb)")
	}
	for _, table := range []string{deviceTable, gatewayTable} {
		if err = ensureTable(ctx, tx, table); err != nil {
			return fmt.Errorf("unable to set up table %s: %w", table, err)
		}
	}
	return tx.Commit(ctx)
}

// ensureTable only receives the table constants; they are formatted into the statements because DDL takes no parameters.
func ensureTable(ctx context.Context, tx pgx.Tx, table string) error {
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id        TEXT        NOT NULL,
    time      TIMESTAMPTZ NOT NULL,
    connected BOOLEAN     NOT NULL,
    PRIMARY KEY (id, time)
)`, table),
		fmt.Sprintf(`SELECT create_hypertable('%s', by_range('time'), if_not_exists => TRUE)`, table),
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	// ALTER TABLE ... SET fails once the table has compressed chunks, so it only runs while the columnstore is off
	var enabled bool
	err := tx.QueryRow(ctx,
		`SELECT compression_enabled FROM timescaledb_information.hypertables WHERE hypertable_schema = current_schema() AND hypertable_name = $1`,
		table).Scan(&enabled)
	if err != nil {
		return fmt.Errorf("unable to read the compression state: %w", err)
	}
	if !enabled {
		_, err = tx.Exec(ctx, fmt.Sprintf(
			`ALTER TABLE %s SET (timescaledb.enable_columnstore = true, timescaledb.segmentby = 'id', timescaledb.orderby = '"time" DESC')`, table))
		if err != nil {
			return fmt.Errorf("unable to enable the columnstore: %w", err)
		}
	}
	_, err = tx.Exec(ctx, fmt.Sprintf(`CALL add_columnstore_policy('%s', after => INTERVAL '30 days', if_not_exists => true)`, table))
	if err != nil {
		return fmt.Errorf("unable to add the columnstore policy: %w", err)
	}
	return nil
}
