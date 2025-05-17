package discord

import (
	"github.com/oatsaysai/billing-in-discord/internal/discord/commands"
)

// setupCommandRegistration sets up the command registration process
func setupCommandRegistration() {
	// Set the registration function in the commands package
	// Create a wrapper function with the correct signature
	wrapper := func(cmdDef commands.CommandDefinition) {
		// Convert commands.CommandDefinition to discord.CommandDefinition
		discordCmd := CommandDefinition{
			Name:        cmdDef.Name,
			Description: cmdDef.Description,
			Usage:       cmdDef.Usage,
			Examples:    cmdDef.Examples,
			Handler:     cmdDef.Handler,
		}
		RegisterCommand(discordCmd)
	}
	commands.SetRegisterFunction(wrapper)
}

// UpdateRegistry initializes and updates the command registry
func UpdateRegistry() {
	// Set up command registration
	setupCommandRegistration()

	// Register all commands
	commands.Register()
}
