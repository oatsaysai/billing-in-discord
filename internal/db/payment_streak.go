package db

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// BillPaymentRanking represents a payment ranking record
type BillPaymentRanking struct {
	ID              int       `json:"id"`
	BillID          int       `json:"bill_id"`          // Reference to the bill/transaction
	UserID          int       `json:"user_id"`          // The user who made the payment
	Rank            int       `json:"rank"`             // 1, 2, or 3
	PaidAt          time.Time `json:"paid_at"`          // When the payment was made
	PaymentDuration int       `json:"payment_duration"` // Time in seconds from bill creation to payment
	ReceivedPraise  bool      `json:"received_praise"`  // Whether the user received praise (for rank 1)
	CreatedAt       time.Time `json:"created_at"`
}

// MigratePaymentStreakTables creates the payment_streak tables if they don't exist
func MigratePaymentStreakTables() error {
	// Create bill_payment_ranking table
	_, err := Pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS bill_payment_ranking (
		id SERIAL PRIMARY KEY,
		bill_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		rank INTEGER NOT NULL,
		paid_at TIMESTAMP NOT NULL,
		payment_duration INTEGER NOT NULL, -- seconds from bill creation to payment
		received_praise BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(bill_id, rank) -- Each bill can only have one of each rank
	)`)
	if err != nil {
		return fmt.Errorf("error creating bill_payment_ranking table: %w", err)
	}

	// Create streak_data table
	_, err = Pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS payment_streak (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		current_streak INTEGER NOT NULL DEFAULT 0,
		longest_streak INTEGER NOT NULL DEFAULT 0,
		last_payment_date TIMESTAMP,
		rank1_count INTEGER NOT NULL DEFAULT 0,
		rank2_count INTEGER NOT NULL DEFAULT 0,
		rank3_count INTEGER NOT NULL DEFAULT 0,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id)
	)`)
	if err != nil {
		return fmt.Errorf("error creating payment_streak table: %w", err)
	}

	// Apply trigger to payment_streak
	streakTrigger := `
	DROP TRIGGER IF EXISTS update_payment_streak_modtime ON payment_streak;
	CREATE TRIGGER update_payment_streak_modtime
	BEFORE UPDATE ON payment_streak
	FOR EACH ROW
	EXECUTE FUNCTION update_modified_column();`
	_, err = Pool.Exec(context.Background(), streakTrigger)
	if err != nil {
		return fmt.Errorf("error applying trigger to payment_streak: %w", err)
	}

	log.Println("Payment streak tables migrated successfully")
	return nil
}

