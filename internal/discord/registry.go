package discord

import (
	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
	"log"
	"strings"
)

// CommandHandler defines the function signature for command handlers
type CommandHandler func(s *discordgo.Session, m *discordgo.MessageCreate, args []string)

// CommandDefinition holds information about a command
type CommandDefinition struct {
	Name        string
	Description string
	Usage       string
	Examples    []string
	Handler     CommandHandler
}

// commandRegistry holds all registered commands
var commandRegistry = make(map[string]CommandDefinition)

// RegisterCommand adds a command to the registry
func RegisterCommand(cmd CommandDefinition) {
	commandRegistry[strings.ToLower(cmd.Name)] = cmd
}

// GetCommand retrieves a command from the registry
func GetCommand(name string) (CommandDefinition, bool) {
	cmd, exists := commandRegistry[strings.ToLower(name)]
	return cmd, exists
}

// ProcessCommand routes a message to the appropriate command handler
func ProcessCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Skip messages from the bot itself
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Handle slip verification replies
	if m.MessageReference != nil && m.MessageReference.MessageID != "" && len(m.Attachments) > 0 {
		go handlers.HandleSlipVerification(s, m)
		return
	}

	// Parse the message
	content := strings.TrimSpace(m.Content)
	args := strings.Fields(content)
	if len(args) == 0 {
		return
	}

	// Extract the command name (remove ! prefix)
	commandName := strings.ToLower(args[0])
	if !strings.HasPrefix(commandName, "!") {
		return
	}
	commandName = strings.TrimPrefix(commandName, "!")

	// Route to the registered command handler
	if cmd, exists := GetCommand(commandName); exists {
		go cmd.Handler(s, m, args)
		return
	}

	// If no registered command found, log it for debugging
	log.Printf("Unrecognized command: %s", commandName)
}

// InitializeCommands registers all available commands
func InitializeCommands() {
	// Command registration will be done in the commands package
	// This function will be called by Initialize in discord.go
}
