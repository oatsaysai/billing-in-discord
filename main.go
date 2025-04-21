package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	pp "github.com/Frontware/promptpay"
	"github.com/bwmarrin/discordgo"
	"github.com/spf13/viper"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
)

type Person struct {
	Name    string
	Amounts []float64
}

var (
	dg *discordgo.Session
)

func genQR(s *discordgo.Session, m *discordgo.MessageCreate) {

	tempMsg := m.Message.Content
	tempArr := strings.Split(tempMsg, "\n")

	targetQR := ""

	// สร้าง regular expression สำหรับจับเฉพาะ user ID
	re := regexp.MustCompile(`<@(\d+)>`)

	for _, msg := range tempArr {

		if strings.Contains(msg, "!genQR") {
			data := strings.Split(msg, " ")
			targetQR = data[1]
		} else {
			fmt.Println(msg)

			// หา matches ทั้งหมดในข้อความ
			matches := re.FindAllStringSubmatch(msg, -1)

			// เก็บ user ID เป็น slice ของ string
			var userIds []string
			for _, match := range matches {
				if len(match) > 1 {
					userIds = append(userIds, match[1])
				}
			}

			var amountFloat64 float64
			data := strings.Split(msg, " ")

			if s, err := strconv.ParseFloat(data[1], 64); err == nil {
				amountFloat64 = s
			}

			payment := pp.PromptPay{
				PromptPayID: targetQR,      // Tax-ID/ID Card/E-Wallet
				Amount:      amountFloat64, // Positive amount
			}

			qrcodeStr, err := payment.Gen() // Generate string to be use in QRCode
			if err != nil {
				log.Fatal(err)
			}

			fmt.Println(qrcodeStr)

			qrc, err := qrcode.New(qrcodeStr)
			if err != nil {
				fmt.Printf("could not generate QRCode: %v", err)
				return
			}

			filename := data[0] + ".jpg"

			w, err := standard.New(filename)
			if err != nil {
				fmt.Printf("standard.New failed: %v", err)
				return
			}

			// save file
			if err = qrc.Save(w); err != nil {
				fmt.Printf("could not save image: %v", err)
			}

			// Open the file
			file, err := os.Open(filename)
			if err != nil {
				panic(err)
			}
			defer file.Close()
			defer os.Remove(filename)

			// Read the entire file into a byte slice
			fileData, err := io.ReadAll(file)
			if err != nil {
				panic(err)
			}

			// Create a new bytes.Buffer and write the data to it
			var buffer bytes.Buffer
			_, err = buffer.Write(fileData)
			if err != nil {
				panic(err)
			}

			for _, userID := range userIds {
				_, err = s.ChannelFileSendWithMessage(
					m.ChannelID,
					fmt.Sprintf("<@%s> %s บาท", userID, data[1]),
					filename,
					bytes.NewBuffer(buffer.Bytes()),
				)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}
}

func callBill(s *discordgo.Session, m *discordgo.MessageCreate, genQR bool) {

	tempMsg := m.Message.Content
	tempArr := strings.Split(tempMsg, "\n")

	targetQR := ""
	balances := make(map[string]float64)

	// สร้าง regular expression สำหรับจับเฉพาะ user ID
	re := regexp.MustCompile(`<@(\d+)>`)

	var payeeID int
	err := dbPool.QueryRow(
		context.Background(),
		`SELECT id FROM users WHERE discord_id = $1`,
		m.Author.ID,
	).Scan(&payeeID)
	if err != nil {
		log.Printf("Payee not found for discord_id %s, adding to database", m.Author.ID)
		err = dbPool.QueryRow(
			context.Background(),
			`INSERT INTO users (discord_id) VALUES ($1) RETURNING id`,
			m.Author.ID,
		).Scan(&payeeID)
		if err != nil {
			log.Printf("Failed to add payee %s: %v", m.Author.ID, err)
			return
		}
	}

	for _, msg := range tempArr {
		if strings.Contains(msg, "!calBill") || strings.Contains(msg, "!updateDept") {
			data := strings.Split(msg, " ")
			if genQR {
				targetQR = data[1]
			}
		} else {
			// หา matches ทั้งหมดในข้อความ
			matches := re.FindAllStringSubmatch(msg, -1)

			// เก็บ user ID เป็น slice ของ string
			var userIds []string
			for _, match := range matches {
				if len(match) > 1 {
					userIds = append(userIds, match[1])
				}
			}

			// // แสดงผล user ID ทั้งหมดที่พบ
			// for _, userId := range userIds {
			// 	fmt.Println("Found user ID:", userId)
			// }

			parts := strings.Fields(msg)
			price, _ := strconv.ParseFloat(parts[1], 64)

			// คำนวณยอดเงินต่อคน
			amountPerPerson := price / float64(len(userIds))

			// บันทึกยอดเงินลงใน map
			for _, person := range userIds {
				balances[person] += amountPerPerson

				// Get user IDs from the database
				var payerID int
				err := dbPool.QueryRow(
					context.Background(),
					`SELECT id FROM users WHERE discord_id = $1`,
					person,
				).Scan(&payerID)
				if err != nil {
					log.Printf("Payer not found for discord_id %s, adding to database", person)
					err = dbPool.QueryRow(
						context.Background(),
						`INSERT INTO users (discord_id) VALUES ($1) RETURNING id`,
						person,
					).Scan(&payerID)
					if err != nil {
						log.Printf("Failed to add payer %s: %v", person, err)
						continue
					}
				}

				// Save the transaction
				_, err = dbPool.Exec(
					context.Background(),
					`INSERT INTO transactions (payer_id, payee_id, amount, description) VALUES ($1, $2, $3, $4)`,
					payerID, payeeID, amountPerPerson, parts[0],
				)
				if err != nil {
					log.Printf("Failed to save transaction for user %s: %v", person, err)
					continue
				}
			}
		}
	}

	for person, balance := range balances {
		fmt.Printf("%s: %.2f\n", person, balance)

		// Update DB
		// Get user IDs from the database
		var payerID int
		err := dbPool.QueryRow(
			context.Background(),
			`SELECT id FROM users WHERE discord_id = $1`,
			person,
		).Scan(&payerID)
		if err != nil {
			log.Printf("Failed to find user %s: %v", person, err)
			continue
		}

		// Update the debt
		err = updateUserDebt(payerID, payeeID, balance)
		if err != nil {
			log.Printf("Failed to update debt for user %s: %v", person, err)
			continue
		}

		if genQR {
			payment := pp.PromptPay{
				PromptPayID: targetQR, // Tax-ID/ID Card/E-Wallet
				Amount:      balance,  // Positive amount
			}

			qrcodeStr, err := payment.Gen() // Generate string to be use in QRCode
			if err != nil {
				log.Fatal(err)
			}

			fmt.Println(qrcodeStr)

			qrc, err := qrcode.New(qrcodeStr)
			if err != nil {
				fmt.Printf("could not generate QRCode: %v", err)
				return
			}

			filename := person + ".jpg"

			w, err := standard.New(filename)
			if err != nil {
				fmt.Printf("standard.New failed: %v", err)
				return
			}

			// save file
			if err = qrc.Save(w); err != nil {
				fmt.Printf("could not save image: %v", err)
			}

			// Open the file
			file, err := os.Open(filename)
			if err != nil {
				panic(err)
			}
			defer file.Close()
			defer os.Remove(filename)

			// Read the entire file into a byte slice
			fileData, err := io.ReadAll(file)
			if err != nil {
				panic(err)
			}

			// Create a new bytes.Buffer and write the data to it
			var buffer bytes.Buffer
			_, err = buffer.Write(fileData)
			if err != nil {
				panic(err)
			}

			_, err = s.ChannelFileSendWithMessage(
				m.ChannelID,
				fmt.Sprintf("<@%s> %.2f บาท", person, balance),
				filename,
				bytes.NewBuffer(buffer.Bytes()),
			)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

type VerifySlipParams struct {
	RefNbr string `json:"refNbr"`
	Amount string `json:"amount"`
	Token  string `json:"token"`
}

type VerifySlipResponse struct {
	Success       bool   `json:"success"`
	StatusMessage string `json:"statusMessage"`
	Data          struct {
		ReceivingBank string `json:"receivingBank"`
		SendingBank   string `json:"sendingBank"`
		TransRef      string `json:"transRef"`
		TransDate     string `json:"transDate"`
		TransTime     string `json:"transTime"`
		Sender        struct {
			DisplayName string `json:"displayName"`
			Name        string `json:"name"`
			Account     struct {
				Value string `json:"value"`
			} `json:"account"`
		} `json:"sender"`
		Receiver struct {
			DisplayName string `json:"displayName"`
			Name        string `json:"name"`
		} `json:"receiver"`
		Amount float64 `json:"amount"`
		Ref1   string  `json:"ref1"`
		Ref2   string  `json:"ref2"`
		Ref3   string  `json:"ref3"`
	} `json:"data"`
}

func VerifySlip(url string, data VerifySlipParams) (*VerifySlipResponse, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))

	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "PostmanRuntime/7.42.0")
	// req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 10,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil // Follow redirect
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err

	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	fmt.Printf("res body: %s", body)

	var res VerifySlipResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func showTxByPayerID(s *discordgo.Session, m *discordgo.MessageCreate) {
	re := regexp.MustCompile(`<@(\d+)>`)
	matches := re.FindStringSubmatch(m.Message.Content)
	if len(matches) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Invalid command format. Please mention a user, e.g., `!showTxByPayer @user`.")
		return
	}
	payerDiscordID := matches[1]

	var payerID int
	err := dbPool.QueryRow(
		context.Background(),
		`SELECT id FROM users WHERE discord_id = $1`,
		payerDiscordID,
	).Scan(&payerID)
	if err != nil {
		log.Printf("Payer not found for discord_id %s: %v", payerDiscordID, err)
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("No transactions found for <@%s>.", payerDiscordID))
		return
	}

	rows, err := dbPool.Query(
		context.Background(),
		`SELECT t.id, t.amount, t.description, t.already_paid, t.created_at, u.discord_id AS payee_discord_id
         FROM transactions t
         JOIN users u ON t.payee_id = u.id
         WHERE t.payer_id = $1 AND t.already_paid = false
         ORDER BY t.created_at DESC
         LIMIT 20`,
		payerID,
	)
	if err != nil {
		log.Printf("Failed to retrieve transactions for payer_id %d: %v", payerID, err)
		s.ChannelMessageSend(m.ChannelID, "Failed to retrieve transactions. Please try again later.")
		return
	}
	defer rows.Close()

	var response strings.Builder
	response.WriteString(fmt.Sprintf("Latest 20 transactions where <@%s> is the payer:\n", payerDiscordID))
	for rows.Next() {
		var txID int
		var amount float64
		var description, payeeDiscordID string
		var alreadyPaid bool
		var createdAt time.Time
		if err := rows.Scan(&txID, &amount, &description, &alreadyPaid, &createdAt, &payeeDiscordID); err != nil {
			log.Printf("Failed to scan transaction row: %v", err)
			continue
		}
		paidStatus := "Unpaid"
		if alreadyPaid {
			paidStatus = "Paid"
		}
		response.WriteString(fmt.Sprintf("- TxID: %d | %.2f บาท for %s to <@%s> on %s | Status: %s\n", txID, amount, description, payeeDiscordID, createdAt.Format(time.RFC3339), paidStatus))
	}

	if response.Len() == 0 {
		response.WriteString("No transactions found.")
	}

	s.ChannelMessageSend(m.ChannelID, response.String())
}

func showTxByPayeeID(s *discordgo.Session, m *discordgo.MessageCreate) {
	re := regexp.MustCompile(`<@(\d+)>`)
	matches := re.FindStringSubmatch(m.Message.Content)
	if len(matches) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Invalid command format. Please mention a user, e.g., `!showTxByPayee @user`.")
		return
	}
	payeeDiscordID := matches[1]

	var payeeID int
	err := dbPool.QueryRow(
		context.Background(),
		`SELECT id FROM users WHERE discord_id = $1`,
		payeeDiscordID,
	).Scan(&payeeID)
	if err != nil {
		log.Printf("Payee not found for discord_id %s: %v", payeeDiscordID, err)
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("No transactions found for <@%s>.", payeeDiscordID))
		return
	}

	rows, err := dbPool.Query(
		context.Background(),
		`SELECT t.id, t.amount, t.description, t.already_paid, t.created_at, u.discord_id AS payer_discord_id
         FROM transactions t
         JOIN users u ON t.payer_id = u.id
         WHERE t.payee_id = $1 AND t.already_paid = false
         ORDER BY t.created_at DESC
         LIMIT 20`,
		payeeID,
	)
	if err != nil {
		log.Printf("Failed to retrieve transactions for payee_id %d: %v", payeeID, err)
		s.ChannelMessageSend(m.ChannelID, "Failed to retrieve transactions. Please try again later.")
		return
	}
	defer rows.Close()

	var response strings.Builder
	response.WriteString(fmt.Sprintf("Latest 20 transactions where <@%s> is the payee:\n", payeeDiscordID))
	for rows.Next() {
		var txID int
		var amount float64
		var description, payeeDiscordID string
		var alreadyPaid bool
		var createdAt time.Time
		if err := rows.Scan(&txID, &amount, &description, &alreadyPaid, &createdAt, &payeeDiscordID); err != nil {
			log.Printf("Failed to scan transaction row: %v", err)
			continue
		}
		paidStatus := "Unpaid"
		if alreadyPaid {
			paidStatus = "Paid"
		}
		response.WriteString(fmt.Sprintf("- TxID: %d | %.2f บาท for %s from <@%s> on %s | Status: %s\n", txID, amount, description, payeeDiscordID, createdAt.Format(time.RFC3339), paidStatus))
	}

	if response.Len() == 0 {
		response.WriteString("No transactions found.")
	}

	s.ChannelMessageSend(m.ChannelID, response.String())
}

