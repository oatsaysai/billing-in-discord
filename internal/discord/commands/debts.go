package commands

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
)

// RegisterDebtCommands registers all debt-related commands
func RegisterDebtCommands() {
	// Register the mydebts command
	registerCommand(CommandDefinition{
		Name:        "mydebts",
		Description: "Show your debts (what you owe to others)",
		Usage:       "!mydebts",
		Examples: []string{
			"!mydebts",
		},
		Handler: handlers.HandleMyDebts,
	})

	// Register the owedtome command
	registerCommand(CommandDefinition{
		Name:        "owedtome",
		Description: "Show debts owed to you (what others owe you)",
		Usage:       "!owedtome",
		Examples: []string{
			"!owedtome",
		},
		Handler: handlers.HandleOwedToMe,
	})

	// Register the mydues command (alias for owedtome)
	registerCommand(CommandDefinition{
		Name:        "mydues",
		Description: "Show debts owed to you (what others owe you)",
		Usage:       "!mydues",
		Examples: []string{
			"!mydues",
		},
		Handler: handlers.HandleOwedToMe,
	})

	// Register the debts command
	registerCommand(CommandDefinition{
		Name:        "debts",
		Description: "Show debts of a specific user",
		Usage:       "!debts @user",
		Examples: []string{
			"!debts @user",
		},
		Handler: handlers.HandleDebtsOfUser,
	})

	// Register the dues command
	registerCommand(CommandDefinition{
		Name:        "dues",
		Description: "Show debts owed to a specific user",
		Usage:       "!dues @user",
		Examples: []string{
			"!dues @user",
		},
		Handler: handlers.HandleDuesForUser,
	})
}
