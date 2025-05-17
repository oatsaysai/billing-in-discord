package commands

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
)

// RegisterBillCommands registers all bill-related commands
func RegisterBillCommands() {
	// Register the bill command
	registerCommand(CommandDefinition{
		Name:        "bill",
		Description: "Create a bill to split expenses among users",
		Usage:       "!bill [promptpay_id]\n<amount> for <description> with @user1 @user2...\n...",
		Examples: []string{
			"!bill\n100 for dinner with @user1 @user2\n50 for drinks with @user1",
			"!bill 0812345678\n200 for lunch with @user1 @user2 @user3",
		},
		Handler: handlers.HandleBillCommand,
	})

	// Register the qr command
	registerCommand(CommandDefinition{
		Name:        "qr",
		Description: "Generate a QR code for payment",
		Usage:       "!qr <amount> to @user [for <description>] [promptpay_id]",
		Examples: []string{
			"!qr 100 to @user for dinner",
			"!qr 50 to @user for drinks 0812345678",
		},
		Handler: handlers.HandleQrCommand,
	})
}
