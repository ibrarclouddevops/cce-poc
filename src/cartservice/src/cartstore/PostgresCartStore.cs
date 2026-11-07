// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

using System;
using System.Text.RegularExpressions;
using System.Threading;
using System.Threading.Tasks;
using Grpc.Core;
using Microsoft.Extensions.Configuration;
using Npgsql;

namespace cartservice.cartstore
{
    // Cart store backed by any PostgreSQL server (in-cluster Postgres, Huawei RDS, ...).
    // Configuration (environment variables):
    //   POSTGRES_HOST (required), POSTGRES_PORT (5432), POSTGRES_DB, POSTGRES_USER,
    //   POSTGRES_PASSWORD, POSTGRES_CART_TABLE (cart_items)
    // The table is created automatically on startup if it doesn't exist.
    public class PostgresCartStore : ICartStore
    {
        private static readonly Regex SafeIdentifier = new("^[a-z_][a-z0-9_]{0,62}$");

        private readonly NpgsqlDataSource dataSource;
        private readonly string table;

        public PostgresCartStore(IConfiguration configuration)
        {
            table = configuration["POSTGRES_CART_TABLE"] ?? "cart_items";
            if (!SafeIdentifier.IsMatch(table))
            {
                throw new ArgumentException($"Invalid POSTGRES_CART_TABLE name: {table}");
            }

            var builder = new NpgsqlConnectionStringBuilder
            {
                Host = configuration["POSTGRES_HOST"],
                Port = int.TryParse(configuration["POSTGRES_PORT"], out var port) ? port : 5432,
                Database = configuration["POSTGRES_DB"] ?? "boutique",
                Username = configuration["POSTGRES_USER"] ?? "boutique",
                Password = configuration["POSTGRES_PASSWORD"],
                Timeout = 10,
                GssEncryptionMode = GssEncryptionMode.Disable, // no Kerberos; avoids loading libgssapi
            };
            dataSource = NpgsqlDataSource.Create(builder.ConnectionString);

            EnsureSchema();
        }

        // Create the cart table if needed. Retries so the service can start before the database is ready.
        private void EnsureSchema()
        {
            var ddl = $@"
                CREATE TABLE IF NOT EXISTS {table} (
                    user_id    TEXT        NOT NULL,
                    product_id TEXT        NOT NULL,
                    quantity   INTEGER     NOT NULL CHECK (quantity > 0),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                    PRIMARY KEY (user_id, product_id)
                )";
            for (var attempt = 1; ; attempt++)
            {
                try
                {
                    using var cmd = dataSource.CreateCommand(ddl);
                    cmd.ExecuteNonQuery();
                    Console.WriteLine($"PostgreSQL cart store ready (table '{table}')");
                    return;
                }
                catch (Exception ex) when (attempt < 30)
                {
                    Console.WriteLine($"Waiting for PostgreSQL (attempt {attempt}/30): {ex.Message}");
                    Thread.Sleep(TimeSpan.FromSeconds(2));
                }
            }
        }

        public async Task AddItemAsync(string userId, string productId, int quantity)
        {
            Console.WriteLine($"AddItemAsync called with userId={userId}, productId={productId}, quantity={quantity}");
            try
            {
                // Atomic upsert: adds to the existing quantity if the item is already in the cart.
                await using var cmd = dataSource.CreateCommand($@"
                    INSERT INTO {table} (user_id, product_id, quantity)
                    VALUES ($1, $2, $3)
                    ON CONFLICT (user_id, product_id)
                    DO UPDATE SET quantity = {table}.quantity + EXCLUDED.quantity, updated_at = now()");
                cmd.Parameters.AddWithValue(userId);
                cmd.Parameters.AddWithValue(productId);
                cmd.Parameters.AddWithValue(quantity);
                await cmd.ExecuteNonQueryAsync();
            }
            catch (Exception ex)
            {
                throw new RpcException(
                    new Status(StatusCode.FailedPrecondition, $"Can't access cart storage. {ex}"));
            }
        }

        public async Task<Hipstershop.Cart> GetCartAsync(string userId)
        {
            Console.WriteLine($"GetCartAsync called with userId={userId}");
            var cart = new Hipstershop.Cart { UserId = userId };
            try
            {
                await using var cmd = dataSource.CreateCommand(
                    $"SELECT product_id, quantity FROM {table} WHERE user_id = $1 ORDER BY product_id");
                cmd.Parameters.AddWithValue(userId);
                await using var reader = await cmd.ExecuteReaderAsync();
                while (await reader.ReadAsync())
                {
                    cart.Items.Add(new Hipstershop.CartItem
                    {
                        ProductId = reader.GetString(0),
                        Quantity = reader.GetInt32(1),
                    });
                }
            }
            catch (Exception ex)
            {
                throw new RpcException(
                    new Status(StatusCode.FailedPrecondition, $"Can't access cart storage. {ex}"));
            }
            return cart;
        }

        public async Task EmptyCartAsync(string userId)
        {
            Console.WriteLine($"EmptyCartAsync called with userId={userId}");
            try
            {
                await using var cmd = dataSource.CreateCommand($"DELETE FROM {table} WHERE user_id = $1");
                cmd.Parameters.AddWithValue(userId);
                await cmd.ExecuteNonQueryAsync();
            }
            catch (Exception ex)
            {
                throw new RpcException(
                    new Status(StatusCode.FailedPrecondition, $"Can't access cart storage. {ex}"));
            }
        }

        // Liveness only: a brief database outage shouldn't make Kubernetes restart the cart service.
        public bool Ping()
        {
            return true;
        }
    }
}
