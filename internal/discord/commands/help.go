package commands

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
)

// RegisterHelpCommand registers the help command
func RegisterHelpCommand() {
	// Register the help command
	registerCommand(CommandDefinition{
		Name:        "help",
		Description: "Show help information about available commands",
		Usage:       "!help [command]",
		Examples: []string{
			"!help",
			"!help bill",
			"!help qr",
		},
		Handler: handlers.HandleHelpCommand,
	})
}
