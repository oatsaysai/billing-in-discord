package commands

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
)

// RegisterPromptPayCommands registers all PromptPay-related commands
func RegisterPromptPayCommands() {
	// Register the setpromptpay command
	registerCommand(CommandDefinition{
		Name:        "setpromptpay",
		Description: "Sets your PromptPay ID for receiving payments",
		Usage:       "!setpromptpay <promptpay_id>",
		Examples: []string{
			"!setpromptpay 0812345678",
			"!setpromptpay 1234567890123",
		},
		Handler: handlers.HandleSetPromptPay,
	})

	// Register the mypromptpay command
	registerCommand(CommandDefinition{
		Name:        "mypromptpay",
		Description: "Shows your currently set PromptPay ID",
		Usage:       "!mypromptpay",
		Examples: []string{
			"!mypromptpay",
		},
		Handler: handlers.HandleGetMyPromptPay,
	})
}
