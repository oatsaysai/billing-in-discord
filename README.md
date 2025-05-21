# Billing in Discord

Billing in Discord is a powerful Discord bot designed to streamline shared expense management, bill splitting, and payment tracking within communities. It leverages Thailand's PromptPay system for QR code generation and aims to provide a seamless financial coordination experience directly within Discord, complemented by features like interactive bill allocation via temporary web UIs, automated payment slip verification, and user engagement through badges and streaks.

Core technologies: Go, PostgreSQL, Docker, Discord API (discordgo), Firebase.

## Table of Contents

- [Project Status](#project-status)
- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [Usage](#usage)
- [Deployment](#deployment)
- [Bot Commands](#bot-commands)
- [Database Schema](#database-schema)
- [Development](#development)
- [Stopping the Bot](#stopping-the-bot)
- [License](#license)
- [Author](#author)

## Project Status

This project is currently under active development. Features are being added and refined. While it is functional, expect ongoing changes and potential for occasional bugs. Feedback and contributions are welcome!

## Features

- **Bill Splitting:** Split expenses among multiple users. Can be equal or itemized.
- **QR Code Generation:** Generate PromptPay QR codes for easy and error-free payments.
- **Debt Tracking:** Keep a clear record of who owes whom, ensuring transparency.
- **Transaction History:** View a comprehensive history of transactions by payer or payee.
- **Debt Settlement:** Mark transactions as paid to accurately update and settle outstanding debts.
- **Multi-Item Bills:** Interactively manage bills with multiple items using the `!bill` command, allowing for detailed expense allocation.
- **Automated Slip Verification:** Automatically verify uploaded payment slips to confirm transactions and update debt statuses.
- **Interactive Bill Allocation via Web UI:** Simplify complex bill splitting. The bot can generate a temporary, unique Firebase-hosted webpage where users can collaboratively assign bill items to participants before finalizing amounts.
- **Badges and Achievements:** Earn badges for various activities and milestones, fostering engagement. Check your collection with the `!badges` command.
- **Payment Streaks:** Track your consistency in settling debts and get recognized for timely payments using the `!streak` command.

## Prerequisites

- **Go:** Version 1.23 or higher. (As specified in `go.mod`)
- **PostgreSQL:** Version 16 or compatible.
- **Docker:** Optional, for containerized database setup or deployment.
- **Firebase CLI:** Optional. Needed if you intend to manage or test Firebase deployments locally for features like the interactive bill allocation UI. The bot typically handles these deployments programmatically, but local CLI setup can be useful for debugging or direct Firebase project management.

## Installation

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/oatsaysai/billing-in-discord.git
    cd billing-in-discord
    ```

2.  **Install dependencies:**
    ```bash
    go mod download
    ```

3.  **Database Setup:**
    *   The project includes scripts in the `tools/` directory to help set up a PostgreSQL database using Docker:
        ```bash
        # Ensure the scripts are executable
        chmod +x tools/start_db.sh
        chmod +x tools/drop_and_create_db.sh

        # Start PostgreSQL database in a Docker container
        ./tools/start_db.sh

        # Optional: Drop and recreate the database (if it already exists and you want a fresh start)
        # ./tools/drop_and_create_db.sh
        ```
    *   Database tables are automatically created and migrated when the bot first starts. For detailed information on the table structures, see the "Database Schema" section.

4.  **Configuration:**
    *   Create a `config.yaml` file in the root directory of the project.
    *   Populate this file based on the detailed instructions and examples provided in the "Configuration" section of this README.

## Configuration

Create a `config.yaml` file in the root directory with the following structure. Comments explain each field.

```yaml
DiscordBot:
  # Your Discord bot token.
  Token: "YOUR_DISCORD_BOT_TOKEN"

Firebase:
  # Settings for Firebase integration, used for deploying temporary bill allocation web UIs.
  # Your Firebase project ID.
  MainProjectID: "YOUR_FIREBASE_PROJECT_ID"
  # Path to your Firebase service account JSON key file.
  # Leave empty if using Application Default Credentials or other auth methods.
  ServiceAccountKeyPath: "path/to/your/serviceAccountKey.json"
  # Prefix for Firebase Hosting site names (e.g., 'mybills-').
  # The bot will create sites like 'mybills-uniqueid'.
  SiteNamePrefix: "mybills-"
  # Path to the Firebase CLI executable (if not in system PATH).
  # e.g., "/usr/local/bin/firebase" or "C:\\Program Files\\nodejs\\firebase.cmd"
  CliPath: "firebase" # Assumes firebase CLI is in PATH
  # The publicly accessible webhook URL for Firebase functions or interactions
  # (e.g., for receiving updates from the bill allocation UI).
  WebhookURL: "https://your-app-service-or-function-url.com/firebase-webhook"

PostgreSQL:
  # Host of the PostgreSQL server.
  Host: "localhost"
  # Port of the PostgreSQL server.
  Port: "5432"
  # Username for PostgreSQL connection.
  User: "postgres"
  # Password for PostgreSQL connection.
  Password: "postgres"
  # Database name.
  DBName: "billing-db"
  # Database schema.
  Schema: "public"
  # Maximum number of open connections to the database.
  PoolMaxConns: 10

SlipVerifier:
  # Settings for the external slip verification service.
  # URL of the slip verification API.
  ApiUrl: "SLIP_VERIFIER_API_URL" # e.g. "https://api.slipverify.com/verify"

OCR:
  # Settings for the Optical Character Recognition (OCR) service used for reading payment slips.
  # URL of the OCR API.
  ApiUrl: "OCR_API_URL" # e.g. "https://api.ocrprovider.com/read_slip"
  # Your API key for the OCR service.
  ApiKey: "YOUR_OCR_API_KEY"

Server:
  # Settings for the bot's internal HTTP server (e.g., for webhooks like Firebase).
  # Port on which the bot's server will listen.
  Port: "8080" # e.g., "8080"
```

## Usage

### Running the Bot Locally

-   **Using `make` (recommended):**
    ```bash
    make run
    ```
-   **Directly with `go run`:**
    ```bash
    go run ./cmd/server
    ```
    The bot will connect to Discord and be ready for commands. Ensure your `config.yaml` is correctly set up.

### Building the Bot

-   **Using `make` (recommended):**
    ```bash
    make build
    ```
    This will create an executable binary in the `bin/` directory.

### Bot Commands

For a detailed list of available bot commands and their usage, please refer to the "Bot Commands" section.

## Deployment

### Docker Deployment

This is a common method for deploying the bot.

1.  **Build the Docker image:**
    *   Using `make`:
        ```bash
        make build-docker
        ```
    *   Or manually:
        ```bash
        docker build -t billing-in-discord .
        ```

2.  **Run the Docker container:**
    *   Using `make` (this often includes specific volume mounts or environment variable setups defined in the Makefile):
        ```bash
        make run-docker
        ```
    *   Or manually (ensure you mount your `config.yaml`):
        ```bash
        # Example:
        docker run -d --name billing-bot \
          -v $(pwd)/config.yaml:/app/config.yaml \
          billing-in-discord
        ```
        Adjust the volume path `$(pwd)/config.yaml` if your configuration file is located elsewhere or if your `WORKDIR` in the Dockerfile is different from `/app`.

### Firebase Deployment Note (for Interactive Bill Allocation)

-   The interactive bill allocation feature deploys temporary websites to Firebase Hosting.
-   Ensure the Firebase service account key (`ServiceAccountKeyPath` in `config.yaml`) has the necessary permissions for Firebase Hosting (e.g., roles like "Firebase Hosting Admin" or "Firebase Admin").
-   Alternatively, if running in an environment that supports Application Default Credentials (ADC) (e.g., Google Cloud services), ensure ADC are correctly set up and have the required permissions.
-   No manual Firebase deployment steps are typically needed by the end-user running the bot, as the bot handles these deployments programmatically via the Firebase API/CLI as configured.

## Bot Commands

All commands must be prefixed with `!`.

### Bill Management

- **`!bill [promptpay_id]`**
  Create a multi-item bill. The bot will guide you through adding items. If `promptpay_id` is provided, it will be used for QR code generation for the total bill; otherwise, the payer's registered PromptPay ID will be used (if set).
  After running `!bill`, the bot will prompt you to add items in the format:
  `<amount> for <description> with @user1 @user2...`
  - Example:
    ```text
    !bill 0812345678
    ```
    Bot then prompts: "Please add items for the bill. Format: `<amount> for <description> with @user1 @user2...` or type `done` to finish."
    User replies:
    ```text
    100 for coffee with @Alice @Bob
    200 for lunch with @Charlie @Alice
    done
    ```
  To create a bill using your registered PromptPay ID (see `!setpromptpay`):
    ```text
    !bill
    ```
    (Followed by item input as above)

- **`!qr <amount> to @user [for <description>] [promptpay_id]`**
  Generate a QR code for a specific payment to another user.
  If `promptpay_id` is not provided, the recipient user's registered PromptPay ID will be used. If the recipient has no registered ID, the command will fail. The `description` is optional.
  - Examples:
    ```text
    !qr 150 to @Bob for movie tickets
    !qr 200 to @Alice 0898765432
    !qr 50 to @Charlie for snacks 0811223344
    ```

### Debt and Payment Tracking

- **`!mydebts`**
  Show a summary of debts you owe to others.

- **`!owedtome`** or **`!mydues`**
  Show a summary of debts others owe to you.

- **`!debts @user`**
  Show a summary of debts the mentioned user owes to others.
  - Example: `!debts @David`

- **`!dues @user`**
  Show a summary of dues owed to the mentioned user by others.
  - Example: `!dues @Eve`

- **`!paid <TxID1>,<TxID2>,...`**
  Mark one or more transactions (by their IDs) as paid. This updates the debt balances between users. Transaction IDs are provided when bills are created or debts are listed.
  - Example: `!paid tx_123abc,tx_456def`

### User Settings & Engagement

- **`!setpromptpay <promptpay_id>`**
  Set or update your default PromptPay ID. This ID will be used when you are the payer in a `!bill` command (if no other ID is specified) or when others use `!qr` to pay you without specifying an ID.
  - Example: `!setpromptpay 0812345678`

- **`!mypromptpay`**
  Check the PromptPay ID currently associated with your Discord account.
  - Example: `!mypromptpay`

- **`!badges [@user]`**
  Show your earned badges and achievements. If no user is mentioned, it shows your own badges.
  - Examples:
    ```text
    !badges
    !badges @Frank
    ```

- **`!streak [@user]`**
  Display your payment streak statistics (e.g., how consistently you've settled debts). Mention a user to see their streak.
  - Examples:
    ```text
    !streak
    !streak @Grace
    ```

### Slip Verification (Interaction)

This is not a typed command but an interaction with bot messages:
1. Reply to a QR code message generated by the bot.
2. Attach the payment slip image in your reply message.
3. The bot will attempt to automatically verify the payment amount from the slip and update the relevant transaction and debt status.

### Help

- **`!help`**
  Display the list of available commands and their brief descriptions.

- **`!help <command>`**
  Get detailed help for a specific command, including its syntax and examples.
  - Example: `!help bill`

## Database Schema

The application uses several PostgreSQL tables to store its data. Key tables are automatically created or migrated on startup. Here's an overview of the important ones:

- **`users`**: Stores Discord user IDs and basic user information (e.g., `discord_id`, `created_at`).
- **`transactions`**: Records all individual payment obligations that arise from bills or QR code payments, including payer, payee, amount, description, and payment status.
- **`user_debts`**: Tracks the net current debt balances between any two users, aggregating multiple transactions.
- **`user_promptpay`**: Stores the PromptPay ID associated with a user's Discord account for quick QR code generation and payments.
- **`firebase_sites`**: Keeps track of temporary Firebase Hosting sites deployed for interactive bill allocation, including their URLs, creation time, and status.
- **`badges`**: Defines available badges that users can earn (e.g., badge name, description, criteria).
- **`user_badges`**: Tracks which badges each user has earned and when.
- **`payment_streaks`**: Stores information about users' payment streaks, such as current streak length and last payment date, to encourage timely debt settlement.

The exact schemas, including all columns, relationships, and indexing, can be found in the database migration code within the `internal/db/` directory (e.g., `db.go`, `badges.go`, `payment_streak.go`).

## Development

### Database Migration

Database tables are automatically created when the application starts. For detailed schema information, refer to the files in `internal/db/` as noted in the "Database Schema" section.

### Project Structure

The project follows a standard Go project layout:

- `cmd/server/`: Contains the main application entry point for the bot. This includes the Discord bot server and the HTTP server used for webhooks (e.g., from Firebase).
- `internal/`: Contains private application and library code. It's not intended for import by external applications.
  - `config/`: Handles application configuration loading and management from `config.yaml`.
  - `db/`: Manages database connections, schema migrations (including for users, transactions, debts, badges, streaks, etc.), and data access operations for PostgreSQL.
  - `discord/`: Core logic for the Discord bot, including command registration, event handlers, and interaction components.
  - `firebase/`: Contains logic related to Firebase integration. This includes deploying and managing temporary bill allocation web UIs on Firebase Hosting. HTML templates for these UIs (e.g., `bill_allocation.html`) are also managed within this package or its subdirectories.
  - `models/`: Defines data structures and models (e.g., `Transaction`, `User`, `Debt`) used throughout the application.
  - `utils/`: Provides common utility functions used across various parts of the project.
- `pkg/`: Contains library code that's safe to use by external applications, though primarily used internally in this project.
  - `firebase/`: A client package for interacting with Firebase services, acting as a wrapper around the Firebase CLI for site deployment and management.
  - `ocr/`: Package for interacting with an external OCR service to extract text from payment slips.
  - `qrcode/`: Utilities for generating QR codes, particularly for PromptPay payments.
  - `verifier/`: Package for interacting with an external payment slip verification service, as configured in `SlipVerifier` settings.
- `templates/`: (Often located within `internal/firebase/templates/` or a similar path) Contains HTML/CSS/JS templates, such as the one for the interactive bill allocation webpage deployed to Firebase. If not top-level, its location is typically tied to the package that uses it (e.g., `internal/firebase`).
- `tools/`: Includes utility scripts for development purposes, such as database setup scripts (`start_db.sh`, `drop_and_create_db.sh`).

## Stopping the Bot

-   **If running locally via `make run` or `go run`:** Press `CTRL+C` in the terminal.
-   **If running via Docker:**
    ```bash
    docker stop billing-bot
    ```
    (Assuming your container is named `billing-bot`).

## License

MIT License

## Author

Created by [oatsaysai](https://github.com/oatsaysai)
