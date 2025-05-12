package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetUserTransactions gets transactions for a user based on roles and status
func GetUserTransactions(userDbID int, isDebtor bool, isPaid bool, limit int) ([]map[string]interface{}, error) {
	var rows pgx.Rows
	var err error

	if isDebtor {
		// User is the payer
		query := `SELECT t.id, t.amount, t.description, t.created_at, t.already_paid, u.discord_id 
				 FROM transactions t JOIN users u ON t.payee_id = u.id 
				 WHERE t.payer_id = $1 AND t.already_paid = $2 
				 ORDER BY t.created_at DESC LIMIT $3`
		rows, err = Pool.Query(context.Background(), query, userDbID, isPaid, limit)
	} else {
		// User is the payee
		query := `SELECT t.id, t.amount, t.description, t.created_at, t.already_paid, u.discord_id 
				 FROM transactions t JOIN users u ON t.payer_id = u.id 
				 WHERE t.payee_id = $1 AND t.already_paid = $2 
				 ORDER BY t.created_at DESC LIMIT $3`
		rows, err = Pool.Query(context.Background(), query, userDbID, isPaid, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("error querying transactions: %w", err)
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var id int
		var amount float64
		var description string
		var createdAt time.Time
		var alreadyPaid bool
		var otherPartyDiscordID string

		err := rows.Scan(&id, &amount, &description, &createdAt, &alreadyPaid, &otherPartyDiscordID)
		if err != nil {
			return nil, fmt.Errorf("error scanning row: %w", err)
		}

		result = append(result, map[string]interface{}{
			"id":                     id,
			"amount":                 amount,
			"description":            description,
			"created_at":             createdAt.Format(time.RFC3339),
			"already_paid":           alreadyPaid,
			"other_party_discord_id": otherPartyDiscordID,
		})
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return result, nil
}

// GetAllUserTransactions gets all transactions involving a user
func GetAllUserTransactions(userDbID int, limit int) ([]map[string]interface{}, error) {
	query := `
		SELECT t.id, t.amount, t.description, t.created_at, t.already_paid, 
               CASE WHEN t.payer_id = $1 THEN u.discord_id ELSE u2.discord_id END as other_party_discord_id,
               CASE WHEN t.payer_id = $2 THEN 'debtor' ELSE 'creditor' END as role
        FROM transactions t 
        JOIN users u ON t.payee_id = u.id 
        JOIN users u2 ON t.payer_id = u2.id
        WHERE t.payer_id = $3 OR t.payee_id = $4 
        ORDER BY t.created_at DESC LIMIT $5`

	rows, err := Pool.Query(context.Background(), query, userDbID, userDbID, userDbID, userDbID, limit)
	if err != nil {
		return nil, fmt.Errorf("error querying all transactions: %w", err)
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var id int
		var amount float64
		var description string
		var createdAt time.Time
		var alreadyPaid bool
		var otherPartyDiscordID string
		var role string

		err := rows.Scan(&id, &amount, &description, &createdAt, &alreadyPaid, &otherPartyDiscordID, &role)
		if err != nil {
			return nil, fmt.Errorf("error scanning row: %w", err)
		}

		result = append(result, map[string]interface{}{
			"id":                     id,
			"amount":                 amount,
			"description":            description,
			"created_at":             createdAt.Format(time.RFC3339),
			"already_paid":           alreadyPaid,
			"other_party_discord_id": otherPartyDiscordID,
			"role":                   role,
		})
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return result, nil
}

// GetRecentTransactions gets recent transactions between two users
func GetRecentTransactions(debtorDbID, creditorDbID, limit int, includePaid bool) ([]map[string]interface{}, error) {
	var whereClause string
	if !includePaid {
		whereClause = "AND t.already_paid = false"
	}

	query := fmt.Sprintf(`
		SELECT t.id, t.amount, t.description, t.created_at, t.already_paid
		FROM transactions t
		WHERE t.payer_id = $1 AND t.payee_id = $2 %s
		ORDER BY t.created_at DESC LIMIT $3`, whereClause)

	rows, err := Pool.Query(context.Background(), query, debtorDbID, creditorDbID, limit)
	if err != nil {
		return nil, fmt.Errorf("error querying recent transactions: %w", err)
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var id int
		var amount float64
		var description string
		var createdAt time.Time
		var alreadyPaid bool

		err := rows.Scan(&id, &amount, &description, &createdAt, &alreadyPaid)
		if err != nil {
			return nil, fmt.Errorf("error scanning row: %w", err)
		}

		result = append(result, map[string]interface{}{
			"id":           id,
			"amount":       amount,
			"description":  description,
			"created_at":   createdAt.Format(time.RFC3339),
			"already_paid": alreadyPaid,
		})
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return result, nil
}
