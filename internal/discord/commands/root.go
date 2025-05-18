package commands

import (
	"github.com/bwmarrin/discordgo"
)

// CommandDefinition holds information about a command
type CommandDefinition struct {
	Name        string
	Description string
	Usage       string
	Examples    []string
	Handler     func(s *discordgo.Session, m *discordgo.MessageCreate, args []string)
}

// Command registration functions
var RegisterCommandFunc func(CommandDefinition)

// SetRegisterFunction sets the function to use for command registration
func SetRegisterFunction(fn func(CommandDefinition)) {
	RegisterCommandFunc = fn
}

// registerCommand adds a command to the registry using the provided function
func registerCommand(cmd CommandDefinition) {
	if RegisterCommandFunc != nil {
		RegisterCommandFunc(cmd)
	}
}

// Register registers all commands with the Discord package
func Register() {
	// Register bill commands
	RegisterBillCommands()

	// Register debt commands
	RegisterDebtCommands()

	// Register payment commands
	RegisterPaymentCommands()

	// Register promptpay commands
	RegisterPromptPayCommands()

	// Register interactive commands
	RegisterInteractiveCommands()

	// Register badge commands
	RegisterBadgeCommands()

	// Register help command
	RegisterHelpCommand()
}
