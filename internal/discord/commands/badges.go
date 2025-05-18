package commands

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
)

// RegisterBadgeCommands registers all badge-related commands
func RegisterBadgeCommands() {
	// Register the badges command
	registerCommand(CommandDefinition{
		Name:        "badges",
		Description: "Show your earned badges and achievements",
		Usage:       "!badges [@user]",
		Examples: []string{
			"!badges",
			"!badges @friend",
		},
		Handler: handlers.HandleBadgesCommand,
	})

	// Future badge-related commands can be added here
}
