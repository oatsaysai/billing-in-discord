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
	log.Println("Starting database migration...")

	// Schema for users table
	usersSchema := `
    CREATE TABLE IF NOT EXISTS users (
        id SERIAL PRIMARY KEY,
        discord_id VARCHAR(50) NOT NULL UNIQUE,
        created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP -- Added created_at
    );
    CREATE INDEX IF NOT EXISTS idx_users_discord_id ON users(discord_id);`
	_, err := dbPool.Exec(context.Background(), usersSchema)
	if err != nil {
		log.Fatalf("Failed to migrate users table: %v", err)
	}

	// Schema for transactions table
	transactionsSchema := `
    CREATE TABLE IF NOT EXISTS transactions (
        id SERIAL PRIMARY KEY,
        payer_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Added ON DELETE CASCADE
        payee_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Added ON DELETE CASCADE
        amount NUMERIC(10, 2) NOT NULL,
       	description TEXT,
		already_paid BOOLEAN DEFAULT FALSE,
        created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP, -- Changed default to CURRENT_TIMESTAMP
        paid_at TIMESTAMPTZ -- Added paid_at for when it's marked paid
    );
    CREATE INDEX IF NOT EXISTS idx_transactions_payer_id ON transactions(payer_id);
    CREATE INDEX IF NOT EXISTS idx_transactions_payee_id ON transactions(payee_id);
    CREATE INDEX IF NOT EXISTS idx_transactions_payer_payee_paid ON transactions(payer_id, payee_id, already_paid); -- More specific index
    `
	_, err = dbPool.Exec(context.Background(), transactionsSchema)
	if err != nil {
		log.Fatalf("Failed to migrate transactions table: %v", err)
	}

	// Schema for user_debts table
	userDebtsSchema := `
    CREATE TABLE IF NOT EXISTS user_debts (
        -- id SERIAL PRIMARY KEY, -- Removed serial ID, composite primary key is better
        debtor_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Added ON DELETE CASCADE
        creditor_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Added ON DELETE CASCADE
        amount NUMERIC(10, 2) NOT NULL,
        created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP, -- Added created_at
        updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP, -- Changed default to CURRENT_TIMESTAMP
		PRIMARY KEY (debtor_id, creditor_id) -- Using composite primary key
    );
    CREATE INDEX IF NOT EXISTS idx_user_debts_debtor_id ON user_debts(debtor_id);
    CREATE INDEX IF NOT EXISTS idx_user_debts_creditor_id ON user_debts(creditor_id);
    `
	_, err = dbPool.Exec(context.Background(), userDebtsSchema)
	if err != nil {
		log.Fatalf("Failed to migrate user_debts table: %v", err)
	}

	// Schema for firebase_sites table (NEW)
	firebaseSitesSchema := `
	CREATE TABLE IF NOT EXISTS firebase_sites (
		id SERIAL PRIMARY KEY,
		user_db_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		firebase_project_id TEXT NOT NULL,
		site_name TEXT NOT NULL UNIQUE, 
		site_url TEXT NOT NULL,        
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		status TEXT NOT NULL DEFAULT 'active' -- e.g., 'active', 'disabled'
	);
	CREATE INDEX IF NOT EXISTS idx_firebase_sites_user_db_id_status ON firebase_sites(user_db_id, status);
	CREATE INDEX IF NOT EXISTS idx_firebase_sites_site_name ON firebase_sites(site_name);
	`
	_, err = dbPool.Exec(context.Background(), firebaseSitesSchema)
	if err != nil {
		log.Fatalf("Failed to migrate firebase_sites table: %v", err)
	}

	// Trigger function to update 'updated_at' timestamp
	triggerFunction := `
    CREATE OR REPLACE FUNCTION update_modified_column()
    RETURNS TRIGGER AS $$
    BEGIN
       NEW.updated_at = NOW(); -- Or CURRENT_TIMESTAMP
       RETURN NEW;
    END;
    $$ language 'plpgsql';`
	_, err = dbPool.Exec(context.Background(), triggerFunction)
	if err != nil {
		log.Fatalf("Failed to create/update trigger function 'update_modified_column': %v", err)
	}

	// Apply trigger to user_debts
	userDebtsTrigger := `
    DROP TRIGGER IF EXISTS update_user_debts_modtime ON user_debts; -- idempotent
    CREATE TRIGGER update_user_debts_modtime
    BEFORE UPDATE ON user_debts
    FOR EACH ROW
    EXECUTE FUNCTION update_modified_column();`
	_, err = dbPool.Exec(context.Background(), userDebtsTrigger)
	if err != nil {
		log.Fatalf("Failed to apply trigger to user_debts: %v", err)
	}

	// Apply trigger to firebase_sites (NEW)
	firebaseSitesTrigger := `
    DROP TRIGGER IF EXISTS update_firebase_sites_modtime ON firebase_sites; -- idempotent
    CREATE TRIGGER update_firebase_sites_modtime
    BEFORE UPDATE ON firebase_sites
    FOR EACH ROW
    EXECUTE FUNCTION update_modified_column();`
	_, err = dbPool.Exec(context.Background(), firebaseSitesTrigger)
	if err != nil {
		log.Fatalf("Failed to apply trigger to firebase_sites: %v", err)
	}

	// paid_at to transactions table
	addPaidAt := `
		ALTER TABLE transactions
		ADD COLUMN IF NOT EXISTS paid_at TIMESTAMPTZ DEFAULT NULL; -- Added paid_at column
	`
	_, err = dbPool.Exec(context.Background(), addPaidAt)
	if err != nil {
		log.Fatalf("Failed to add paid_at column to transactions table: %v", err)
	}

	log.Println("Database migration completed successfully")
}

// updateUserDebt updates the summary user_debts table.
// debtorDbID and creditorDbID are the integer IDs from the 'users' table.
func updateUserDebt(debtorDbID, creditorDbID int, amount float64) error {
	query := `
        INSERT INTO user_debts (debtor_id, creditor_id, amount, created_at, updated_at)
        VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
        ON CONFLICT (debtor_id, creditor_id)
        DO UPDATE SET amount = user_debts.amount + EXCLUDED.amount, updated_at = CURRENT_TIMESTAMP;
    `
	_, err := dbPool.Exec(context.Background(), query, debtorDbID, creditorDbID, amount)
	if err != nil {
		log.Printf("Error updating user_debts for debtor %d, creditor %d, amount %.2f: %v", debtorDbID, creditorDbID, amount, err)
		return fmt.Errorf("failed to update user_debts: %w", err)
	}
	return nil
}
