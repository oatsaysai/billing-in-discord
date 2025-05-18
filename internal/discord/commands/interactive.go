package commands

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
)

// RegisterInteractiveCommands registers all interactive commands
func RegisterInteractiveCommands() {
	// Register the imydebts command
	registerCommand(CommandDefinition{
		Name:        "imydebts",
		Description: "Show your debts with interactive UI",
		Usage:       "!imydebts",
		Examples: []string{
			"!imydebts",
		},
		Handler: handlers.HandleInteractiveMyDebts,
	})

	// Register the imydues command
	registerCommand(CommandDefinition{
		Name:        "imydues",
		Description: "Show debts owed to you with interactive UI",
		Usage:       "!imydues",
		Examples: []string{
			"!imydues",
		},
		Handler: handlers.HandleInteractiveOwedToMe,
	})

	// Register the iowedtome command (alias for imydues)
	registerCommand(CommandDefinition{
		Name:        "iowedtome",
		Description: "Show debts owed to you with interactive UI",
		Usage:       "!iowedtome",
		Examples: []string{
			"!iowedtome",
		},
		Handler: handlers.HandleInteractiveOwedToMe,
	})

	// Register the list command
	registerCommand(CommandDefinition{
		Name:        "list",
		Description: "Browse and select transactions",
		Usage:       "!list [unpaid|paid|due]",
		Examples: []string{
			"!list",
			"!list unpaid",
			"!list paid",
			"!list due",
		},
		Handler: handlers.HandleSelectTransaction,
	})

	// Register the irequest command
	registerCommand(CommandDefinition{
		Name:        "irequest",
		Description: "Request payment with interactive UI",
		Usage:       "!irequest @user",
		Examples: []string{
			"!irequest @user",
		},
		Handler: handlers.HandleInteractiveRequestPayment,
	})
}
