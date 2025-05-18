package commands

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
)

// RegisterStreakCommands registers the streak command
func RegisterStreakCommands() {
	// Register the streak command
	registerCommand(CommandDefinition{
		Name:        "streak",
		Description: "แสดงสถิติและข้อมูล streak การชำระเงินของคุณ",
		Usage:       "!streak [@user]",
		Examples: []string{
			"!streak",
			"!streak @friend",
		},
		Handler: handlers.HandleStreakCommand,
	})
}
