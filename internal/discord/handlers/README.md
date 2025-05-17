# Discord Command Handlers

This directory contains the handler implementations for all Discord bot commands.

## Directory Structure

- `helpers.go` - Common helper functions used across handlers
- `bill.go` - Bill command handlers and related functions
- `ocr_bill.go` - OCR-based bill processing handlers
- `debts.go` - Debt-related command handlers
- `interactive.go` - Interactive UI command handlers
- `payments.go` - Payment-related command handlers
- `promptpay.go` - PromptPay management command handlers
- `components.go` - UI component interaction handler registration
- `button_handlers.go` - Button and dropdown interaction handlers
- `modal_handlers.go` - Modal submission handlers
- `slip_verification.go` - Payment slip verification handlers
- `help.go` - Help command handler

## Handler Implementation

Each handler follows this general signature:
```go
func HandleCommandName(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
    // Command implementation
}
```

Handlers are responsible for:
1. Parsing and validating arguments
2. Interacting with the database through the db package
3. Sending responses to the user
4. Generating interactive UI elements when needed

## Component Handler Implementation

Component handlers have a different signature:
```go
func handleComponentName(s *discordgo.Session, i *discordgo.InteractionCreate) {
    // Component handler implementation
}
```

These handlers process interactive elements like:
- Button clicks
- Dropdown selections
- Modal submissions

## Common Utilities

The `helpers.go` file provides shared utilities such as:
- `SendErrorMessage()` - Send formatted error messages
- `GenerateAndSendQrCode()` - Generate and send QR codes for payments
- `GetDiscordUsername()` - Retrieve usernames from Discord IDs
- `EnhanceDebtsWithUsernames()` - Add Discord usernames to debt objects
