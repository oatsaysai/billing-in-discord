# Billing in Discord

A Discord bot for managing bill splitting, expense tracking, and debt settlement through PromptPay (Thailand) with automated payment verification.

## Features

- **Bill Splitting:** Split expenses equally among multiple users.
- **QR Code Generation:** Generate PromptPay QR codes for payments.
- **Debt Tracking:** Track debts between users.
- **Transaction History:** View transaction history by payer or payee.
- **Debt Settlement:** Mark transactions as paid and update debts.
- **Multi-Item Bills:** Manage bills with multiple items interactively.
- **Slip Verification:** Automatically verify payment slips and update debts.

## Prerequisites

- Go 1.23 or higher
- PostgreSQL 16
- Docker (optional, for containerization)

## Installation

### Clone the repository

```bash
git clone https://github.com/oatsaysai/billing-in-discord.git
cd billing-in-discord
```

### Install dependencies

```bash
go mod download
```

### Database Setup

You can use the provided scripts to set up a PostgreSQL database:

```bash
# Start PostgreSQL database in Docker
chmod +x tools/start_db.sh
./tools/start_db.sh

# Create a new database (if needed)
chmod +x tools/drop_and_create_db.sh
./tools/drop_and_create_db.sh
```

## Configuration

Create a `config.yaml` file in the root directory:

```yaml
DiscordBot:
  Token: "YOUR_DISCORD_BOT_TOKEN"

PostgreSQL:
  Host: "localhost"
  Port: "5432"
  User: "postgres"
  Password: "postgres"
  DBName: "billing-db"
  Schema: "public"
  PoolMaxConns: 10
```

## Usage

### Running the Bot

```bash
go run *.go
```

### Docker Deployment

```bash
# Build the Docker image
docker build -t billing-in-discord .

# Run the container
docker run -d --name billing-bot --network host -v $(pwd)/config.yaml:/config.yaml billing-in-discord
```

## Bot Commands

### Bill Management

- **!bill [PromptPayID]**: Create a multi-item bill. Optionally specify a PromptPay ID for QR code generation.
  - Example:
    ```text
    !bill 0812345678
    100 for coffee with @Alice @Bob
    200 for lunch with @Charlie @Alice
    ```
- **!qr <amount> to @user for <description> <PromptPayID>**: Generate a QR code for a specific payment.

### Debt Tracking

- **!mydebts**: Show debts you owe to others.
- **!owedtome** or **!mydues**: Show debts others owe to you.
- **!debts @user**: Show debts of a specific user.
- **!dues @user**: Show dues owed to a specific user.

### Payment Management

- **!paid <TxID1>,<TxID2>,...**: Mark transactions as paid and update debts.

### Slip Verification

1. Reply to a QR code message from the bot.
2. Attach the payment slip image in your reply.
3. The bot will verify the payment amount and update the transaction status.

### Help

- **!help**: Display the list of available commands.
- **!help <command>**: Get detailed help for a specific command.

## Payment Verification

The bot supports automatic slip verification for payments:
1. Reply to a QR code message from the bot.
2. Attach the payment slip image in your reply.
3. The bot will verify the payment amount and update the transaction status.

## Database Schema

The application uses three main tables:
- **users**: Stores Discord user IDs.
- **transactions**: Records all billing transactions.
- **user_debts**: Tracks current debt balances between users.

## Development

### Database Migration

Database tables are automatically created when the application starts. See `db.go` for the schema details.

## Stopping the Bot

To stop the bot, press `CTRL+C` in the terminal where it's running, or stop the Docker container:

```bash
docker stop billing-bot
```

## License

MIT License

## Author

Created by [oatsaysai](https://github.com/oatsaysai)
