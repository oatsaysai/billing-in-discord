# Discord Bot Implementation

This directory contains the implementation of the Discord bot for the billing system.

## Directory Structure

- `commands/` - Command definitions (name, description, options, etc.)
- `handlers/` - Command handler implementations
- `discord.go` - Main Discord client initialization and management
- `registry.go` - Command registration and routing
- `registrables.go` - Helper functions for command registration

## Architecture

The Discord bot is designed with a clear separation between command definitions and handler implementations:

1. **Command Definitions** (`commands/`)
   - Each command is defined with its name, description, usage, and examples
   - Command definitions are grouped by functionality (bill, debt, payment, etc.)
   - Commands are registered using the `registerCommand` function

2. **Handler Implementations** (`handlers/`)
   - Each handler contains the actual logic for executing commands
   - Handlers are organized by functionality, matching the command structure
   - Common utilities are provided in `helpers.go`
   - Interactive UI components are handled in:
     - `components.go` - Component registration and routing
     - `button_handlers.go` - Button and dropdown handlers
     - `modal_handlers.go` - Modal submission handlers

3. **Command Registration** (`registry.go`)
   - The `ProcessCommand` function routes incoming messages to the appropriate handler
   - Commands are registered in the `commandRegistry` map

4. **Client Management** (`discord.go`)
   - Initializes the Discord session
   - Sets up event handlers
   - Manages API clients (OCR, Verifier, Firebase)

## Command Flow

1. When a message is received, `ProcessCommand` checks if it's a valid command
2. If it's a valid command, it looks up the appropriate handler in the registry
3. The handler is called with the session, message, and parsed arguments
4. The handler executes the command logic and sends a response

## Interactive Components

Interactive UI elements (buttons, dropdowns, etc.) are handled through:
1. Component registration in `handlers.RegisterComponentHandlers`
2. Component handlers in `handlers/components.go`
3. Custom ID prefixes to route interactions to the appropriate handler
