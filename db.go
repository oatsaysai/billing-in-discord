package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/viper"
)

var dbPool *pgxpool.Pool

func initPostgresPool() {

	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable search_path=%s",
		viper.GetString("PostgreSQL.Host"),
		viper.GetString("PostgreSQL.Port"),
		viper.GetString("PostgreSQL.User"),
		viper.GetString("PostgreSQL.Password"),
		viper.GetString("PostgreSQL.DBName"),
		viper.GetString("PostgreSQL.Schema"),
	)

	var connectConf, err = pgxpool.ParseConfig(connStr)
	if err != nil {
		log.Fatalf("Unable to parse PostgreSQL config: %v", err)
	}

	connectConf.MaxConns = int32(viper.GetInt("PostgreSQL.PoolMaxConns"))
	connectConf.HealthCheckPeriod = 15 * time.Second
	connectConf.ConnConfig.ConnectTimeout = 5 * time.Second

	// Set timezone to PGX runtime
	if s := os.Getenv("TZ"); s != "" {
		connectConf.ConnConfig.RuntimeParams["timezone"] = s
	}

	dbPool, err = pgxpool.NewWithConfig(context.Background(), connectConf)
	if err != nil {
		log.Fatalf("Unable to create PostgreSQL connection pool: %v", err)
	}

	log.Println("Connected to PostgreSQL successfully")
}

func migrateDatabase() {
	query := `
    CREATE TABLE IF NOT EXISTS users (
        id SERIAL PRIMARY KEY,
        discord_id VARCHAR(50) NOT NULL UNIQUE
    );

    CREATE TABLE IF NOT EXISTS transactions (
        id SERIAL PRIMARY KEY,
        payer_id INT NOT NULL REFERENCES users(id),
        payee_id INT NOT NULL REFERENCES users(id),
        amount NUMERIC(10, 2) NOT NULL,
       	description TEXT,
		already_paid BOOLEAN DEFAULT FALSE,
        created_at TIMESTAMPTZ DEFAULT NOW()
    );

    CREATE TABLE IF NOT EXISTS user_debts (
        id SERIAL PRIMARY KEY,
        debtor_id INT NOT NULL REFERENCES users(id),
        creditor_id INT NOT NULL REFERENCES users(id),
        amount NUMERIC(10, 2) NOT NULL,
        updated_at TIMESTAMPTZ DEFAULT NOW(),
		UNIQUE (debtor_id, creditor_id)
    );

	-- Add indexes for frequently queried fields
	CREATE INDEX IF NOT EXISTS idx_users_discord_id ON users(discord_id);
    CREATE INDEX IF NOT EXISTS idx_transactions_payer_id ON transactions(payer_id);
    CREATE INDEX IF NOT EXISTS idx_transactions_payee_id ON transactions(payee_id);
    CREATE INDEX IF NOT EXISTS idx_user_debts_debtor_id ON user_debts(debtor_id);
    CREATE INDEX IF NOT EXISTS idx_user_debts_creditor_id ON user_debts(creditor_id);
    `

	_, err := dbPool.Exec(context.Background(), query)
	if err != nil {
		log.Fatalf("Failed to run database migration: %v", err)
	}

	log.Println("Database migration completed successfully")
}

func updateUserDebt(debtorID, creditorID int, amount float64) error {
	_, err := dbPool.Exec(
		context.Background(),
		`INSERT INTO user_debts (debtor_id, creditor_id, amount)
         VALUES ($1, $2, $3)
         ON CONFLICT (debtor_id, creditor_id)
         DO UPDATE SET amount = user_debts.amount + $3, updated_at = CURRENT_TIMESTAMP`,
		debtorID, creditorID, amount,
	)
	return err
}
