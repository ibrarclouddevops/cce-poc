// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/productcatalogservice/genproto"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var safeIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadCatalogFromPostgres reads the catalog from any PostgreSQL server
// (in-cluster Postgres, Huawei RDS, ...). Configuration (environment variables):
//
//	POSTGRES_HOST (required), POSTGRES_PORT (5432), POSTGRES_DB, POSTGRES_USER,
//	POSTGRES_PASSWORD, POSTGRES_PRODUCTS_TABLE (products)
//
// The table is created automatically and, when empty, filled from products.json.
func loadCatalogFromPostgres(catalog *pb.ListProductsResponse) error {
	table := getenvDefault("POSTGRES_PRODUCTS_TABLE", "products")
	if !safeIdentifier.MatchString(table) {
		return fmt.Errorf("invalid POSTGRES_PRODUCTS_TABLE name: %q", table)
	}

	config, err := pgxpool.ParseConfig("")
	if err != nil {
		return err
	}
	config.ConnConfig.Host = os.Getenv("POSTGRES_HOST")
	port, err := strconv.ParseUint(getenvDefault("POSTGRES_PORT", "5432"), 10, 16)
	if err != nil {
		return fmt.Errorf("invalid POSTGRES_PORT: %v", err)
	}
	config.ConnConfig.Port = uint16(port)
	config.ConnConfig.Database = getenvDefault("POSTGRES_DB", "boutique")
	config.ConnConfig.User = getenvDefault("POSTGRES_USER", "boutique")
	config.ConnConfig.Password = os.Getenv("POSTGRES_PASSWORD")
	config.ConnConfig.ConnectTimeout = 10 * time.Second
	config.MaxConns = 2

	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to set up PostgreSQL pool: %v", err)
	}
	defer pool.Close()

	// Wait for the database: the service may start before PostgreSQL is ready.
	for attempt := 1; ; attempt++ {
		if err = pool.Ping(ctx); err == nil {
			break
		}
		if attempt == 30 {
			return fmt.Errorf("PostgreSQL not reachable: %v", err)
		}
		log.Infof("waiting for PostgreSQL (attempt %d/30): %v", attempt, err)
		time.Sleep(2 * time.Second)
	}

	if err := ensureProductsTable(ctx, pool, table); err != nil {
		return err
	}

	rows, err := pool.Query(ctx, "SELECT id, name, description, picture, price_usd_currency_code, "+
		"price_usd_units, price_usd_nanos, categories FROM "+table+" ORDER BY id")
	if err != nil {
		return fmt.Errorf("failed to query products: %v", err)
	}
	defer rows.Close()

	catalog.Products = catalog.Products[:0]
	for rows.Next() {
		product := &pb.Product{PriceUsd: &pb.Money{}}
		var categories string
		if err := rows.Scan(&product.Id, &product.Name, &product.Description, &product.Picture,
			&product.PriceUsd.CurrencyCode, &product.PriceUsd.Units, &product.PriceUsd.Nanos, &categories); err != nil {
			return fmt.Errorf("failed to read product row: %v", err)
		}
		if categories != "" {
			product.Categories = strings.Split(strings.ToLower(categories), ",")
		}
		catalog.Products = append(catalog.Products, product)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	log.Infof("loaded %d products from PostgreSQL table %q", len(catalog.Products), table)
	return nil
}

// ensureProductsTable creates the products table and seeds it from products.json the first time.
func ensureProductsTable(ctx context.Context, pool *pgxpool.Pool, table string) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (
		id                      TEXT PRIMARY KEY,
		name                    TEXT    NOT NULL,
		description             TEXT    NOT NULL DEFAULT '',
		picture                 TEXT    NOT NULL DEFAULT '',
		price_usd_currency_code TEXT    NOT NULL DEFAULT 'USD',
		price_usd_units         BIGINT  NOT NULL DEFAULT 0,
		price_usd_nanos         INTEGER NOT NULL DEFAULT 0,
		categories              TEXT    NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("failed to create table %q: %v", table, err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	seed := &pb.ListProductsResponse{}
	if err := loadCatalogFromLocalFile(seed); err != nil {
		return fmt.Errorf("failed to read products.json for seeding: %v", err)
	}
	batch := &pgx.Batch{}
	for _, p := range seed.Products {
		price := p.GetPriceUsd()
		batch.Queue(`INSERT INTO `+table+` (id, name, description, picture, price_usd_currency_code,
			price_usd_units, price_usd_nanos, categories) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (id) DO NOTHING`,
			p.Id, p.Name, p.Description, p.Picture, price.GetCurrencyCode(),
			price.GetUnits(), price.GetNanos(), strings.Join(p.Categories, ","))
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("failed to seed products: %v", err)
	}
	log.Infof("seeded %d products into PostgreSQL table %q from products.json", len(seed.Products), table)
	return nil
}
