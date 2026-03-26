package db

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/viper"
)

// Pool represents a connection pool to the PostgreSQL database
var Pool *pgxpool.Pool

// Initialize creates and initializes the PostgreSQL connection pool
func Initialize() {
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

	Pool, err = pgxpool.NewWithConfig(context.Background(), connectConf)
	if err != nil {
		log.Fatalf("Unable to create PostgreSQL connection pool: %v", err)
	}

	log.Println("Connected to PostgreSQL successfully")
}

// Migrate sets up the database schema
func Migrate() {
	log.Println("Starting database migration...")

	// Schema for users table
	usersSchema := `
    CREATE TABLE IF NOT EXISTS users (
        id SERIAL PRIMARY KEY,
        discord_id VARCHAR(50) NOT NULL UNIQUE,
        created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP -- Added created_at
    );
    CREATE INDEX IF NOT EXISTS idx_users_discord_id ON users(discord_id);`
	_, err := Pool.Exec(context.Background(), usersSchema)
	if err != nil {
		log.Fatalf("Failed to migrate users table: %v", err)
	}

	// Add created_at column to users table if it doesn't exist
	addCreatedAt := `
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP;
	`
	_, err = Pool.Exec(context.Background(), addCreatedAt)
	if err != nil {
		log.Fatalf("Failed to add created_at column to users table: %v", err)
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
	_, err = Pool.Exec(context.Background(), transactionsSchema)
	if err != nil {
		log.Fatalf("Failed to migrate transactions table: %v", err)
	}

	// Add paid_at column to transactions table if it doesn't exist
	addPaidAt := `
		ALTER TABLE transactions
		ADD COLUMN IF NOT EXISTS paid_at TIMESTAMPTZ DEFAULT NULL; -- Added paid_at column
	`
	_, err = Pool.Exec(context.Background(), addPaidAt)
	if err != nil {
		log.Fatalf("Failed to add paid_at column to transactions table: %v", err)
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
	_, err = Pool.Exec(context.Background(), userDebtsSchema)
	if err != nil {
		log.Fatalf("Failed to migrate user_debts table: %v", err)
	}

	// Add created_at column to user_debts table if they don't exist
	addCreatedAtUserDebts := `
		ALTER TABLE user_debts
		ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP;
	`
	_, err = Pool.Exec(context.Background(), addCreatedAtUserDebts)
	if err != nil {
		log.Fatalf("Failed to add created_at column to user_debts table: %v", err)
	}

	// Schema for user_promptpay table
	userPromptPaySchema := `
	CREATE TABLE IF NOT EXISTS user_promptpay (
		user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		promptpay_id VARCHAR(50) NOT NULL,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id)
	);
	CREATE INDEX IF NOT EXISTS idx_user_promptpay_user_id ON user_promptpay(user_id);
	`
	_, err = Pool.Exec(context.Background(), userPromptPaySchema)
	if err != nil {
		log.Fatalf("Failed to migrate user_promptpay table: %v", err)
	}

	// Apply trigger to user_promptpay
	userPromptPayTrigger := `
	DROP TRIGGER IF EXISTS update_user_promptpay_modtime ON user_promptpay;
	CREATE TRIGGER update_user_promptpay_modtime
	BEFORE UPDATE ON user_promptpay
	FOR EACH ROW
	EXECUTE FUNCTION update_modified_column();`
	_, err = Pool.Exec(context.Background(), userPromptPayTrigger)
	if err != nil {
		log.Fatalf("Failed to apply trigger to user_promptpay: %v", err)
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
		status TEXT NOT NULL DEFAULT 'active' -- e.g., 'active', 'inactive'
	);
	CREATE INDEX IF NOT EXISTS idx_firebase_sites_user_db_id_status ON firebase_sites(user_db_id, status);
	CREATE INDEX IF NOT EXISTS idx_firebase_sites_site_name ON firebase_sites(site_name);
	`
	_, err = Pool.Exec(context.Background(), firebaseSitesSchema)
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
	_, err = Pool.Exec(context.Background(), triggerFunction)
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
	_, err = Pool.Exec(context.Background(), userDebtsTrigger)
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
	_, err = Pool.Exec(context.Background(), firebaseSitesTrigger)
	if err != nil {
		log.Fatalf("Failed to apply trigger to firebase_sites: %v", err)
	}

	// Check if site_token column exists in firebase_sites table
	var columnExists bool
	err = Pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_name = 'firebase_sites' AND column_name = 'site_token'
		)
	`).Scan(&columnExists)

	if err != nil {
		log.Printf("Error checking if site_token column exists: %v", err)
		// Continue anyway, we'll try to add the column
	}

	// Add site_token column to existing firebase_sites table if it doesn't exist
	if !columnExists {
		addSiteTokenColumn := `
			ALTER TABLE firebase_sites
			ADD COLUMN site_token TEXT;
		`
		_, err = Pool.Exec(context.Background(), addSiteTokenColumn)
		if err != nil {
			log.Fatalf("Failed to add site_token column to firebase_sites table: %v", err)
		}
		log.Println("Added site_token column to firebase_sites table")
	}

	// Create index for site_token if needed
	var indexExists bool
	err = Pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM pg_indexes
			WHERE tablename = 'firebase_sites' AND indexname = 'idx_firebase_sites_site_token'
		)
	`).Scan(&indexExists)

	if err != nil {
		log.Printf("Error checking if site_token index exists: %v", err)
		// Continue anyway, we'll try to create the index
	}

	if !indexExists {
		addSiteTokenIndex := `
			CREATE INDEX idx_firebase_sites_site_token ON firebase_sites(site_token);
		`
		_, err = Pool.Exec(context.Background(), addSiteTokenIndex)
		if err != nil {
			log.Fatalf("Failed to create index on site_token column: %v", err)
		}
		log.Println("Created index on site_token column")
	}

	log.Println("Database migration completed successfully")
}

// GetOrCreateUser retrieves a user from the database by Discord ID or creates a new one
func GetOrCreateUser(discordID string) (int, error) {
	var dbUserID int
	err := Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE discord_id = $1`, discordID).Scan(&dbUserID)
	if err == nil {
		return dbUserID, nil
	}
	err = Pool.QueryRow(context.Background(), `INSERT INTO users (discord_id) VALUES ($1) RETURNING id`, discordID).Scan(&dbUserID)
	if err != nil {
		log.Printf("Failed to insert user %s: %v", discordID, err)
		// Attempt to fetch again in case of concurrent insert
		fetchErr := Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE discord_id = $1`, discordID).Scan(&dbUserID)
		if fetchErr == nil {
			return dbUserID, nil
		}
		return 0, fmt.Errorf("unable to create or find user %s in database: %w (original insert error: %v)", discordID, fetchErr, err)
	}
	return dbUserID, nil
}

// UpdateUserDebt updates the summary user_debts table
// debtorDbID and creditorDbID are the integer IDs from the 'users' table
func UpdateUserDebt(debtorDbID, creditorDbID int, amount float64) error {
	query := `
        INSERT INTO user_debts (debtor_id, creditor_id, amount, created_at, updated_at)
        VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
        ON CONFLICT (debtor_id, creditor_id)
        DO UPDATE SET amount = user_debts.amount + EXCLUDED.amount, updated_at = CURRENT_TIMESTAMP;
    `
	_, err := Pool.Exec(context.Background(), query, debtorDbID, creditorDbID, amount)
	if err != nil {
		log.Printf("Error updating user_debts for debtor %d, creditor %d, amount %.2f: %v", debtorDbID, creditorDbID, amount, err)
		return fmt.Errorf("failed to update user_debts: %w", err)
	}
	return nil
}

// CreateTransaction creates a new transaction between users
func CreateTransaction(payerID, payeeID int, amount float64, description string) (int, error) {
	var txID int
	err := Pool.QueryRow(context.Background(),
		`INSERT INTO transactions (payer_id, payee_id, amount, description) VALUES ($1, $2, $3, $4) RETURNING id`,
		payerID, payeeID, amount, description).Scan(&txID)
	if err != nil {
		return 0, fmt.Errorf("failed to create transaction: %w", err)
	}
	return txID, nil
}

// DebtDetail represents a debt relationship with transaction details
type DebtDetail struct {
	Amount              float64
	OtherPartyDiscordID string
	OtherPartyName      string
	Details             string
}

// GetUserDebtsWithDetails gets all outstanding debts with transaction details for a user
func GetUserDebtsWithDetails(userID int, isDebtor bool) ([]DebtDetail, error) {
	// Subquery to get a comma-separated list of recent unpaid transaction details
	transactionDetailsSubquery := `
	WITH RankedTransactionDetails AS (
		SELECT
			t.payer_id,
			t.payee_id,
			t.description || ' (TxID:' || t.id::text || ')' as detail_text,
			ROW_NUMBER() OVER (PARTITION BY t.payer_id, t.payee_id ORDER BY t.created_at DESC, t.id DESC) as rn
		FROM transactions t
		WHERE t.already_paid = false
	)
	SELECT
		rtd.payer_id,
		rtd.payee_id,
		STRING_AGG(rtd.detail_text, '; ' ORDER BY rtd.rn) as details
	FROM RankedTransactionDetails rtd
	WHERE rtd.rn <= 5 -- Limit to 5 most recent details per pair
	GROUP BY rtd.payer_id, rtd.payee_id
	`

	var query string
	if isDebtor {
		query = fmt.Sprintf(`
			SELECT ud.amount, u_other.discord_id AS other_party_discord_id,
				   COALESCE(tx_details.details, 'หนี้สินรวม ไม่พบรายการธุรกรรมที่ยังไม่ได้ชำระที่เกี่ยวข้อง') as details
			FROM user_debts ud
			JOIN users u_other ON ud.creditor_id = u_other.id
			LEFT JOIN (
				%s
			) AS tx_details ON tx_details.payer_id = ud.debtor_id AND tx_details.payee_id = ud.creditor_id
			WHERE ud.debtor_id = $1 AND ud.amount > 0.009
			ORDER BY ud.amount DESC;`, transactionDetailsSubquery)
	} else {
		query = fmt.Sprintf(`
			SELECT ud.amount, u_other.discord_id AS other_party_discord_id,
				   COALESCE(tx_details.details, 'หนี้สินรวม ไม่พบรายการธุรกรรมที่ยังไม่ได้ชำระที่เกี่ยวข้อง') as details
			FROM user_debts ud
			JOIN users u_other ON ud.debtor_id = u_other.id
			LEFT JOIN (
				%s
			) AS tx_details ON tx_details.payer_id = ud.debtor_id AND tx_details.payee_id = ud.creditor_id
			WHERE ud.creditor_id = $1 AND ud.amount > 0.009
			ORDER BY ud.amount DESC;`, transactionDetailsSubquery)
	}

	rows, err := Pool.Query(context.Background(), query, userID)
	if err != nil {
		return nil, fmt.Errorf("error querying user debts/dues with details: %w", err)
	}
	defer rows.Close()

	var results []DebtDetail
	for rows.Next() {
		var debt DebtDetail
		if err := rows.Scan(&debt.Amount, &debt.OtherPartyDiscordID, &debt.Details); err != nil {
			return nil, fmt.Errorf("error scanning debt/due with details row: %w", err)
		}

		// อัตโนมัติตั้งชื่อเริ่มต้นเป็นค่าว่าง ชื่อจริงจะถูกตั้งตอนดึงจาก Discord
		debt.OtherPartyName = ""

		results = append(results, debt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user debts/dues with details: %w", err)
	}

	return results, nil
}

// GetTotalDebtAmount gets the total debt amount between two users
func GetTotalDebtAmount(debtorID, creditorID int) (float64, error) {
	var totalAmount float64
	query := `SELECT COALESCE(amount, 0) FROM user_debts WHERE debtor_id = $1 AND creditor_id = $2`
	err := Pool.QueryRow(context.Background(), query, debtorID, creditorID).Scan(&totalAmount)
	if err != nil {
		return 0, fmt.Errorf("error getting total debt amount: %w", err)
	}
	return totalAmount, nil
}

// GetDiscordIDFromDbID gets a Discord ID from a database user ID
func GetDiscordIDFromDbID(dbUserID int) (string, error) {
	var discordID string
	query := `SELECT discord_id FROM users WHERE id = $1`
	err := Pool.QueryRow(context.Background(), query, dbUserID).Scan(&discordID)
	if err != nil {
		return "", fmt.Errorf("error getting Discord ID for user %d: %w", dbUserID, err)
	}
	return discordID, nil
}

// GetTransactionInfo gets information about a transaction
func GetTransactionInfo(txID int) (map[string]interface{}, error) {
	query := `
		SELECT t.id, t.payer_id, t.payee_id, t.amount, t.description, 
		       t.already_paid, t.created_at, t.paid_at
		FROM transactions t
		WHERE t.id = $1
	`

	var id, payerID, payeeID int
	var amount float64
	var description string
	var alreadyPaid bool
	var createdAt time.Time
	var paidAt *time.Time // Using pointer for nullable column

	err := Pool.QueryRow(context.Background(), query, txID).Scan(
		&id, &payerID, &payeeID, &amount, &description,
		&alreadyPaid, &createdAt, &paidAt,
	)

	if err != nil {
		return nil, fmt.Errorf("error getting transaction info: %w", err)
	}

	result := map[string]interface{}{
		"id":           id,
		"payer_id":     payerID,
		"payee_id":     payeeID,
		"amount":       amount,
		"description":  description,
		"already_paid": alreadyPaid,
		"created_at":   createdAt,
	}

	if paidAt != nil {
		result["paid_at"] = *paidAt
	}

	return result, nil
}

// UpdateFirebaseSiteStatus updates the status of a Firebase site
func UpdateFirebaseSiteStatus(siteName, status string) error {
	query := `
		UPDATE firebase_sites
		SET status = $1, updated_at = CURRENT_TIMESTAMP
		WHERE site_name = $2
	`

	_, err := Pool.Exec(context.Background(), query, status, siteName)
	if err != nil {
		return fmt.Errorf("error updating firebase site status: %w", err)
	}

	return nil
}

// SaveFirebaseSite saves a Firebase site to the database
func SaveFirebaseSite(userDbID int, projectID, siteName, siteURL, token string) error {
	query := `
		INSERT INTO firebase_sites (user_db_id, firebase_project_id, site_name, site_url, site_token, status)
		VALUES ($1, $2, $3, $4, $5, 'active')
		ON CONFLICT (site_name) 
		DO UPDATE SET site_url = $4, site_token = $5, status = 'active', updated_at = CURRENT_TIMESTAMP
	`

	_, err := Pool.Exec(context.Background(), query, userDbID, projectID, siteName, siteURL, token)
	if err != nil {
		return fmt.Errorf("error saving firebase site: %w", err)
	}

	return nil
}

// GetFirebaseSiteByToken gets a Firebase site by token
func GetFirebaseSiteByToken(token string) (*FirebaseSite, error) {
	query := `
		SELECT id, user_db_id, firebase_project_id, site_name, site_url, created_at, status, site_token
		FROM firebase_sites
		WHERE site_token = $1 AND status = 'active'
		LIMIT 1
	`

	var site FirebaseSite
	err := Pool.QueryRow(context.Background(), query, token).Scan(
		&site.ID, &site.UserDbID, &site.FirebaseProjectID, &site.SiteName,
		&site.SiteURL, &site.CreatedAt, &site.Status, &site.SiteToken,
	)

	if err != nil {
		return nil, fmt.Errorf("error finding firebase site by token: %w", err)
	}

	return &site, nil
}

// GetExpiredFirebaseSites gets Firebase sites that were created more than 'minutes' minutes ago
func GetExpiredFirebaseSites(minutes int) ([]FirebaseSite, error) {
	query := `
		SELECT id, user_db_id, firebase_project_id, site_name, site_url, created_at, status, site_token
		FROM firebase_sites
		WHERE status = 'active' AND created_at < NOW() - INTERVAL '1 minute' * $1
	`

	rows, err := Pool.Query(context.Background(), query, minutes)
	if err != nil {
		return nil, fmt.Errorf("error querying expired firebase sites: %w", err)
	}
	defer rows.Close()

	var sites []FirebaseSite
	for rows.Next() {
		var site FirebaseSite
		if err := rows.Scan(
			&site.ID, &site.UserDbID, &site.FirebaseProjectID, &site.SiteName,
			&site.SiteURL, &site.CreatedAt, &site.Status, &site.SiteToken,
		); err != nil {
			return nil, fmt.Errorf("error scanning firebase site row: %w", err)
		}
		sites = append(sites, site)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating firebase site rows: %w", err)
	}

	return sites, nil
}

// FirebaseSite represents a firebase site in the database
type FirebaseSite struct {
	ID                int
	UserDbID          int
	FirebaseProjectID string
	SiteName          string
	SiteURL           string
	SiteToken         string
	CreatedAt         time.Time
	Status            string
}
