package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

// Badge represents a badge or achievement in the system
type Badge struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Emoji       string    `json:"emoji"`
	Category    string    `json:"category"`
	CreatedAt   time.Time `json:"created_at"`
}

// UserBadge represents a badge earned by a user
type UserBadge struct {
	ID           int       `json:"id"`
	UserID       int       `json:"user_id"`
	BadgeID      int       `json:"badge_id"`
	BadgeName    string    `json:"badge_name"`
	BadgeEmoji   string    `json:"badge_emoji"`
	Description  string    `json:"description"`
	UnlockedAt   time.Time `json:"unlocked_at"`
	ProgressData string    `json:"progress_data,omitempty"` // JSON string for progress data if applicable
}

// MigrateBadgeTables creates the badges and user_badges tables if they don't exist
func MigrateBadgeTables() error {
	// Create badges table
	_, err := Pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS badges (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100) NOT NULL,
		description TEXT NOT NULL,
		emoji VARCHAR(20) NOT NULL,
		category VARCHAR(50) NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("error creating badges table: %w", err)
	}

	// Create user_badges table
	_, err = Pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS user_badges (
		id SERIAL PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		badge_id INTEGER NOT NULL REFERENCES badges(id) ON DELETE CASCADE,
		unlocked_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		progress_data TEXT,
		UNIQUE(user_id, badge_id)
	)`)
	if err != nil {
		return fmt.Errorf("error creating user_badges table: %w", err)
	}

	// Insert default badges if they don't exist
	err = seedDefaultBadges()
	if err != nil {
		return fmt.Errorf("error seeding default badges: %w", err)
	}

	log.Println("Badge tables migrated successfully")
	return nil
}

// seedDefaultBadges inserts default badges if they don't exist
func seedDefaultBadges() error {
	// Define default badges
	defaultBadges := []Badge{
		{
			Name:        "ผู้ที่ติดหนี้หนักที่สุด",
			Description: "เคยมีหนี้สินสะสมมากกว่า 50,000 บาท",
			Emoji:       "🏋️",
			Category:    "debt",
		},
		{
			Name:        "เพื่อนที่ดีที่สุด",
			Description: "แชร์ค่าใช้จ่ายกับผู้อื่นมากกว่า 50 รายการ",
			Emoji:       "🤝",
			Category:    "social",
		},
		{
			Name:        "เศรษฐี",
			Description: "มีธุรกรรมรวมมูลค่าเกิน 10,000 บาท",
			Emoji:       "💰",
			Category:    "financial",
		},
		{
			Name:        "ปลอดหนี้",
			Description: "ไม่มีหนี้ค้างชำระติดต่อกัน 30 วัน",
			Emoji:       "🏆",
			Category:    "financial",
		},
		{
			Name:        "ผู้เริ่มต้น",
			Description: "สร้างรายการแบ่งจ่ายแรก",
			Emoji:       "🌱",
			Category:    "milestone",
		},
		{
			Name:        "ผู้ชำระเร็วที่สุด",
			Description: "เป็นคนแรกที่ชำระบิลเร็วที่สุด จนได้รับคำชมเชยจากเพื่อน",
			Emoji:       "🥇",
			Category:    "streak",
		},
		{
			Name:        "ผู้ชำระเร็วอันดับ 2",
			Description: "เป็นคนที่สองที่ชำระบิลเร็วรองจากคนแรก",
			Emoji:       "🥈",
			Category:    "streak",
		},
		{
			Name:        "ผู้ชำระเร็วอันดับ 3",
			Description: "เป็นคนที่สามที่ชำระบิลเร็วในกลุ่ม",
			Emoji:       "🥉",
			Category:    "streak",
		},
	}

	// Insert each badge if it doesn't exist
	for _, badge := range defaultBadges {
		var count int
		err := Pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM badges WHERE name = $1", badge.Name).Scan(&count)
		if err != nil {
			return err
		}

		if count == 0 {
			_, err = Pool.Exec(context.Background(), "INSERT INTO badges (name, description, emoji, category) VALUES ($1, $2, $3, $4)",
				badge.Name, badge.Description, badge.Emoji, badge.Category)
			if err != nil {
				return err
			}
			log.Printf("Added badge: %s", badge.Name)
		}
	}

	return nil
}

// GetUserBadges retrieves all badges earned by a user
func GetUserBadges(userDiscordID string) ([]UserBadge, error) {
	userDbID, err := GetOrCreateUser(userDiscordID)
	if err != nil {
		return nil, fmt.Errorf("error getting user ID: %w", err)
	}

	rows, err := Pool.Query(context.Background(), `
		SELECT ub.id, ub.user_id, ub.badge_id, b.name, b.emoji, b.description, ub.unlocked_at, ub.progress_data
		FROM user_badges ub
		JOIN badges b ON b.id = ub.badge_id
		WHERE ub.user_id = $1
		ORDER BY ub.unlocked_at DESC
	`, userDbID)
	if err != nil {
		return nil, fmt.Errorf("error querying user badges: %w", err)
	}
	defer rows.Close()

	var badges []UserBadge
	for rows.Next() {
		var badge UserBadge
		var progressData sql.NullString
		err := rows.Scan(
			&badge.ID,
			&badge.UserID,
			&badge.BadgeID,
			&badge.BadgeName,
			&badge.BadgeEmoji,
			&badge.Description,
			&badge.UnlockedAt,
			&progressData,
		)
		if err != nil {
			return nil, fmt.Errorf("error scanning badge: %w", err)
		}
		if progressData.Valid {
			badge.ProgressData = progressData.String
		}
		badges = append(badges, badge)
	}

	return badges, nil
}

// GetAllBadges retrieves all available badges in the system
func GetAllBadges() ([]Badge, error) {
	rows, err := Pool.Query(context.Background(), "SELECT id, name, description, emoji, category, created_at FROM badges ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("error querying badges: %w", err)
	}
	defer rows.Close()

	var badges []Badge
	for rows.Next() {
		var badge Badge
		err := rows.Scan(
			&badge.ID,
			&badge.Name,
			&badge.Description,
			&badge.Emoji,
			&badge.Category,
			&badge.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("error scanning badge: %w", err)
		}
		badges = append(badges, badge)
	}

	return badges, nil
}

// AwardBadgeToUser awards a badge to a user if they don't already have it
func AwardBadgeToUser(userDiscordID string, badgeName string, progressData string) error {
	userDbID, err := GetOrCreateUser(userDiscordID)
	if err != nil {
		return fmt.Errorf("error getting user ID: %w", err)
	}

	var badgeID int
	err = Pool.QueryRow(context.Background(), "SELECT id FROM badges WHERE name = $1", badgeName).Scan(&badgeID)
	if err != nil {
		return fmt.Errorf("error finding badge: %w", err)
	}

	// Check if user already has this badge
	var exists bool
	err = Pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM user_badges WHERE user_id = $1 AND badge_id = $2)",
		userDbID, badgeID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("error checking existing badge: %w", err)
	}

	if exists {
		// If user already has the badge, update progress data if provided
		if progressData != "" {
			_, err = Pool.Exec(context.Background(), "UPDATE user_badges SET progress_data = $1 WHERE user_id = $2 AND badge_id = $3",
				progressData, userDbID, badgeID)
			if err != nil {
				return fmt.Errorf("error updating badge progress: %w", err)
			}
		}
		return nil
	}

	// Award new badge
	_, err = Pool.Exec(context.Background(), "INSERT INTO user_badges (user_id, badge_id, progress_data) VALUES ($1, $2, $3)",
		userDbID, badgeID, progressData)
	if err != nil {
		return fmt.Errorf("error awarding badge: %w", err)
	}

	log.Printf("Awarded badge '%s' to user %s", badgeName, userDiscordID)
	return nil
}

// CheckBadgeEligibility checks if a user is eligible for any badges they don't have yet
// This should be called after relevant actions (payment, transaction creation, etc.)
func CheckBadgeEligibility(userDiscordID string) ([]Badge, error) {
	// Get user database ID
	userDbID, err := GetOrCreateUser(userDiscordID)
	if err != nil {
		return nil, fmt.Errorf("error getting user ID: %w", err)
	}

	// Get badges user already has
	rows, err := Pool.Query(context.Background(), "SELECT badge_id FROM user_badges WHERE user_id = $1", userDbID)
	if err != nil {
		return nil, fmt.Errorf("error querying user badges: %w", err)
	}
	defer rows.Close()

	var existingBadgeIDs []int
	for rows.Next() {
		var badgeID int
		if err := rows.Scan(&badgeID); err != nil {
			return nil, fmt.Errorf("error scanning badge ID: %w", err)
		}
		existingBadgeIDs = append(existingBadgeIDs, badgeID)
	}

	// Set of badge IDs the user already has for faster lookup
	badgeIDSet := make(map[int]bool)
	for _, id := range existingBadgeIDs {
		badgeIDSet[id] = true
	}

	// Check eligibility for each badge
	var newlyEarnedBadges []Badge

	// 1. Check for "ผู้เริ่มต้น" badge (first bill split)
	txCount, err := getTransactionCount(userDbID)
	if err != nil {
		log.Printf("Error checking transaction count: %v", err)
	} else if txCount > 0 {
		// User has at least one transaction, eligible for beginner badge
		badge, err := awardBadgeIfEligible(userDbID, "ผู้เริ่มต้น", badgeIDSet)
		if err != nil {
			log.Printf("Error awarding beginner badge: %v", err)
		} else if badge != nil {
			newlyEarnedBadges = append(newlyEarnedBadges, *badge)
		}
	}

	// 1.1 Check for "ผู้ที่ติดหนี้หนักที่สุด" badge (had debt over 50,000 baht)
	maxDebt, err := getMaximumDebtAmount(userDbID)
	if err != nil {
		log.Printf("Error checking maximum debt amount: %v", err)
	} else if maxDebt >= 50000 {
		// User has had debt over 50,000 baht
		badge, err := awardBadgeIfEligible(userDbID, "ผู้ที่ติดหนี้หนักที่สุด", badgeIDSet)
		if err != nil {
			log.Printf("Error awarding heavy debt badge: %v", err)
		} else if badge != nil {
			newlyEarnedBadges = append(newlyEarnedBadges, *badge)
		}
	}

	// 2. Check for "เศรษฐี" badge (total transactions over 10,000 baht)
	totalAmount, err := getTotalTransactionAmount(userDbID)
	if err != nil {
		log.Printf("Error checking total transaction amount: %v", err)
	} else if totalAmount >= 10000 {
		// User has transactions totaling over 10,000 baht
		badge, err := awardBadgeIfEligible(userDbID, "เศรษฐี", badgeIDSet)
		if err != nil {
			log.Printf("Error awarding rich badge: %v", err)
		} else if badge != nil {
			newlyEarnedBadges = append(newlyEarnedBadges, *badge)
		}
	}

	// 3. Check for "เพื่อนที่ดีที่สุด" badge (shared transactions with more than 50 other people)
	uniqueTransactionPartners, err := getUniqueTransactionPartners(userDbID)
	if err != nil {
		log.Printf("Error checking transaction partners: %v", err)
	} else if uniqueTransactionPartners >= 50 {
		badge, err := awardBadgeIfEligible(userDbID, "เพื่อนที่ดีที่สุด", badgeIDSet)
		if err != nil {
			log.Printf("Error awarding best friend badge: %v", err)
		} else if badge != nil {
			newlyEarnedBadges = append(newlyEarnedBadges, *badge)
		}
	}

	// 4. Check for "ปลอดหนี้" badge (no outstanding debts for 30 consecutive days)
	debtFreeForDays, err := getConsecutiveDebtFreeDays(userDbID)
	if err != nil {
		log.Printf("Error checking debt-free days: %v", err)
	} else if debtFreeForDays >= 30 {
		badge, err := awardBadgeIfEligible(userDbID, "ปลอดหนี้", badgeIDSet)
		if err != nil {
			log.Printf("Error awarding debt-free badge: %v", err)
		} else if badge != nil {
			newlyEarnedBadges = append(newlyEarnedBadges, *badge)
		}
	}

	// 5. Check for payment rank badges (อันดับ 1, 2, 3)
	rankBadges, err := CheckPaymentRankBadges(userDiscordID)
	if err != nil {
		log.Printf("Error checking payment rank badges: %v", err)
	} else {
		newlyEarnedBadges = append(newlyEarnedBadges, rankBadges...)
	}

	return newlyEarnedBadges, nil
}

// Helper functions for checking badge eligibility

func getTransactionCount(userDbID int) (int, error) {
	var count int
	err := Pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM transactions 
		WHERE (debtor_id = $1 OR creditor_id = $1)
	`, userDbID).Scan(&count)
	return count, err
}