func updatePaidStatus(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Extract the transaction IDs from the message
	parts := strings.Fields(m.Message.Content)
	if len(parts) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Invalid command format. Please use `!updatePaid <tx_id1>,<tx_id2>,...`.")
		return
	}

	// Split the transaction IDs by comma
	txIDs := strings.Split(parts[1], ",")

	for _, txIDStr := range txIDs {
		txID, err := strconv.Atoi(strings.TrimSpace(txIDStr))
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Invalid transaction ID: %s. Please provide valid numbers.", txIDStr))
			continue
		}

		// Retrieve the transaction details
		var payerID, payeeID int
		var amount float64
		var alreadyPaid bool
		err = dbPool.QueryRow(
			context.Background(),
			`SELECT payer_id, payee_id, amount, already_paid FROM transactions WHERE id = $1`,
			txID,
		).Scan(&payerID, &payeeID, &amount, &alreadyPaid)
		if err != nil {
			log.Printf("Failed to retrieve transaction with id %d: %v", txID, err)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Transaction with ID %d not found.", txID))
			continue
		}

		// Check if the transaction is already marked as paid
		if alreadyPaid {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Transaction with ID %d is already marked as paid.", txID))
			continue
		}

		// Update the transaction's paid status
		_, err = dbPool.Exec(
			context.Background(),
			`UPDATE transactions SET already_paid = TRUE WHERE id = $1`,
			txID,
		)
		if err != nil {
			log.Printf("Failed to update transaction with id %d: %v", txID, err)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to update the transaction status for ID %d. Please try again later.", txID))
			continue
		}

		// Update the user_debts table
		_, err = dbPool.Exec(
			context.Background(),
			`UPDATE user_debts
             SET amount = amount - $1, updated_at = CURRENT_TIMESTAMP
             WHERE debtor_id = $2 AND creditor_id = $3`,
			amount, payerID, payeeID,
		)
		if err != nil {
			log.Printf("Failed to update user_debts for payer_id %d and payee_id %d: %v", payerID, payeeID, err)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to update the user debts for transaction ID %d. Please try again later.", txID))
			continue
		}

		// Notify the user of the successful update
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Transaction with ID %d has been marked as paid, and user debts have been updated.", txID))
	}
}

