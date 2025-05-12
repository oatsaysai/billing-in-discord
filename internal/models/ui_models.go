package models

// UserDebtDetail extends UserDebt with additional details for UI display
type UserDebtDetail struct {
	OtherPartyDiscordID string
	OtherPartyName      string
	Amount              float64
	Details             string
	RecentTransactions  []TransactionSummary
}

// TransactionSummary provides a summary of a transaction for UI display
type TransactionSummary struct {
	ID             int
	Amount         float64
	Description    string
	AlreadyPaid    bool
	OtherPartyID   string
	OtherPartyName string
	Date           string
	Role           string // "debtor" or "creditor"
}

// InteractiveItem represents an item in an interactive select menu
type InteractiveItem struct {
	ID          string
	Label       string
	Description string
	Value       string
	Selected    bool
	Disabled    bool
	Emoji       string
}
