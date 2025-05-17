package commands

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
)

// RegisterPaymentCommands registers all payment-related commands
func RegisterPaymentCommands() {
	// Register the paid command
	registerCommand(CommandDefinition{
		Name:        "paid",
		Description: "Mark transactions as paid",
		Usage:       "!paid <TxID1>[,<TxID2>,...]",
		Examples: []string{
			"!paid 123",
			"!paid 123,456,789",
		},
		Handler: handlers.HandlePaidCommand,
	})

	// Register the request command
	registerCommand(CommandDefinition{
		Name:        "request",
		Description: "Request payment from a user",
		Usage:       "!request @user [promptpay_id]",
		Examples: []string{
			"!request @user",
			"!request @user 0812345678",
		},
		Handler: handlers.HandleRequestPayment,
	})
}