func listUserDebtsByDebtorID(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Extract the mentioned user ID from the message
	re := regexp.MustCompile(`<@(\d+)>`)
	matches := re.FindStringSubmatch(m.Message.Content)
	if len(matches) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Invalid command format. Please mention a user, e.g., `!listDebts @user`.")
		return
	}
	debtorDiscordID := matches[1]

	// Retrieve the debtor's ID from the database
	var debtorID int
	err := dbPool.QueryRow(
		context.Background(),
		`SELECT id FROM users WHERE discord_id = $1`,
		debtorDiscordID,
	).Scan(&debtorID)
	if err != nil {
		log.Printf("Debtor not found for discord_id %s: %v", debtorDiscordID, err)
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("No debts found for <@%s>.", debtorDiscordID))
		return
	}

	// Query all debts for the debtor
	rows, err := dbPool.Query(
		context.Background(),
		`SELECT ud.amount, u.discord_id AS creditor_discord_id
         FROM user_debts ud
         JOIN users u ON ud.creditor_id = u.id
         WHERE ud.debtor_id = $1`,
		debtorID,
	)
	if err != nil {
		log.Printf("Failed to retrieve debts for debtor_id %d: %v", debtorID, err)
		s.ChannelMessageSend(m.ChannelID, "Failed to retrieve debts. Please try again later.")
		return
	}
	defer rows.Close()

	// Build the response message
	var response strings.Builder
	response.WriteString(fmt.Sprintf("Debts for <@%s>:\n", debtorDiscordID))
	for rows.Next() {
		var amount float64
		var creditorDiscordID string
		if err := rows.Scan(&amount, &creditorDiscordID); err != nil {
			log.Printf("Failed to scan debt row: %v", err)
			continue
		}
		response.WriteString(fmt.Sprintf("- Owes %.2f บาท to <@%s>\n", amount, creditorDiscordID))
	}

	// Check if no debts were found
	if response.Len() == 0 {
		response.WriteString("No debts found.")
	}

	// Send the response to the Discord channel
	s.ChannelMessageSend(m.ChannelID, response.String())
}