func getTotalTransactionAmount(userDbID int) (float64, error) {
	var amount float64
	err := Pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(amount), 0) FROM transactions 
		WHERE (debtor_id = $1 OR creditor_id = $1)
	`, userDbID).Scan(&amount)
	return amount, err
}

func getUniqueTransactionPartners(userDbID int) (int, error) {
	var count int
	err := Pool.QueryRow(context.Background(), `
		SELECT COUNT(DISTINCT 
			CASE WHEN debtor_id = $1 THEN creditor_id ELSE debtor_id END
		) FROM transactions 
		WHERE (debtor_id = $1 OR creditor_id = $1)
	`, userDbID).Scan(&count)
	return count, err
}

func getConsecutiveDebtFreeDays(userDbID int) (int, error) {
	// This is a simplified version - in reality, this would need to track debt status over time
	// For now, we'll just check if the user has any current debts
	var debtCount int
	err := Pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM user_debts 
		WHERE debtor_id = $1 AND amount > 0
	`, userDbID).Scan(&debtCount)

	if err != nil {
		return 0, err
	}

	if debtCount > 0 {
		return 0, nil // User has debts, so 0 debt-free days
	}

	// If no current debts, check when the last debt was paid
	var lastDebtPaid sql.NullTime
	err = Pool.QueryRow(context.Background(), `
		SELECT MAX(updated_at) FROM transactions
		WHERE debtor_id = $1 AND already_paid = true
	`, userDbID).Scan(&lastDebtPaid)

	if err != nil {
		return 0, err
	}

	if !lastDebtPaid.Valid {
		// User never had debt (or never paid any), let's set a default
		return 30, nil
	}

	// Calculate days since last debt was paid
	days := int(time.Since(lastDebtPaid.Time).Hours() / 24)
	return days, nil
}