// UpdatePaymentRankAndStreak is a common utility function that handles payment ranking and streak updates
// Can be used with or without a transaction - if txn is nil, a new transaction will be created
func UpdatePaymentRankAndStreak(txn pgx.Tx, billID, userID, rank int, paidAt time.Time, durationSeconds int) error {
	var err error
	var ownTx bool
	var tx pgx.Tx

	// Use provided transaction or create a new one
	if txn == nil {
		tx, err = Pool.Begin(context.Background())
		if err != nil {
			return fmt.Errorf("error starting transaction: %w", err)
		}
		defer tx.Rollback(context.Background())
		ownTx = true
	} else {
		tx = txn
	}

	// Record the payment ranking
	_, err = tx.Exec(context.Background(), `
		INSERT INTO bill_payment_ranking 
		(bill_id, user_id, rank, paid_at, payment_duration) 
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bill_id, rank) DO UPDATE 
		SET user_id = $2, paid_at = $4, payment_duration = $5
	`, billID, userID, rank, paidAt, durationSeconds)
	if err != nil {
		return fmt.Errorf("error recording payment ranking: %w", err)
	}

	// Update the user's streak data
	var existingRecord bool
	err = tx.QueryRow(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM payment_streak WHERE user_id = $1)
	`, userID).Scan(&existingRecord)
	if err != nil {
		return fmt.Errorf("error checking existing streak record: %w", err)
	}

	if !existingRecord {
		// Create new streak record
		_, err = tx.Exec(context.Background(), `
			INSERT INTO payment_streak 
			(user_id, current_streak, longest_streak, last_payment_date, 
			rank1_count, rank2_count, rank3_count) 
			VALUES ($1, 1, 1, $2, 
			$3, $4, $5)
		`, userID, paidAt,
			func() int {
				if rank == 1 {
					return 1
				} else {
					return 0
				}
			}(),
			func() int {
				if rank == 2 {
					return 1
				} else {
					return 0
				}
			}(),
			func() int {
				if rank == 3 {
					return 1
				} else {
					return 0
				}
			}())
		if err != nil {
			return fmt.Errorf("error creating streak record: %w", err)
		}
	} else {
		// Update existing streak record
		_, err = tx.Exec(context.Background(), `
			UPDATE payment_streak SET 
			current_streak = CASE 
				WHEN DATE(last_payment_date) >= DATE(NOW() - INTERVAL '24 hours') THEN current_streak + 1 
				ELSE 1 
			END,
			longest_streak = CASE 
				WHEN DATE(last_payment_date) >= DATE(NOW() - INTERVAL '24 hours') AND current_streak + 1 > longest_streak THEN current_streak + 1 
				WHEN current_streak + 1 > longest_streak THEN current_streak + 1
				ELSE longest_streak 
			END,
			last_payment_date = $2,
			rank1_count = CASE WHEN $3 = 1 THEN rank1_count + 1 ELSE rank1_count END,
			rank2_count = CASE WHEN $3 = 2 THEN rank2_count + 1 ELSE rank2_count END,
			rank3_count = CASE WHEN $3 = 3 THEN rank3_count + 1 ELSE rank3_count END
			WHERE user_id = $1
		`, userID, paidAt, rank)
		if err != nil {
			return fmt.Errorf("error updating streak record: %w", err)
		}
	}

	// Commit the transaction if we own it
	if ownTx {
		if err := tx.Commit(context.Background()); err != nil {
			return fmt.Errorf("error committing transaction: %w", err)
		}
	}

	return nil
}

// RecordPaymentRanking records a user's payment ranking for a bill
func RecordPaymentRanking(billID, userID, rank int, paidAt time.Time, durationSeconds int) error {
	// Use the common utility function with a nil transaction to create its own
	var tx pgx.Tx = nil
	return UpdatePaymentRankAndStreak(tx, billID, userID, rank, paidAt, durationSeconds)
}

// MarkPraiseGiven marks that praise was given for a payment ranking
func MarkPraiseGiven(billID, rank int) error {
	_, err := Pool.Exec(context.Background(), `
		UPDATE bill_payment_ranking SET received_praise = true
		WHERE bill_id = $1 AND rank = $2
	`, billID, rank)
	if err != nil {
		return fmt.Errorf("error marking praise given: %w", err)
	}
	return nil
}

// GetUserPaymentStreak gets a user's payment streak information
func GetUserPaymentStreak(userDiscordID string) (*PaymentStreakInfo, error) {
	userDbID, err := GetOrCreateUser(userDiscordID)
	if err != nil {
		return nil, fmt.Errorf("error getting user ID: %w", err)
	}

	var streak PaymentStreakInfo
	err = Pool.QueryRow(context.Background(), `
		SELECT user_id, current_streak, longest_streak, last_payment_date, 
		rank1_count, rank2_count, rank3_count, updated_at
		FROM payment_streak
		WHERE user_id = $1
	`, userDbID).Scan(
		&streak.UserID,
		&streak.CurrentStreak,
		&streak.LongestStreak,
		&streak.LastPaymentDate,
		&streak.Rank1Count,
		&streak.Rank2Count,
		&streak.Rank3Count,
		&streak.UpdatedAt,
	)
	if err != nil {
		// If no record exists, return an empty streak
		return &PaymentStreakInfo{
			UserID:          userDbID,
			CurrentStreak:   0,
			LongestStreak:   0,
			LastPaymentDate: time.Time{},
			Rank1Count:      0,
			Rank2Count:      0,
			Rank3Count:      0,
			UpdatedAt:       time.Now(),
		}, nil
	}

	return &streak, nil
}

// PaymentStreakInfo represents a user's payment streak information
type PaymentStreakInfo struct {
	UserID          int       `json:"user_id"`
	CurrentStreak   int       `json:"current_streak"`
	LongestStreak   int       `json:"longest_streak"`
	LastPaymentDate time.Time `json:"last_payment_date"`
	Rank1Count      int       `json:"rank1_count"`
	Rank2Count      int       `json:"rank2_count"`
	Rank3Count      int       `json:"rank3_count"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CheckPaymentRankBadges checks if a user is eligible for payment rank badges
func CheckPaymentRankBadges(userDiscordID string) ([]Badge, error) {
	userDbID, err := GetOrCreateUser(userDiscordID)
	if err != nil {
		return nil, fmt.Errorf("error getting user ID: %w", err)
	}

	// Get user's payment streak info
	streakInfo, err := GetUserPaymentStreak(userDiscordID)
	if err != nil {
		return nil, fmt.Errorf("error getting payment streak info: %w", err)
	}

	// Get badges user already has
	rows, err := Pool.Query(context.Background(), "SELECT badge_id FROM user_badges WHERE user_id = $1", userDbID)
	if err != nil {
		return nil, fmt.Errorf("error querying user badges: %w", err)
	}
	defer rows.Close()

	// Create a set of existing badge IDs
	existingBadgeIDs := make(map[int]bool)
	for rows.Next() {
		var badgeID int
		if err := rows.Scan(&badgeID); err != nil {
			return nil, fmt.Errorf("error scanning badge ID: %w", err)
		}
		existingBadgeIDs[badgeID] = true
	}

	var newlyEarnedBadges []Badge

	// Check for rank 1 badge (ผู้ชำระเร็วที่สุด)
	if streakInfo.Rank1Count > 0 {
		badge, err := awardBadgeIfEligible(userDbID, "ผู้ชำระเร็วที่สุด", existingBadgeIDs)
		if err != nil {
			log.Printf("Error awarding rank 1 badge: %v", err)
		} else if badge != nil {
			newlyEarnedBadges = append(newlyEarnedBadges, *badge)
		}
	}

	// Check for rank 2 badge (ผู้ชำระเร็วอันดับ 2)
	if streakInfo.Rank2Count > 0 {
		badge, err := awardBadgeIfEligible(userDbID, "ผู้ชำระเร็วอันดับ 2", existingBadgeIDs)
		if err != nil {
			log.Printf("Error awarding rank 2 badge: %v", err)
		} else if badge != nil {
			newlyEarnedBadges = append(newlyEarnedBadges, *badge)
		}
	}

	// Check for rank 3 badge (ผู้ชำระเร็วอันดับ 3)
	if streakInfo.Rank3Count > 0 {
		badge, err := awardBadgeIfEligible(userDbID, "ผู้ชำระเร็วอันดับ 3", existingBadgeIDs)
		if err != nil {
			log.Printf("Error awarding rank 3 badge: %v", err)
		} else if badge != nil {
			newlyEarnedBadges = append(newlyEarnedBadges, *badge)
		}
	}

	return newlyEarnedBadges, nil
}