func listUserDebtsByCreditorID(creditorDiscordID string) (string, error) {
	// Retrieve the creditor's ID from the database
	var creditorID int
	err := dbPool.QueryRow(
		context.Background(),
		`SELECT id FROM users WHERE discord_id = $1`,
		creditorDiscordID,
	).Scan(&creditorID)
	if err != nil {
		log.Printf("Creditor not found for discord_id %s: %v", creditorDiscordID, err)
		return "", fmt.Errorf("no debts found for <@%s>", creditorDiscordID)
	}

	// Query all debts for the creditor
	rows, err := dbPool.Query(
		context.Background(),
		`SELECT ud.amount, u.discord_id AS debtor_discord_id
         FROM user_debts ud
         JOIN users u ON ud.debtor_id = u.id
         WHERE ud.creditor_id = $1`,
		creditorID,
	)
	if err != nil {
		log.Printf("Failed to retrieve debts for creditor_id %d: %v", creditorID, err)
		return "", fmt.Errorf("failed to retrieve debts. Please try again later")
	}
	defer rows.Close()

	// Build the response message
	var response strings.Builder
	response.WriteString(fmt.Sprintf("Debts owed to <@%s>:\n", creditorDiscordID))
	for rows.Next() {
		var amount float64
		var debtorDiscordID string
		if err := rows.Scan(&amount, &debtorDiscordID); err != nil {
			log.Printf("Failed to scan debt row: %v", err)
			continue
		}
		response.WriteString(fmt.Sprintf("- <@%s> owes %.2f บาท\n", debtorDiscordID, amount))
	}

	// Check if no debts were found
	if response.Len() == 0 {
		response.WriteString("No debts found.")
	}

	return response.String(), nil
}