// getMaximumDebtAmount checks the maximum debt amount a user has ever had
func getMaximumDebtAmount(userDbID int) (float64, error) {
	// Check current total debt
	var currentTotalDebt float64
	err := Pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(amount), 0) FROM user_debts 
		WHERE debtor_id = $1
	`, userDbID).Scan(&currentTotalDebt)

	if err != nil {
		return 0, err
	}

	// Check for maximum historical debt
	// If your system stores transaction history and debt changes, you can use that
	// For now, we'll check if there's a history table or use a simpler approach

	// Option 1: If you have a debt_history table
	var maxHistoricalDebt sql.NullFloat64
	err = Pool.QueryRow(context.Background(), `
		SELECT MAX(amount) FROM user_debts
		WHERE debtor_id = $1
	`, userDbID).Scan(&maxHistoricalDebt)

	if err != nil {
		return 0, err
	}

	if !maxHistoricalDebt.Valid {
		return currentTotalDebt, nil
	}

	// Return the higher of current debt or max historical debt
	if currentTotalDebt > maxHistoricalDebt.Float64 {
		return currentTotalDebt, nil
	}

	return maxHistoricalDebt.Float64, nil
}

// Helper function to award a badge if the user is eligible and doesn't have it yet
func awardBadgeIfEligible(userDbID int, badgeName string, existingBadges map[int]bool) (*Badge, error) {
	var badge Badge
	err := Pool.QueryRow(context.Background(), `
		SELECT id, name, description, emoji, category, created_at 
		FROM badges WHERE name = $1
	`, badgeName).Scan(
		&badge.ID,
		&badge.Name,
		&badge.Description,
		&badge.Emoji,
		&badge.Category,
		&badge.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("error finding badge '%s': %w", badgeName, err)
	}

	// Check if user already has this badge
	if existingBadges[badge.ID] {
		return nil, nil // User already has this badge
	}

	// Award the badge
	_, err = Pool.Exec(context.Background(), "INSERT INTO user_badges (user_id, badge_id) VALUES ($1, $2)",
		userDbID, badge.ID)
	if err != nil {
		return nil, fmt.Errorf("error awarding badge: %w", err)
	}

	return &badge, nil
}
