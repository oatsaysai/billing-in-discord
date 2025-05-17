# Discord Command Definitions

This directory contains the definitions for all Discord bot commands.

## Directory Structure

- `root.go` - Central command registration and utility functions
- `bill.go` - Bill and payment request command definitions
- `debts.go` - Debt-related command definitions
- `interactive.go` - Interactive UI command definitions
- `payments.go` - Payment-related command definitions
- `promptpay.go` - PromptPay management command definitions
- `help.go` - Help command definition

## Command Registration Process

Each command is defined using the `CommandDefinition` structure with the following fields:
- `Name` - The command name (without the ! prefix)
- `Description` - A brief description of what the command does
- `Usage` - The command syntax
- `Examples` - Example usages of the command
- `Handler` - The function that handles the command logic (from the handlers package)

Commands are registered through the `registerCommand` function, which is linked to the main Discord package at runtime.