func listUserDebtsByDebtorIDMessageHandler(s *discordgo.Session, m *discordgo.MessageCreate) {

	if m.Author.ID == s.State.User.ID { // Ignore the bot's own messages
		return
	}

	if strings.Contains(m.Message.Content, "!listDebtsByCreditor") {
		re := regexp.MustCompile(`<@(\d+)>`)
		matches := re.FindStringSubmatch(m.Message.Content)
		if len(matches) < 2 {
			s.ChannelMessageSend(m.ChannelID, "Invalid command format. Please mention a user, e.g., `!listDebtsByCreditor @user`.")
			return
		}
		creditorDiscordID := matches[1]

		response, err := listUserDebtsByCreditorID(creditorDiscordID)
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, err.Error())
			return
		}

		s.ChannelMessageSend(m.ChannelID, response)
	}
}

func showHelp(s *discordgo.Session, m *discordgo.MessageCreate) {
	helpMessage := `
**Available Commands:**
- **!genQR <PromptPayID>**: Generate a QR code for PromptPay payments.
	Example:
	
		!genQR 0891234567
		ค่าขนม 100 @Oat
- **!calBill <PromptPayID>**: Calculate and split a bill among mentioned users and generate QR codes.
	Example:
	
		!calBill 0891234567
		ค่าขนม 100 @Oat @Bom
- **!updateDept**: Update debts without generating QR codes.
- **!showTxByPayer @user**: Show the latest 20 transactions where the mentioned user is the payer.
- **!showTxByPayee @user**: Show the latest 20 transactions where the mentioned user is the payee.
- **!updatePaid <tx_id1>,<tx_id2>,...**: Mark a transaction as paid and update debts.
- **!listDebtsByDebtor @user**: List all debts for the mentioned user as a debtor.
- **!listDebtsByCreditor @user**: List all debts owed to the mentioned user as a creditor.
- **!help**: Show this help message.
`
	s.ChannelMessageSend(m.ChannelID, helpMessage)
}

