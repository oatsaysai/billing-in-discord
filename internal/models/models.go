package models

import (
	"time"
)

// BillItem represents an item in a bill
type BillItem struct {
	Description string
	Amount      float64
	SharedWith  []string // Slice of Discord User IDs
}

// FirebaseSite stores info about a deployed Firebase site
type FirebaseSite struct {
	ID                int       `json:"id"`
	UserDbID          int       `json:"user_db_id"`
	FirebaseProjectID string    `json:"firebase_project_id"`
	SiteName          string    `json:"site_name"` // The unique ID used for firebase hosting:site:<site_name>
	SiteURL           string    `json:"site_url"`
	CreatedAt         time.Time `json:"created_at"`
	Status            string    `json:"status"` // e.g., "active", "disabled"
}

// FirebaseSiteCreateResult is used for parsing Firebase CLI JSON output for site creation
type FirebaseSiteCreateResult struct {
	Status string `json:"status"`
	Result struct {
		Name       string `json:"name"`       // This is typically the site ID, e.g., projects/my-project/sites/my-site-id
		DefaultUrl string `json:"defaultUrl"` // The full URL
		Type       string `json:"type"`
	} `json:"result"`
	Error struct { // Added to potentially catch structured errors from Firebase JSON
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// FirebaseDeployResult is used for parsing Firebase CLI JSON output for deployment
type FirebaseDeployResult struct {
	Status string                 `json:"status"`
	Result map[string]interface{} `json:"result"` // Can be complex, e.g. {"hosting": {"site-name": "url"}}
	Error  struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// Transaction represents a financial transaction between users
type Transaction struct {
	ID          int       `json:"id"`
	PayerID     int       `json:"payer_id"`
	PayeeID     int       `json:"payee_id"`
	Amount      float64   `json:"amount"`
	Description string    `json:"description"`
	AlreadyPaid bool      `json:"already_paid"`
	CreatedAt   time.Time `json:"created_at"`
	PaidAt      time.Time `json:"paid_at,omitempty"`
}

// UserDebt represents a debt between users
type UserDebt struct {
	DebtorID   int       `json:"debtor_id"`
	CreditorID int       `json:"creditor_id"`
	Amount     float64   `json:"amount"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// User represents a user in the system
type User struct {
	ID        int       `json:"id"`
	DiscordID string    `json:"discord_id"`
	CreatedAt time.Time `json:"created_at"`
}