func messageHandler(s *discordgo.Session, m *discordgo.MessageCreate) {

	if m.Author.ID == s.State.User.ID { // Ignore the bot's own messages
		return
	}

	if strings.Contains(m.Message.Content, "!genQR") {
		genQR(s, m)
	} else if strings.Contains(m.Message.Content, "!calBill") {
		callBill(s, m, true)
	} else if strings.Contains(m.Message.Content, "!updateDept") {
		callBill(s, m, false)
	} else if strings.Contains(m.Message.Content, "!showTxByPayer") {
		showTxByPayerID(s, m)
	} else if strings.Contains(m.Message.Content, "!showTxByPayee") {
		showTxByPayeeID(s, m)
	} else if strings.Contains(m.Message.Content, "!updatePaid") {
		updatePaidStatus(s, m)
	} else if strings.Contains(m.Message.Content, "!listDebtsByDebtor") {
		listUserDebtsByDebtorID(s, m)
	} else if strings.Contains(m.Message.Content, "!listDebtsByCreditor") {
		listUserDebtsByDebtorIDMessageHandler(s, m)
	} else if strings.Contains(m.Message.Content, "!help") {
		showHelp(s, m)
	}
}

// DiscordConnect make a new connection to Discord
func DiscordConnect() (err error) {
	dg, err = discordgo.New("Bot " + viper.GetString("DiscordBot.Token"))
	if err != nil {
		log.Println("FATAL: error creating Discord session,", err)
		return
	}

	log.Println("INFO: Bot is Opening")

	dg.AddHandler(messageHandler)

	// Open Websocket
	err = dg.Open()
	if err != nil {
		log.Println("FATAL: Error Open():", err)
		return
	}

	_, err = dg.User("@me")
	if err != nil {
		// Login unsuccessful
		log.Println("FATAL:", err)
		return
	}

	// Login successful
	log.Println("INFO: Bot is now running. Press CTRL-C to exit.")
	// initRoutine()
	return nil
}

func removeIndex(s []float64, index int) []float64 {
	return append(s[:index], s[index+1:]...)
}

func DownloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

func init() {
	viper.SetConfigName("config")
	viper.AddConfigPath(".")

	viper.SetDefault("Log.Level", "debug")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("unable to read config: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	initPostgresPool()
	defer dbPool.Close()

	migrateDatabase() // Run database migrations

	DiscordConnect()
	<-make(chan struct{})
}
