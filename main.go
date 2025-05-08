package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

var (
	dg *discordgo.Session
)

type BillItem struct {
	Description string
	Amount      float64
	SharedWith  []string // Slice of Discord User IDs
}

var (
	userMentionRegex      = regexp.MustCompile(`<@!?(\d+)>`)
	txIDRegex             = regexp.MustCompile(`\(TxID:\s?(\d+)\)`)
	txIDsRegex            = regexp.MustCompile(`\(TxIDs:\s?([\d,]+)\)`)
	firebaseSiteNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,20}[a-z0-9]$`)
	jsonRegex             = regexp.MustCompile(`(?s)\{.*\}`) // Regex to extract JSON part
)

// FirebaseSite stores info about a deployed Firebase site
type FirebaseSite struct {
	ID                int       `json:"id"`
	UserDbID          int       `json:"user_db_id"`
	FirebaseProjectID string    `json:"firebase_project_id"`
	SiteName          string    `json:"site_name"` // The unique ID used for firebase hosting:site:<site_name>
	SiteURL           string    `json:"site_url"`
	CreatedAt         time.Time `json:"created_at"`
	Status            string    `json:"status"` // e.g., "active", "disabled"
}

// Struct for parsing Firebase CLI JSON output for site creation
type FirebaseSiteCreateResult struct {
	Status string `json:"status"`
	Result struct {
		Name       string `json:"name"`       // This is typically the site ID, e.g., projects/my-project/sites/my-site-id
		DefaultUrl string `json:"defaultUrl"` // The full URL
		Type       string `json:"type"`
	} `json:"result"`
	Error struct { // Added to potentially catch structured errors from Firebase JSON
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// Struct for parsing Firebase CLI JSON output for deployment
type FirebaseDeployResult struct {
	Status string                 `json:"status"`
	Result map[string]interface{} `json:"result"` // Can be complex, e.g. {"hosting": {"site-name": "url"}}
	Error  struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// messageHandler routes incoming messages to appropriate handlers.
func messageHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Prioritize slip verification replies
	if m.MessageReference != nil && m.MessageReference.MessageID != "" && len(m.Attachments) > 0 {
		go handleSlipVerification(s, m)
		return
	}

	// Handle regular commands
	content := strings.TrimSpace(m.Content)
	args := strings.Fields(content)
	if len(args) == 0 {
		return
	}
	command := strings.ToLower(args[0])

	switch {
	case command == "!bill":
		go handleBillCommand(s, m)
	case command == "!qr":
		go handleQrCommand(s, m)
	case command == "!mydebts":
		go handleMyDebts(s, m)
	case command == "!owedtome", command == "!mydues":
		go handleOwedToMe(s, m)
	case command == "!debts" && len(args) > 1 && userMentionRegex.MatchString(args[1]):
		go handleDebtsOfUser(s, m, args[1:])
	case command == "!dues" && len(args) > 1 && userMentionRegex.MatchString(args[1]):
		go handleDuesForUser(s, m, args[1:])
	case command == "!paid":
		go updatePaidStatus(s, m)
	case command == "!request":
		go handleRequestPayment(s, m)
	case command == "!upweb":
		go handleUpWebCommand(s, m)
	case command == "!downweb":
		go handleDownWebCommand(s, m)
	case command == "!help":
		go handleHelpCommand(s, m, args)
	}
}

// --- Parsing Helper Functions ---
func parseQrArgs(content string) (amount float64, toUser string, description string, promptPayID string, err error) {
	normalizedContent := strings.ToLower(content)
	trimmedContent := strings.TrimSpace(strings.TrimPrefix(normalizedContent, "!qr "))
	parts := strings.Fields(trimmedContent)
	if len(parts) < 6 {
		return 0, "", "", "", fmt.Errorf("รูปแบบ `!qr` ไม่ถูกต้อง โปรดใช้: `!qr <จำนวนเงิน> to @user for <รายละเอียด> <YourPromptPayID>`")
	}
	parsedAmount, amountErr := strconv.ParseFloat(parts[0], 64)
	if amountErr != nil {
		return 0, "", "", "", fmt.Errorf("จำนวนเงิน '%s' ไม่ถูกต้อง", parts[0])
	}
	amount = parsedAmount
	if parts[1] != "to" {
		return 0, "", "", "", fmt.Errorf("ไม่พบคำว่า 'to'")
	}
	if !userMentionRegex.MatchString(parts[2]) {
		return 0, "", "", "", fmt.Errorf("ต้องระบุ @user ที่ถูกต้องหลัง 'to'")
	}
	toUser = userMentionRegex.FindStringSubmatch(parts[2])[1]
	if parts[3] != "for" {
		return 0, "", "", "", fmt.Errorf("ไม่พบคำว่า 'for'")
	}
	promptPayID = parts[len(parts)-1]
	if !regexp.MustCompile(`^(\d{10}|\d{13}|ewallet-\d+)$`).MatchString(promptPayID) {
		return 0, "", "", "", fmt.Errorf("PromptPayID '%s' ไม่ถูกต้องที่ส่วนท้าย", promptPayID)
	}
	if len(parts)-1 <= 4 {
		return 0, "", "", "", fmt.Errorf("รายละเอียดห้ามว่าง")
	}
	description = strings.Join(parts[4:len(parts)-1], " ")
	if description == "" {
		return 0, "", "", "", fmt.Errorf("รายละเอียดห้ามว่าง")
	}
	return amount, toUser, description, promptPayID, nil
}

func parseRequestPaymentArgs(content string) (debtorDiscordID string, creditorPromptPayID string, err error) {
	parts := strings.Fields(content)
	if len(parts) != 3 {
		return "", "", fmt.Errorf("รูปแบบไม่ถูกต้อง โปรดใช้: `!request @ลูกหนี้ <PromptPayIDของคุณ>`")
	}
	if !userMentionRegex.MatchString(parts[1]) {
		return "", "", fmt.Errorf("ต้องระบุ @ลูกหนี้ ที่ถูกต้อง")
	}
	debtorDiscordID = userMentionRegex.FindStringSubmatch(parts[1])[1]
	creditorPromptPayID = parts[2]
	if !regexp.MustCompile(`^(\d{10}|\d{13}|ewallet-\d+)$`).MatchString(creditorPromptPayID) {
		return "", "", fmt.Errorf("PromptPayID '%s' ของคุณไม่ถูกต้อง", creditorPromptPayID)
	}
	return debtorDiscordID, creditorPromptPayID, nil
}

func parseBillItem(line string) (amount float64, description string, mentions []string, err error) {
	normalizedContent := strings.ToLower(line)
	parts := strings.Fields(normalizedContent)
	if len(parts) < 4 {
		return 0, "", nil, fmt.Errorf("รูปแบบรายการไม่ถูกต้อง โปรดใช้: `<จำนวนเงิน> for <รายละเอียด> with @user1 @user2...`")
	}
	amountNum, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, "", nil, fmt.Errorf("จำนวนเงินในรายการไม่ถูกต้อง: '%s'", parts[0])
	}
	amount = amountNum
	forIndex, withIndex := -1, -1
	for i, p := range parts {
		if p == "for" && forIndex == -1 {
			forIndex = i
		}
		if p == "with" && withIndex == -1 {
			withIndex = i
		}
	}
	if forIndex != 1 || withIndex == -1 || forIndex >= withIndex {
		return 0, "", nil, fmt.Errorf("รูปแบบรายการไม่ถูกต้อง: โปรดตรวจสอบว่า 'for' อยู่หลังจำนวนเงิน และ 'with' อยู่หลังรายละเอียด")
	}
	description = strings.Join(parts[forIndex+1:withIndex], " ")
	if description == "" {
		return 0, "", nil, fmt.Errorf("รายละเอียดรายการห้ามว่าง")
	}
	mentionParts := parts[withIndex+1:]
	if len(mentionParts) == 0 {
		return 0, "", nil, fmt.Errorf("ไม่ได้ระบุผู้ใช้สำหรับรายการ '%s'", description)
	}
	var foundMentions []string
	for _, p := range mentionParts {
		if userMentionRegex.MatchString(p) {
			foundMentions = append(foundMentions, userMentionRegex.FindStringSubmatch(p)[1])
		} else {
			return 0, "", nil, fmt.Errorf("การระบุผู้ใช้ไม่ถูกต้อง '%s' ในรายการ '%s'", p, description)
		}
	}
	if len(foundMentions) == 0 {
		return 0, "", nil, fmt.Errorf("ไม่ได้ระบุผู้ใช้ที่ถูกต้องสำหรับรายการ '%s'", description)
	}
	mentions = foundMentions
	return amount, description, mentions, nil
}

// --- General Helper Functions ---
func sendErrorMessage(s *discordgo.Session, channelID string, message string) {
	log.Printf("ERROR to user (Channel: %s): %s", channelID, message)
	_, err := s.ChannelMessageSend(channelID, fmt.Sprintf("⚠️ เกิดข้อผิดพลาด: %s", message))
	if err != nil {
		log.Printf("Failed to send error message to Discord: %v", err)
	}
}

func getOrCreateDBUser(discordID string) (int, error) {
	var dbUserID int
	err := dbPool.QueryRow(context.Background(), `SELECT id FROM users WHERE discord_id = $1`, discordID).Scan(&dbUserID)
	if err == nil {
		return dbUserID, nil
	}
	err = dbPool.QueryRow(context.Background(), `INSERT INTO users (discord_id) VALUES ($1) RETURNING id`, discordID).Scan(&dbUserID)
	if err != nil {
		log.Printf("Failed to insert user %s: %v", discordID, err)
		// Attempt to fetch again in case of concurrent insert
		fetchErr := dbPool.QueryRow(context.Background(), `SELECT id FROM users WHERE discord_id = $1`, discordID).Scan(&dbUserID)
		if fetchErr == nil {
			return dbUserID, nil
		}
		return 0, fmt.Errorf("ไม่สามารถสร้างหรือค้นหาผู้ใช้ %s ในฐานข้อมูล: %w (ข้อผิดพลาดการเพิ่มข้อมูลเดิม: %v)", discordID, fetchErr, err)
	}
	return dbUserID, nil
}

func generateAndSendQrCode(s *discordgo.Session, channelID string, promptPayNum string, amount float64, targetUserDiscordID string, description string, txIDs []int) {
	payment := pp.PromptPay{PromptPayID: promptPayNum, Amount: amount}
	qrcodeStr, err := payment.Gen()
	if err != nil {
		sendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถสร้างข้อมูล QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Error generating PromptPay string for %s: %v", targetUserDiscordID, err)
		return
	}
	qrc, err := qrcode.New(qrcodeStr)
	if err != nil {
		sendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถสร้างรูปภาพ QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Error generating QR code for %s: %v", targetUserDiscordID, err)
		return
	}
	filename := fmt.Sprintf("qr_%s_%d.jpg", targetUserDiscordID, time.Now().UnixNano())
	fileWriter, err := standard.New(filename)
	if err != nil {
		sendErrorMessage(s, channelID, fmt.Sprintf("การสร้างรูปภาพ QR สำหรับ <@%s> ล้มเหลวภายในระบบ", targetUserDiscordID))
		log.Printf("standard.New failed for QR %s: %v", targetUserDiscordID, err)
		return
	}
	if err = qrc.Save(fileWriter); err != nil {
		sendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถบันทึกรูปภาพ QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Could not save QR image for %s: %v", targetUserDiscordID, err)
		os.Remove(filename) // Clean up
		return
	}
	file, err := os.Open(filename)
	if err != nil {
		sendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถส่งรูปภาพ QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Could not open QR image %s for sending: %v", filename, err)
		os.Remove(filename) // Clean up
		return
	}
	defer file.Close()
	defer os.Remove(filename) // Clean up

	txIDString := ""
	if len(txIDs) == 1 {
		txIDString = fmt.Sprintf(" (TxID: %d)", txIDs[0])
	} else if len(txIDs) > 1 {
		var idStrs []string
		for _, id := range txIDs {
			idStrs = append(idStrs, strconv.Itoa(id))
		}
		txIDString = fmt.Sprintf(" (TxIDs: %s)", strings.Join(idStrs, ","))
	}

	msgContent := fmt.Sprintf("<@%s> กรุณาชำระ %.2f บาท สำหรับ \"%s\"%s\nหากต้องการยืนยันการชำระเงิน ตอบกลับข้อความนี้พร้อมแนบสลิปของคุณ", targetUserDiscordID, amount, description, txIDString)
	if description == "" {
		msgContent = fmt.Sprintf("<@%s> กรุณาชำระ %.2f บาท%s\nหากต้องการยืนยันการชำระเงิน ตอบกลับข้อความนี้พร้อมแนบสลิปของคุณ", targetUserDiscordID, amount, txIDString)
	}

	_, err = s.ChannelFileSendWithMessage(channelID, msgContent, filename, file)
	if err != nil {
		log.Printf("Failed to send QR file for %s: %v", targetUserDiscordID, err)
	}
}

// --- Command Handlers ---
func handleBillCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	lines := strings.Split(strings.TrimSpace(m.Content), "\n")
	if len(lines) < 2 {
		sendErrorMessage(s, m.ChannelID, "รูปแบบ `!bill` ไม่ถูกต้อง ต้องมีอย่างน้อย 2 บรรทัด (บรรทัดแรกคือคำสั่ง บรรทัดถัดไปคือรายการ)")
		return
	}

	firstLineParts := strings.Fields(lines[0])
	if strings.ToLower(firstLineParts[0]) != "!bill" {
		sendErrorMessage(s, m.ChannelID, "บรรทัดแรกต้องขึ้นต้นด้วย `!bill`")
		return
	}
	var promptPayID string
	if len(firstLineParts) > 1 {
		promptPayID = firstLineParts[1]
		if !regexp.MustCompile(`^(\d{10}|\d{13}|ewallet-\d+)$`).MatchString(promptPayID) {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("PromptPayID '%s' ในบรรทัดแรกดูเหมือนจะไม่ถูกต้อง จะดำเนินการต่อโดยไม่สร้าง QR", promptPayID))
			promptPayID = "" // Clear invalid ID
		}
	}

	payeeDiscordID := m.Author.ID
	payeeDbID, err := getOrCreateDBUser(payeeDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", payeeDiscordID))
		return
	}

	userTotalDebts := make(map[string]float64) // payerDiscordID -> totalOwed
	userTxIDs := make(map[string][]int)        // payerDiscordID -> list of TxIDs for this bill
	var billItemsSummary strings.Builder
	billItemsSummary.WriteString(fmt.Sprintf("สรุปบิลโดย <@%s>:\n", m.Author.ID))
	totalBillAmount := 0.0
	hasErrors := false

	for i, line := range lines[1:] {
		lineNum := i + 2 // User-facing line number
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue // Skip empty lines
		}

		amount, description, mentions, parseErr := parseBillItem(trimmedLine)
		if parseErr != nil {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d มีข้อผิดพลาด: %v", lineNum, parseErr))
			hasErrors = true
			continue
		}

		totalBillAmount += amount
		billItemsSummary.WriteString(fmt.Sprintf("- `%.2f` สำหรับ **%s**, หารกับ: ", amount, description))
		for _, uid := range mentions {
			billItemsSummary.WriteString(fmt.Sprintf("<@%s> ", uid))
		}
		billItemsSummary.WriteString("\n")

		amountPerPerson := amount / float64(len(mentions))
		if amountPerPerson < 0.01 && amount > 0 { // Avoid tiny dust amounts
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: จำนวนเงินต่อคนน้อยเกินไป (%.4f)", lineNum, amountPerPerson))
			hasErrors = true
			continue
		}

		for _, payerDiscordID := range mentions {
			payerDbID, dbErr := getOrCreateDBUser(payerDiscordID)
			if dbErr != nil {
				log.Printf("Error DB user %s for item '%s' line %d: %v", payerDiscordID, description, lineNum, dbErr)
				sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาด DB สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true
				continue // Skip this specific payer for this item
			}

			var txID int
			txErr := dbPool.QueryRow(context.Background(),
				`INSERT INTO transactions (payer_id, payee_id, amount, description) VALUES ($1, $2, $3, $4) RETURNING id`,
				payerDbID, payeeDbID, amountPerPerson, description).Scan(&txID)
			if txErr != nil {
				log.Printf("Failed to save transaction for user %s, item '%s' line %d: %v", payerDiscordID, description, lineNum, txErr)
				sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาดในการบันทึก transaction สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true
				continue // Skip this specific payer for this item
			}

			userTotalDebts[payerDiscordID] += amountPerPerson
			userTxIDs[payerDiscordID] = append(userTxIDs[payerDiscordID], txID)

			// Update user_debts table
			debtErr := updateUserDebt(payerDbID, payeeDbID, amountPerPerson)
			if debtErr != nil {
				log.Printf("Failed to update debt for user %s, item '%s' line %d: %v", payerDiscordID, description, lineNum, debtErr)
				sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาดในการอัปเดตยอดหนี้สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true // Mark error, but transaction was saved
			}
		}
	}

	// Send bill summary
	s.ChannelMessageSend(m.ChannelID, billItemsSummary.String())

	if len(userTotalDebts) > 0 {
		var qrSummary strings.Builder
		qrSummary.WriteString(fmt.Sprintf("\n**ยอดรวมทั้งสิ้น: %.2f บาท**\n", totalBillAmount))
		if hasErrors {
			qrSummary.WriteString("⚠️ *มีข้อผิดพลาดเกิดขึ้นในการประมวลผลบางรายการ โปรดตรวจสอบข้อความก่อนหน้า*\n")
		}
		qrSummary.WriteString("\nสร้าง QR Code สำหรับชำระเงิน:\n")
		s.ChannelMessageSend(m.ChannelID, qrSummary.String())

		for payerDiscordID, totalOwed := range userTotalDebts {
			if promptPayID != "" && totalOwed > 0.009 { // Only send QR if ID provided and amount is significant
				relevantTxIDs := userTxIDs[payerDiscordID]
				generateAndSendQrCode(s, m.ChannelID, promptPayID, totalOwed, payerDiscordID, fmt.Sprintf("ยอดรวมจากบิลนี้โดย <@%s>", m.Author.ID), relevantTxIDs)
			}
		}
	} else if !hasErrors {
		s.ChannelMessageSend(m.ChannelID, "ไม่พบรายการที่ถูกต้องในบิล")
	}
}

func handleQrCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	amount, toUserDiscordID, description, promptPayID, err := parseQrArgs(m.Content)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, err.Error())
		return
	}
	payeeDiscordID := m.Author.ID // The one creating the QR is the payee
	payeeDbID, err := getOrCreateDBUser(payeeDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", payeeDiscordID))
		return
	}
	payerDbID, err := getOrCreateDBUser(toUserDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับผู้รับ <@%s>", toUserDiscordID))
		return
	}

	var txID int
	err = dbPool.QueryRow(context.Background(),
		`INSERT INTO transactions (payer_id, payee_id, amount, description) VALUES ($1, $2, $3, $4) RETURNING id`,
		payerDbID, payeeDbID, amount, description).Scan(&txID)
	if err != nil {
		log.Printf("Failed to save transaction for !qr from %s to %s: %v", payeeDiscordID, toUserDiscordID, err)
		sendErrorMessage(s, m.ChannelID, "เกิดข้อผิดพลาดในการบันทึก Transaction")
		return
	}

	err = updateUserDebt(payerDbID, payeeDbID, amount)
	if err != nil {
		// Log but don't halt, transaction is primary
		log.Printf("Failed to update debt for !qr from %s to %s: %v", payeeDiscordID, toUserDiscordID, err)
	}

	generateAndSendQrCode(s, m.ChannelID, promptPayID, amount, toUserDiscordID, description, []int{txID})
}

func handleRequestPayment(s *discordgo.Session, m *discordgo.MessageCreate) {
	debtorDiscordID, creditorPromptPayID, err := parseRequestPaymentArgs(m.Content)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, err.Error())
		return
	}

	creditorDiscordID := m.Author.ID // The one making the request is the creditor

	if debtorDiscordID == creditorDiscordID {
		sendErrorMessage(s, m.ChannelID, "คุณไม่สามารถร้องขอการชำระเงินจากตัวเองได้")
		return
	}
	debtorDbID, err := getOrCreateDBUser(debtorDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับลูกหนี้ <@%s>", debtorDiscordID))
		return
	}
	creditorDbID, err := getOrCreateDBUser(creditorDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", creditorDiscordID))
		return
	}

	var totalDebtAmount float64
	queryTotal := `SELECT COALESCE(SUM(amount), 0) FROM user_debts WHERE debtor_id = $1 AND creditor_id = $2`
	err = dbPool.QueryRow(context.Background(), queryTotal, debtorDbID, creditorDbID).Scan(&totalDebtAmount)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดในการค้นหายอดหนี้รวมที่ <@%s> ค้างชำระกับคุณ", debtorDiscordID))
		log.Printf("Error querying total debt for !request from creditor %s to debtor %s: %v", creditorDiscordID, debtorDiscordID, err)
		return
	}

	if totalDebtAmount <= 0.009 { // Using a small epsilon for float comparison
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("ยอดเยี่ยม! <@%s> ไม่ได้ติดหนี้คุณอยู่", debtorDiscordID))
		return
	}

	// Get unpaid transaction IDs and details to include in the QR message
	unpaidTxIDs, unpaidTxDetails, unpaidTotal, err := getUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID, 10) // Limit details to 10 items
	if err != nil {
		log.Printf("Error fetching transaction details for !request: %v", err)
		// Proceed without detailed Tx list if this fails
	}

	// Sanity check: does sum of unpaid transactions roughly match total debt?
	if !(unpaidTotal > totalDebtAmount-0.01 && unpaidTotal < totalDebtAmount+0.01) {
		log.Printf("Data Inconsistency Alert: Unpaid transactions sum (%.2f) does not match user_debts amount (%.2f) for debtor %d -> creditor %d. Sending QR for total debt without specific TxIDs.", unpaidTotal, totalDebtAmount, debtorDbID, creditorDbID)
		description := fmt.Sprintf("คำร้องขอชำระหนี้คงค้างจาก <@%s> (ยอดรวม)", creditorDiscordID)
		generateAndSendQrCode(s, m.ChannelID, creditorPromptPayID, totalDebtAmount, debtorDiscordID, description, nil) // No specific TxIDs
		return
	}

	description := fmt.Sprintf("คำร้องขอชำระหนี้คงค้างจาก <@%s>", creditorDiscordID)
	if unpaidTxDetails != "" {
		maxDescLen := 1500 // Max length for Discord message component
		detailsHeader := "\nประกอบด้วยรายการ (TxIDs):\n"
		availableSpace := maxDescLen - len(description) - len(detailsHeader) - 50 // Buffer
		if len(unpaidTxDetails) > availableSpace && availableSpace > 0 {
			unpaidTxDetails = unpaidTxDetails[:availableSpace] + "...\n(และรายการอื่นๆ)"
		} else if availableSpace <= 0 {
			unpaidTxDetails = "(แสดงรายการไม่ได้เนื่องจากข้อความยาวเกินไป)"
		}
		description += detailsHeader + unpaidTxDetails
	}

	generateAndSendQrCode(s, m.ChannelID, creditorPromptPayID, totalDebtAmount, debtorDiscordID, description, unpaidTxIDs)
}

func getUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID int, detailLimit int) ([]int, string, float64, error) {
	query := `
        SELECT id, amount, description
        FROM transactions
        WHERE payer_id = $1 AND payee_id = $2 AND already_paid = false
        ORDER BY created_at ASC;
    `
	rows, err := dbPool.Query(context.Background(), query, debtorDbID, creditorDbID)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()

	var details strings.Builder
	var txIDs []int
	var totalAmount float64
	count := 0
	for rows.Next() {
		var id int
		var amount float64
		var description sql.NullString
		if err := rows.Scan(&id, &amount, &description); err != nil {
			return nil, "", 0, err
		}
		descText := description.String
		if !description.Valid || descText == "" {
			descText = "(ไม่มีรายละเอียด)"
		}
		if detailLimit <= 0 || count < detailLimit { // if detailLimit is 0 or less, show all
			details.WriteString(fmt.Sprintf("- `%.2f` บาท: %s (TxID: %d)\n", amount, descText, id))
		} else if count == detailLimit {
			details.WriteString("- ... (และรายการอื่นๆ)\n")
		}
		txIDs = append(txIDs, id)
		totalAmount += amount
		count++
	}
	if count == 0 {
		return nil, "", 0, nil // No unpaid transactions found
	}
	return txIDs, details.String(), totalAmount, nil
}

func queryAndSendDebts(s *discordgo.Session, m *discordgo.MessageCreate, principalDiscordID string, mode string) {
	principalDbID, err := getOrCreateDBUser(principalDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่พบ <@%s> ในฐานข้อมูล", principalDiscordID))
		return
	}
	var query, title string

	// Subquery to get a comma-separated list of recent unpaid transaction details
	transactionDetailsSubquery := `
	WITH RankedTransactionDetails AS (
		SELECT
			t.payer_id,
			t.payee_id,
			t.description || ' (TxID:' || t.id::text || ')' as detail_text,
			ROW_NUMBER() OVER (PARTITION BY t.payer_id, t.payee_id ORDER BY t.created_at DESC, t.id DESC) as rn
		FROM transactions t
		WHERE t.already_paid = false
	)
	SELECT
		rtd.payer_id,
		rtd.payee_id,
		STRING_AGG(rtd.detail_text, '; ' ORDER BY rtd.rn) as details
	FROM RankedTransactionDetails rtd
	WHERE rtd.rn <= 5 -- Limit to 5 most recent details per pair
	GROUP BY rtd.payer_id, rtd.payee_id
	`
	if mode == "debtor" { // principalDiscordID is the one who owes money
		title = fmt.Sprintf("หนี้สินของ <@%s> (ที่ต้องจ่ายคนอื่น):\n", principalDiscordID)
		query = fmt.Sprintf(`
            SELECT ud.amount, u_other.discord_id AS other_party_discord_id,
                   COALESCE(tx_details.details, 'หนี้สินรวม ไม่พบรายการธุรกรรมที่ยังไม่ได้ชำระที่เกี่ยวข้อง') as details
            FROM user_debts ud
            JOIN users u_other ON ud.creditor_id = u_other.id
            LEFT JOIN (
                %s
            ) AS tx_details ON tx_details.payer_id = ud.debtor_id AND tx_details.payee_id = ud.creditor_id
            WHERE ud.debtor_id = $1 AND ud.amount > 0.009
            ORDER BY ud.amount DESC;`, transactionDetailsSubquery)
	} else { // principalDiscordID is the one who is owed money
		title = fmt.Sprintf("ยอดค้างชำระถึง <@%s> (ที่คนอื่นต้องจ่าย):\n", principalDiscordID)
		query = fmt.Sprintf(`
            SELECT ud.amount, u_other.discord_id AS other_party_discord_id,
                   COALESCE(tx_details.details, 'หนี้สินรวม ไม่พบรายการธุรกรรมที่ยังไม่ได้ชำระที่เกี่ยวข้อง') as details
            FROM user_debts ud
            JOIN users u_other ON ud.debtor_id = u_other.id
            LEFT JOIN (
                %s
            ) AS tx_details ON tx_details.payer_id = ud.debtor_id AND tx_details.payee_id = ud.creditor_id
            WHERE ud.creditor_id = $1 AND ud.amount > 0.009
            ORDER BY ud.amount DESC;`, transactionDetailsSubquery)
	}

	rows, err := dbPool.Query(context.Background(), query, principalDbID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลหนี้สินได้")
		log.Printf("Error querying debts (mode: %s) for %s (dbID %d): %v\n--- Query Start ---\n%s\n--- Query End ---", mode, principalDiscordID, principalDbID, err, query)
		return
	}
	defer rows.Close()

	var response strings.Builder
	response.WriteString(title)
	count := 0
	for rows.Next() {
		var amount float64
		var otherPartyDiscordID, details string
		if err := rows.Scan(&amount, &otherPartyDiscordID, &details); err != nil {
			log.Printf("Failed to scan debt row (mode: %s): %v", mode, err)
			continue
		}
		maxDetailLen := 150 // Max length for details string in the summary
		if len(details) > maxDetailLen {
			details = details[:maxDetailLen-3] + "..."
		}
		if mode == "debtor" {
			response.WriteString(fmt.Sprintf("- **%.2f บาท** ให้ <@%s> (รายละเอียดล่าสุด: %s)\n", amount, otherPartyDiscordID, details))
		} else {
			response.WriteString(fmt.Sprintf("- <@%s> เป็นหนี้ **%.2f บาท** (รายละเอียดล่าสุด: %s)\n", otherPartyDiscordID, amount, details))
		}
		count++
	}

	if count == 0 {
		if mode == "debtor" {
			response.WriteString(fmt.Sprintf("<@%s> ไม่มีหนี้สินค้างชำระ! 🎉\n", principalDiscordID))
		} else {
			response.WriteString(fmt.Sprintf("ดูเหมือนว่าทุกคนจะชำระหนี้ให้ <@%s> หมดแล้ว 👍\n", principalDiscordID))
		}
	}
	s.ChannelMessageSend(m.ChannelID, response.String())
}

func handleMyDebts(s *discordgo.Session, m *discordgo.MessageCreate) {
	queryAndSendDebts(s, m, m.Author.ID, "debtor")
}
func handleOwedToMe(s *discordgo.Session, m *discordgo.MessageCreate) {
	queryAndSendDebts(s, m, m.Author.ID, "creditor")
}
func handleDebtsOfUser(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 || !userMentionRegex.MatchString(args[0]) {
		sendErrorMessage(s, m.ChannelID, "รูปแบบไม่ถูกต้อง โปรดใช้ `!debts @user`")
		return
	}
	targetUserDiscordID := userMentionRegex.FindStringSubmatch(args[0])[1]
	queryAndSendDebts(s, m, targetUserDiscordID, "debtor")
}
func handleDuesForUser(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 || !userMentionRegex.MatchString(args[0]) {
		sendErrorMessage(s, m.ChannelID, "รูปแบบไม่ถูกต้อง โปรดใช้ `!dues @user`")
		return
	}
	targetUserDiscordID := userMentionRegex.FindStringSubmatch(args[0])[1]
	queryAndSendDebts(s, m, targetUserDiscordID, "creditor")
}

// --- Slip Verification and Payment Handling ---
func handleSlipVerification(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.MessageReference == nil || m.MessageReference.MessageID == "" || len(m.Attachments) == 0 {
		return
	}
	refMsg, err := s.ChannelMessage(m.ChannelID, m.MessageReference.MessageID)
	if err != nil {
		log.Printf("SlipVerify: Error fetching referenced message %s: %v", m.MessageReference.MessageID, err)
		return
	}
	// Ensure the referenced message is from the bot itself
	if refMsg.Author == nil || refMsg.Author.ID != s.State.User.ID {
		return
	}
	parsedDebtorDiscordID, parsedAmount, parsedTxIDs, err := parseBotQRMessageContent(refMsg.Content)
	if err != nil {
		log.Printf("SlipVerify: Could not parse bot message content: %v", err)
		// Don't send error to user, might be a reply to a non-QR bot message
		return
	}
	log.Printf("SlipVerify: Received slip verification for debtor %s, amount %.2f, TxIDs %v", parsedDebtorDiscordID, parsedAmount, parsedTxIDs)
	slipUploaderID := m.Author.ID
	var slipURL string
	for _, att := range m.Attachments {
		if strings.HasPrefix(strings.ToLower(att.ContentType), "image/") {
			slipURL = att.URL
			break
		}
	}
	if slipURL == "" {
		return // No image attachment found
	}

	// The person uploading the slip should be the one mentioned as the debtor in the QR message
	if slipUploaderID != parsedDebtorDiscordID {
		log.Printf("SlipVerify: Slip uploaded by %s for debtor %s - ignoring (uploader mismatch).", slipUploaderID, parsedDebtorDiscordID)
		// s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("<@%s>, คุณสามารถยืนยันสลิปสำหรับการชำระเงินของ <@%s> เท่านั้น", slipUploaderID, parsedDebtorDiscordID))
		return
	}

	tmpFile := fmt.Sprintf("slip_%s_%s.png", m.ID, parsedDebtorDiscordID) // Unique temp file name
	err = DownloadFile(tmpFile, slipURL)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "ไม่สามารถดาวน์โหลดรูปภาพสลิปเพื่อยืนยันได้")
		log.Printf("SlipVerify: Failed to download slip %s: %v", slipURL, err)
		return
	}
	defer os.Remove(tmpFile)

	verifyResp, err := VerifySlip(parsedAmount, tmpFile)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("การเรียก API ยืนยันสลิปล้มเหลว: %v", err))
		log.Printf("SlipVerify: API call failed for debtor %s, amount %.2f: %v", parsedDebtorDiscordID, parsedAmount, err)
		return
	}

	// Check if amount from slip matches expected amount (with tolerance)
	if !(verifyResp.Data.Amount > parsedAmount-0.01 && verifyResp.Data.Amount < parsedAmount+0.01) {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("จำนวนเงินในสลิป (%.2f) ไม่ตรงกับจำนวนที่คาดไว้ (%.2f)", verifyResp.Data.Amount, parsedAmount))
		return
	}

	intendedPayeeDiscordID := "???" // Placeholder
	// Try to determine intended payee
	if len(parsedTxIDs) > 0 {
		// If TxIDs are present, payee is determined from the first TxID
		payeeDbID, fetchErr := getPayeeDbIdFromTx(parsedTxIDs[0])
		if fetchErr == nil {
			intendedPayeeDiscordID, _ = getDiscordIdFromDbId(payeeDbID)
		}
	} else {
		// If no TxIDs, try to find payee based on debtor and amount
		payee, findErr := findIntendedPayee(parsedDebtorDiscordID, parsedAmount)
		if findErr != nil {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่สามารถระบุผู้รับเงินที่ถูกต้องสำหรับการชำระเงินนี้ได้: %v", findErr))
			log.Printf("SlipVerify: Could not determine intended payee for debtor %s, amount %.2f: %v", parsedDebtorDiscordID, parsedAmount, findErr)
			return
		}
		intendedPayeeDiscordID = payee
	}
	if intendedPayeeDiscordID == "???" || intendedPayeeDiscordID == "" {
		log.Printf("SlipVerify: Critical - Failed to determine intended payee for debtor %s, amount %.2f", parsedDebtorDiscordID, parsedAmount)
		sendErrorMessage(s, m.ChannelID, "เกิดข้อผิดพลาดร้ายแรง: ไม่สามารถระบุผู้รับเงินได้")
		return
	}

	// Process payment based on TxIDs if available
	if len(parsedTxIDs) > 0 {
		log.Printf("SlipVerify: Attempting batch update using TxIDs: %v", parsedTxIDs)
		successCount := 0
		failCount := 0
		var failMessages []string

		for _, txID := range parsedTxIDs {
			err = markTransactionPaidAndUpdateDebt(txID) // This function handles both transaction and user_debt updates
			if err == nil {
				successCount++
			} else {
				failCount++
				failMessages = append(failMessages, fmt.Sprintf("TxID %d (%v)", txID, err))
				log.Printf("SlipVerify: Failed update for TxID %d: %v", txID, err)
			}
		}

		var report strings.Builder
		report.WriteString(fmt.Sprintf(
			"✅ สลิปได้รับการยืนยัน!\n- ผู้จ่าย: <@%s>\n- ผู้รับ: <@%s>\n- จำนวน: %.2f บาท\n- ผู้ส่ง (สลิป): %s (%s)\n- ผู้รับ (สลิป): %s (%s)\n- วันที่ (สลิป): %s\n- เลขอ้างอิง (สลิป): %s\n",
			parsedDebtorDiscordID, intendedPayeeDiscordID, parsedAmount,
			verifyResp.Data.SenderName, verifyResp.Data.SenderID,
			verifyResp.Data.ReceiverName, verifyResp.Data.ReceiverID,
			verifyResp.Data.Date, verifyResp.Data.Ref,
		))
		report.WriteString(fmt.Sprintf("อัปเดตสำเร็จ %d/%d รายการธุรกรรม (TxIDs: %v)\n", successCount, len(parsedTxIDs), parsedTxIDs))
		if failCount > 0 {
			report.WriteString(fmt.Sprintf("⚠️ เกิดข้อผิดพลาด %d รายการ: %s", failCount, strings.Join(failMessages, "; ")))
		}
		s.ChannelMessageSend(m.ChannelID, report.String())
		return

	} else { // No TxIDs, general debt reduction
		log.Printf("SlipVerify: No TxIDs found in message. Attempting general debt reduction for %s paying %s amount %.2f.", parsedDebtorDiscordID, intendedPayeeDiscordID, parsedAmount)

		errReduce := reduceDebtFromPayment(parsedDebtorDiscordID, intendedPayeeDiscordID, parsedAmount)
		if errReduce != nil {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดในการลดหนี้สินทั่วไปสำหรับ <@%s> ถึง <@%s>: %v", parsedDebtorDiscordID, intendedPayeeDiscordID, errReduce))
			log.Printf("SlipVerify: Failed general debt reduction for %s to %s (%.2f): %v", parsedDebtorDiscordID, intendedPayeeDiscordID, parsedAmount, errReduce)
			return
		}
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf(
			"✅ สลิปได้รับการยืนยัน & ยอดหนี้สินจาก <@%s> ถึง <@%s> ลดลง %.2f บาท!\n- ผู้ส่ง (สลิป): %s (%s)\n- ผู้รับ (สลิป): %s (%s)\n- วันที่ (สลิป): %s\n- เลขอ้างอิง (สลิป): %s",
			parsedDebtorDiscordID, intendedPayeeDiscordID, parsedAmount,
			verifyResp.Data.SenderName, verifyResp.Data.SenderID,
			verifyResp.Data.ReceiverName, verifyResp.Data.ReceiverID,
			verifyResp.Data.Date, verifyResp.Data.Ref,
		))
	}
}

func getPayeeDbIdFromTx(txID int) (int, error) {
	var payeeDbID int
	query := `SELECT payee_id FROM transactions WHERE id = $1`
	err := dbPool.QueryRow(context.Background(), query, txID).Scan(&payeeDbID)
	if err != nil {
		log.Printf("Error fetching payee DB ID for TxID %d: %v", txID, err)
		return 0, err
	}
	return payeeDbID, nil
}

func getDiscordIdFromDbId(dbUserID int) (string, error) {
	var discordID string
	query := `SELECT discord_id FROM users WHERE id = $1`
	err := dbPool.QueryRow(context.Background(), query, dbUserID).Scan(&discordID)
	if err != nil {
		log.Printf("Error fetching Discord ID for DB User ID %d: %v", dbUserID, err)
		return "", err
	}
	return discordID, nil
}

func findIntendedPayee(debtorDiscordID string, amount float64) (string, error) {
	debtorDbID, err := getOrCreateDBUser(debtorDiscordID)
	if err != nil {
		return "", fmt.Errorf("ไม่พบผู้จ่ายเงิน %s ใน DB: %w", debtorDiscordID, err)
	}

	var payeeDiscordID string
	var count int // To check if query returns exactly one row
	// First, check if there's a single creditor to whom this debtor owes this exact total amount
	query := `
		SELECT u.discord_id, COUNT(*) OVER() as total_matches
		FROM user_debts ud
		JOIN users u ON ud.creditor_id = u.id
		WHERE ud.debtor_id = $1
		  AND ABS(ud.amount - $2::numeric) < 0.01 -- Amount matches total debt closely
		  AND ud.amount > 0.009 -- Debt is significant
		LIMIT 1; -- Only interested if there's one unique match
	`
	err = dbPool.QueryRow(context.Background(), query, debtorDbID, amount).Scan(&payeeDiscordID, &count)
	if err == nil && count == 1 {
		log.Printf("findIntendedPayee: Found single matching creditor %s based on total debt amount %.2f for debtor %s", payeeDiscordID, amount, debtorDiscordID)
		return payeeDiscordID, nil
	}
	if err == nil && count > 1 {
		log.Printf("findIntendedPayee: Ambiguous - Debtor %s owes %.2f to multiple creditors based on total debt amount.", debtorDiscordID, amount)
		// Continue to check individual transactions
	}

	// If not, check for a single unpaid transaction of this amount from this debtor
	query = `
		SELECT u.discord_id, COUNT(*) OVER() as payee_count
		FROM transactions t
		JOIN users u ON t.payee_id = u.id
		WHERE t.payer_id = $1
		  AND ABS(t.amount - $2::numeric) < 0.01 -- Transaction amount matches closely
		  AND t.already_paid = false
		GROUP BY u.discord_id -- Group by payee in case of multiple tx to same payee
		LIMIT 2; -- Fetch up to 2 to detect ambiguity
	`
	rows, err := dbPool.Query(context.Background(), query, debtorDbID, amount)
	if err != nil {
		log.Printf("findIntendedPayee: Error querying transactions for debtor %s amount %.2f: %v", debtorDiscordID, amount, err)
		return "", fmt.Errorf("เกิดข้อผิดพลาดในการค้นหาผู้รับเงิน")
	}
	defer rows.Close()

	var potentialPayees []string
	for rows.Next() {
		var payee string
		var payeeCount int                                     // This will be total distinct payees from the GROUP BY
		if err := rows.Scan(&payee, &payeeCount); err != nil { // payeeCount here is not what we expect from OVER()
			log.Printf("findIntendedPayee: Error scanning transaction row: %v", err)
			continue
		}
		potentialPayees = append(potentialPayees, payee)
	}

	if len(potentialPayees) == 1 {
		log.Printf("findIntendedPayee: Found single matching payee %s based on transaction amount %.2f for debtor %s", potentialPayees[0], amount, debtorDiscordID)
		return potentialPayees[0], nil
	}

	if len(potentialPayees) > 1 {
		log.Printf("findIntendedPayee: Ambiguous - Found multiple potential payees (%v) based on transaction amount %.2f for debtor %s", potentialPayees, amount, debtorDiscordID)
		return "", fmt.Errorf("พบผู้รับเงินที่เป็นไปได้หลายคนสำหรับจำนวนเงินนี้ โปรดใช้คำสั่ง `!paid <TxID>` โดยผู้รับเงิน")
	}

	log.Printf("findIntendedPayee: Could not determine unique intended payee for debtor %s, amount %.2f", debtorDiscordID, amount)
	return "", fmt.Errorf("ไม่สามารถระบุผู้รับเงินที่แน่นอนสำหรับยอดนี้ได้ โปรดให้ผู้รับเงินยืนยันด้วย `!paid <TxID>` หรือตอบกลับ QR ที่มี TxID")
}

func reduceDebtFromPayment(debtorDiscordID, payeeDiscordID string, amount float64) error {
	debtorDbID, err := getOrCreateDBUser(debtorDiscordID)
	if err != nil {
		return fmt.Errorf("ไม่พบผู้จ่ายเงิน %s ใน DB: %w", debtorDiscordID, err)
	}
	payeeDbID, err := getOrCreateDBUser(payeeDiscordID)
	if err != nil {
		return fmt.Errorf("ไม่พบผู้รับเงิน %s ใน DB: %w", payeeDiscordID, err)
	}

	tx, err := dbPool.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("ไม่สามารถเริ่ม Transaction ได้: %w", err)
	}
	defer tx.Rollback(context.Background()) // Rollback if commit isn't called

	result, err := tx.Exec(context.Background(),
		`UPDATE user_debts SET amount = amount - $1, updated_at = CURRENT_TIMESTAMP
         WHERE debtor_id = $2 AND creditor_id = $3 AND amount > 0.009`, // only update if there's existing debt
		amount, debtorDbID, payeeDbID)

	if err != nil {
		return fmt.Errorf("เกิดข้อผิดพลาดขณะอัปเดตหนี้สินรวม: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// This could mean the debt was already 0, or became < 0 due to overpayment.
		// If it became <0 and we want to zero it out, we could do another update.
		// For now, just log. If debt was paid, `user_debts.amount` would be <= 0.
		log.Printf("Debt reduction update affected 0 rows for debtor %d paying creditor %d amount %.2f. Debt might be zero or negative already, or there was no debt record.", debtorDbID, payeeDbID, amount)
		// Optionally, ensure it doesn't go negative or create a debt if none existed (though should not happen with `amount > 0.009` guard)
		// One strategy could be to set to 0 if amount - $1 < 0
		zeroResult, errZero := tx.Exec(context.Background(),
			`UPDATE user_debts SET amount = 0, updated_at = CURRENT_TIMESTAMP
		   WHERE debtor_id = $1 AND creditor_id = $2 AND amount > 0.009 AND amount < $3`, // Only zero if original amount was less than payment
			debtorDbID, payeeDbID, amount)
		if errZero != nil {
			log.Printf("Warning/Error trying to zero out remaining debt for debtor %d creditor %d amount %.2f: %v", debtorDbID, payeeDbID, amount, errZero)
		} else if zeroResult.RowsAffected() > 0 {
			log.Printf("Zeroed out remaining debt for debtor %d paying creditor %d (Payment %.2f)", debtorDbID, payeeDbID, amount)
		}
	}

	if err = tx.Commit(context.Background()); err != nil {
		return fmt.Errorf("ไม่สามารถ Commit Transaction ได้: %w", err)
	}

	log.Printf("General debt reduction successful: Debtor %d, Creditor %d, Amount %.2f", debtorDbID, payeeDbID, amount)
	return nil
}

func parseBotQRMessageContent(content string) (debtorDiscordID string, amount float64, txIDs []int, err error) {
	// Regex to capture debtor ID and amount
	// Example: <@12345> กรุณาชำระ 100.50 บาท ...
	re := regexp.MustCompile(`<@!?(\d+)> กรุณาชำระ ([\d.]+) บาท`)
	matches := re.FindStringSubmatch(content)
	if len(matches) < 3 {
		return "", 0, nil, fmt.Errorf("เนื้อหาข้อความไม่ตรงกับรูปแบบข้อความ QR ของบอท (ไม่พบ debtor/amount)")
	}

	debtorDiscordID = matches[1]
	parsedAmount, parseErr := strconv.ParseFloat(matches[2], 64)
	if parseErr != nil {
		return "", 0, nil, fmt.Errorf("ไม่สามารถแยกวิเคราะห์จำนวนเงินจากข้อความ QR ของบอท: %v", parseErr)
	}
	amount = parsedAmount

	// Try to parse multiple TxIDs: (TxIDs: 1,2,3)
	txsMatch := txIDsRegex.FindStringSubmatch(content)
	if len(txsMatch) == 2 { // txsMatch[0] is full match, txsMatch[1] is the capture group "1,2,3"
		idStrings := strings.Split(txsMatch[1], ",")
		txIDs = make([]int, 0, len(idStrings))
		for _, idStr := range idStrings {
			trimmedIDStr := strings.TrimSpace(idStr)
			if parsedTxID, txErr := strconv.Atoi(trimmedIDStr); txErr == nil {
				txIDs = append(txIDs, parsedTxID)
			} else {
				log.Printf("Warning: Failed to parse TxID '%s' from multi-ID list: %v", trimmedIDStr, txErr)
				// Potentially return error here if strict parsing is needed
			}
		}
		if len(txIDs) > 0 {
			return debtorDiscordID, amount, txIDs, nil
		}
		// If parsing failed for all, fall through to single TxID or no TxID
	}

	// Try to parse single TxID: (TxID: 123)
	txMatch := txIDRegex.FindStringSubmatch(content)
	if len(txMatch) == 2 { // txMatch[0] is full match, txMatch[1] is the capture group "123"
		if parsedTxID, txErr := strconv.Atoi(txMatch[1]); txErr == nil {
			txIDs = []int{parsedTxID} // Return as a slice with one element
			return debtorDiscordID, amount, txIDs, nil
		} else {
			log.Printf("Warning: Failed to parse single TxID '%s': %v", txMatch[1], txErr)
			// Potentially return error here
		}
	}

	// If no TxID regex matched, or parsing failed, return with nil txIDs
	return debtorDiscordID, amount, nil, nil
}

// --- Firebase Helper Functions ---
func generateRandomHex(n int) (string, error) {
	bytesVal := make([]byte, n)
	if _, err := rand.Read(bytesVal); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytesVal), nil
}

func runFirebaseCommand(workingDir string, commandArgs ...string) ([]byte, error) {
	firebaseCliPath := viper.GetString("Firebase.CliPath")
	if firebaseCliPath == "" {
		firebaseCliPath = "firebase" // Assume in PATH if not configured
	}

	cmd := exec.Command(firebaseCliPath, commandArgs...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	saKeyPath := viper.GetString("Firebase.ServiceAccountKeyPath")
	currentEnv := os.Environ()
	if saKeyPath != "" {
		absSaKeyPath, err := filepath.Abs(saKeyPath)
		if err != nil {
			log.Printf("Warning: Could not get absolute path for ServiceAccountKeyPath '%s': %v. Firebase CLI might not authenticate correctly.", saKeyPath, err)
		} else {
			foundGac := false
			for i, envVar := range currentEnv {
				if strings.HasPrefix(envVar, "GOOGLE_APPLICATION_CREDENTIALS=") {
					currentEnv[i] = "GOOGLE_APPLICATION_CREDENTIALS=" + absSaKeyPath
					foundGac = true
					break
				}
			}
			if !foundGac {
				currentEnv = append(currentEnv, "GOOGLE_APPLICATION_CREDENTIALS="+absSaKeyPath)
			}
			// log.Printf("Using ServiceAccountKeyPath for Firebase: %s", absSaKeyPath) // Can be noisy
		}
	}
	cmd.Env = currentEnv

	log.Printf("Executing Firebase command: %s %s (in dir: %s)", firebaseCliPath, strings.Join(commandArgs, " "), workingDir)
	output, err := cmd.CombinedOutput()

	if err != nil {
		errMsg := fmt.Sprintf("Firebase command [%s %s] failed.\nError: %v\nCLI Output:\n%s", firebaseCliPath, strings.Join(commandArgs, " "), err, string(output))
		log.Println(errMsg)
		return output, fmt.Errorf("firebase command execution failed: %w", err)
	}

	log.Printf("Firebase command success: %s %s\nOutput (first 200 chars): %.200s", firebaseCliPath, strings.Join(commandArgs, " "), string(output))
	return output, nil
}

// cleanFirebaseError tries to extract a structured error message from Firebase JSON output.
// If it can't, it returns the original error string or a generic message.
func cleanFirebaseError(originalErr error, output []byte) string {
	jsonStr := jsonRegex.FindString(string(output))
	if jsonStr != "" {
		var firebaseError struct {
			Status string `json:"status"`
			Error  struct {
				Message string `json:"message"`
				Name    string `json:"name"`
				Code    int    `json:"code"`
				Details []struct {
					Type     string `json:"@type"`
					Reason   string `json:"reason"`
					Domain   string `json:"domain"`
					Metadata struct {
						Service string `json:"service"`
					} `json:"metadata"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &firebaseError); err == nil {
			if firebaseError.Error.Message != "" {
				return firebaseError.Error.Message
			}
			if firebaseError.Status == "error" { // Fallback if message is empty but status is error
				return fmt.Sprintf("Firebase operation reported status: %s (no detailed message in JSON error field)", firebaseError.Status)
			}
		}
	}
	// Fallback to the original error if present
	if originalErr != nil {
		unwrappedErr := originalErr
		if e, ok := originalErr.(interface{ Unwrap() error }); ok {
			unwrappedErr = e.Unwrap()
		}
		return unwrappedErr.Error()
	}
	// Fallback to a snippet of the output if no other error info is available
	if len(output) > 0 {
		maxLength := 200
		if len(output) < maxLength {
			maxLength = len(output)
		}
		return fmt.Sprintf("Unknown Firebase CLI error. Output snippet: %s", string(output[:maxLength]))
	}
	return "Unknown Firebase CLI error with no output."
}

func handleUpWebCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	mainProjectID := viper.GetString("Firebase.MainProjectID")
	if mainProjectID == "" {
		sendErrorMessage(s, m.ChannelID, "Firebase MainProjectID is not configured for the bot. Please contact the administrator.")
		log.Println("!upweb error: Firebase.MainProjectID not set in config.")
		return
	}

	args := strings.Fields(m.Content)
	userPrefix := ""
	if len(args) > 1 {
		userPrefix = strings.ToLower(args[1])
		if !firebaseSiteNameRegex.MatchString(userPrefix) {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Invalid custom prefix '%s'. Use lowercase letters, numbers, and hyphens (3-20 chars, not starting/ending with hyphen).", userPrefix))
			return
		}
	}

	userDbID, err := getOrCreateDBUser(m.Author.ID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "Could not retrieve your user information from the database.")
		return
	}

	siteNameSuffix, err := generateRandomHex(3)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "Failed to generate a unique site ID component.")
		log.Printf("!upweb error: failed to generate random hex: %v", err)
		return
	}

	botPrefix := viper.GetString("Firebase.SiteNamePrefix")
	var siteNameBuilder strings.Builder
	siteNameBuilder.WriteString(botPrefix)
	if userPrefix != "" {
		siteNameBuilder.WriteString("-" + userPrefix)
	}
	siteNameBuilder.WriteString("-" + siteNameSuffix)
	siteName := siteNameBuilder.String()

	tempDeployDir, err := os.MkdirTemp("", "paysplitter-firebase-deploy-")
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "Failed to create temporary directory for deployment.")
		log.Printf("!upweb error: MkdirTemp failed: %v", err)
		return
	}
	defer os.RemoveAll(tempDeployDir)

	publicContentDir := "public_html"
	publicDirPath := filepath.Join(tempDeployDir, publicContentDir)
	if err := os.Mkdir(publicDirPath, 0755); err != nil {
		sendErrorMessage(s, m.ChannelID, "Failed to create public content directory.")
		log.Printf("!upweb error: Mkdir publicContentDir failed: %v", err)
		return
	}

	authorTag := m.Author.Username
	if m.Author.Discriminator != "0" && m.Author.Discriminator != "" {
		authorTag += "#" + m.Author.Discriminator
	}
	htmlContent := fmt.Sprintf(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Site by %s</title><style>body{font-family:'Arial',sans-serif;display:flex;flex-direction:column;justify-content:center;align-items:center;height:100vh;margin:0;background-color:#f0f2f5;color:#333;text-align:center;padding:20px;box-sizing:border-box}.container{background-color:white;padding:30px 40px;border-radius:8px;box-shadow:0 4px 12px rgba(0,0,0,0.15);max-width:600px}h1{color:#007bff;margin-bottom:.5em}p{font-size:1.1em;line-height:1.6}strong{color:#555}footer{margin-top:30px;font-size:.9em;color:#777}</style></head><body><div class="container"><h1>🎉 Welcome! 🎉</h1><p>This site was generated by PaySplitter Bot.</p><p>Requested by: <strong>%s</strong></p><p>Site Name: <strong>%s</strong></p><p>Deployed at: %s</p></div><footer>PaySplitter Bot & Firebase Hosting</footer></body></html>`, authorTag, authorTag, siteName, time.Now().Local().Format(time.RFC1123)) // Using Local time for display

	if err := os.WriteFile(filepath.Join(publicDirPath, "index.html"), []byte(htmlContent), 0644); err != nil {
		sendErrorMessage(s, m.ChannelID, "Failed to write index.html.")
		log.Printf("!upweb error: WriteFile index.html failed: %v", err)
		return
	}

	firebaseJsonContent := fmt.Sprintf(`{"hosting":{"site":"%s","public":"%s","ignore":["firebase.json","**/.*","**/node_modules/**"]}}`, siteName, publicContentDir)
	if err := os.WriteFile(filepath.Join(tempDeployDir, "firebase.json"), []byte(firebaseJsonContent), 0644); err != nil {
		sendErrorMessage(s, m.ChannelID, "Failed to write firebase.json.")
		log.Printf("!upweb error: WriteFile firebase.json failed: %v", err)
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("⏳ Attempting to create Firebase site `%s`...", siteName))

	createArgs := []string{"hosting:sites:create", siteName, "--project", mainProjectID, "--json"}
	output, err := runFirebaseCommand("", createArgs...)
	if err != nil { // This 'err' is from exec.Command, check output for Firebase specific errors
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Failed to create Firebase site `%s`. CLI Error: %s", siteName, cleanFirebaseError(err, output)))
		log.Printf("!upweb error: hosting:sites:create failed for %s. Error: %v. Output: %s", siteName, err, string(output))
		return
	}

	var createResult FirebaseSiteCreateResult
	jsonStr := jsonRegex.FindString(string(output))
	if jsonStr == "" {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("No JSON found in Firebase site creation response for `%s`. Output: %s", siteName, string(output)))
		log.Printf("!upweb error: No JSON in sites:create output for %s. Output: %s", siteName, string(output))
		return
	}

	if err := json.Unmarshal([]byte(jsonStr), &createResult); err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Failed to parse Firebase site creation JSON response for `%s`. JSON: %s", siteName, jsonStr))
		log.Printf("!upweb error: unmarshal sites:create output failed: %v. JSON: %s. Full output: %s", err, jsonStr, string(output))
		return
	}

	if createResult.Status != "success" || createResult.Result.DefaultUrl == "" {
		errMsg := cleanFirebaseError(nil, []byte(jsonStr))                           // Try to get specific error from JSON
		if errMsg == "" || strings.HasPrefix(errMsg, "Unknown Firebase CLI error") { // if cleanFirebaseError didn't find a specific message
			errMsg = fmt.Sprintf("Status: %s", createResult.Status)
			if createResult.Error.Message != "" {
				errMsg += fmt.Sprintf(", Error: %s", createResult.Error.Message)
			}
		}
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Firebase site creation for `%s` was not successful. %s", siteName, errMsg))
		log.Printf("!upweb error: sites:create non-success for %s. Full JSON: %+v. Raw output: %s", siteName, createResult, string(output))
		return
	}
	siteURL := createResult.Result.DefaultUrl
	// Firebase site ID is usually the last part of the Name field, e.g. "projects/my-project/sites/my-site-id"
	siteIDFromName := siteName // Default to user-generated siteName
	if createResult.Result.Name != "" {
		parts := strings.Split(createResult.Result.Name, "/")
		if len(parts) > 0 {
			siteIDFromName = parts[len(parts)-1]
		}
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("✅ Site `%s` created. Attempting to deploy content...", siteIDFromName))

	deployArgs := []string{"deploy", "--project", mainProjectID, "--only", "hosting:" + siteIDFromName, "--json", "--force"}
	output, err = runFirebaseCommand(tempDeployDir, deployArgs...)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Failed to deploy to Firebase site `%s`. CLI Error: %s", siteIDFromName, cleanFirebaseError(err, output)))
		log.Printf("!upweb error: deploy failed for %s. Error: %v. Output: %s", siteIDFromName, err, string(output))
		return
	}

	var deployResult FirebaseDeployResult
	jsonStr = jsonRegex.FindString(string(output))
	if jsonStr == "" {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("No JSON found in Firebase deploy response for `%s`. Output: %s", siteIDFromName, string(output)))
		log.Printf("!upweb error: No JSON in deploy output for %s. Output: %s", siteIDFromName, string(output))
		return
	}
	if err := json.Unmarshal([]byte(jsonStr), &deployResult); err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Failed to parse Firebase deploy JSON response for `%s`. JSON: %s", siteIDFromName, jsonStr))
		log.Printf("!upweb error: unmarshal deploy output failed: %v. JSON: %s. Full output: %s", err, jsonStr, string(output))
		return
	}
	if deployResult.Status != "success" {
		errMsg := cleanFirebaseError(nil, []byte(jsonStr))
		if errMsg == "" || strings.HasPrefix(errMsg, "Unknown Firebase CLI error") {
			errMsg = fmt.Sprintf("Status: %s", deployResult.Status)
			if deployResult.Error.Message != "" {
				errMsg += fmt.Sprintf(", Error: %s", deployResult.Error.Message)
			}
		}
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Firebase deploy for `%s` was not successful. %s", siteIDFromName, errMsg))
		log.Printf("!upweb error: deploy non-success for %s. Full JSON: %+v. Raw output: %s", siteIDFromName, deployResult, string(output))
		return
	}

	_, err = dbPool.Exec(context.Background(),
		`INSERT INTO firebase_sites (user_db_id, firebase_project_id, site_name, site_url, status) VALUES ($1, $2, $3, $4, 'active')`,
		userDbID, mainProjectID, siteIDFromName, siteURL) // Store the site ID from Firebase response if different
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Site `%s` deployed to <%s>, but failed to save info to DB. Please report this to an admin.", siteIDFromName, siteURL))
		log.Printf("!upweb error: failed to insert into firebase_sites for site %s, URL %s: %v", siteIDFromName, siteURL, err)
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("✅ Successfully deployed! Your site is live at: <%s>\nTo take it down, use: `!downweb %s`", siteURL, siteIDFromName))
}

func handleDownWebCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	args := strings.Fields(m.Content)
	if len(args) < 2 {
		sendErrorMessage(s, m.ChannelID, "Usage: `!downweb <site_name>`\n`<site_name>` is the ID like `psweb-myprefix-xxxxxx` given when you used `!upweb`.")
		return
	}
	siteNameToDown := args[1] // This is the site_id (e.g., lnwzaa-d989e3)

	userDbID, err := getOrCreateDBUser(m.Author.ID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "Could not retrieve your user information from the database.")
		return
	}

	var site FirebaseSite
	err = dbPool.QueryRow(context.Background(),
		`SELECT id, firebase_project_id, site_name, site_url FROM firebase_sites 
         WHERE user_db_id = $1 AND site_name = $2 AND status = 'active'`,
		userDbID, siteNameToDown).Scan(&site.ID, &site.FirebaseProjectID, &site.SiteName, &site.SiteURL)

	if err != nil {
		if err == sql.ErrNoRows {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("No active site named `%s` found under your account, or it's already been taken down.", siteNameToDown))
		} else {
			sendErrorMessage(s, m.ChannelID, "Error querying database for your site information.")
			log.Printf("!downweb error: db query failed for site %s, user %d: %v", siteNameToDown, userDbID, err)
		}
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("⏳ Attempting to disable site `%s` (%s)...", site.SiteName, site.SiteURL))

	// `firebase hosting:disable` usually takes the site ID.
	// The `--project` flag is often implicit if you're authenticated to the correct project,
	// but it's good practice to include it.
	// `--force` is often used instead of `--confirm` for non-interactive disabling.
	disableArgs := []string{"hosting:disable", "--site", site.SiteName, "--project", site.FirebaseProjectID, "--force", "--json"}
	output, err := runFirebaseCommand("", disableArgs...)
	if err != nil { // This err is from exec.Command
		// Check if Firebase CLI indicates it's already disabled or some other non-fatal condition
		outputStr := strings.ToLower(string(output))
		jsonStr := jsonRegex.FindString(string(output))
		var fbErrorResp struct {
			Status string `json:"status"`
			Error  struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if jsonStr != "" {
			_ = json.Unmarshal([]byte(jsonStr), &fbErrorResp)
		}

		if strings.Contains(outputStr, "has been disabled") || strings.Contains(outputStr, "is already disabled") || (fbErrorResp.Error.Message != "" && (strings.Contains(strings.ToLower(fbErrorResp.Error.Message), "already disabled") || strings.Contains(strings.ToLower(fbErrorResp.Error.Message), "has been disabled"))) {
			log.Printf("!downweb: Site %s (%s) was already disabled or reported as such by Firebase. Proceeding to update DB. Output: %s", site.SiteName, site.SiteURL, string(output))
		} else {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Failed to disable Firebase site `%s`. Error: %s", site.SiteName, cleanFirebaseError(err, output)))
			log.Printf("!downweb error: hosting:disable failed for %s (%s). Full Error: %v. Output: %s", site.SiteName, site.SiteURL, err, string(output))
			return
		}
	} else {
		// Even on success from exec.Command, check Firebase JSON output for status
		jsonStr := jsonRegex.FindString(string(output))
		var fbSuccessResp struct {
			Status string `json:"status"`
		}
		if jsonStr != "" {
			if err := json.Unmarshal([]byte(jsonStr), &fbSuccessResp); err == nil {
				if fbSuccessResp.Status != "success" {
					sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Firebase reported non-success for disabling site `%s`. Status: %s", site.SiteName, fbSuccessResp.Status))
					log.Printf("!downweb error: hosting:disable non-success for site %s. Output: %s", site.SiteName, string(output))
					return
				}
			} else {
				log.Printf("!downweb warning: could not parse JSON from successful disable command for site %s. Output: %s", site.SiteName, string(output))
			}
		} else {
			log.Printf("!downweb warning: no JSON found in successful disable command output for site %s. Output: %s", site.SiteName, string(output))
		}
	}

	_, err = dbPool.Exec(context.Background(),
		`UPDATE firebase_sites SET status = 'disabled', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, site.ID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("Site `%s` likely disabled on Firebase, but failed to update DB record. Please report this to an admin.", site.SiteName))
		log.Printf("!downweb error: failed to update firebase_sites status for ID %d (site %s): %v", site.ID, site.SiteName, err)
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("✅ Successfully disabled site `%s` (%s). It may take a few minutes for changes to fully propagate.", site.SiteName, site.SiteURL))
}

// --- Help Command ---
// (handleHelpCommand - unchanged from previous version with upweb/downweb help)
func handleHelpCommand(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	baseHelp := `
**PaySplitter Bot - วิธีใช้งาน**
นี่คือคำสั่งที่มีให้ใช้งาน หากต้องการความช่วยเหลือเฉพาะคำสั่ง พิมพ์ ` + "`!help <ชื่อคำสั่ง>`" + `

- ` + "`!bill`" + `: จัดการและหารบิล (รองรับหลายบรรทัด)
- ` + "`!qr`" + `: สร้าง QR code สำหรับการชำระเงินเฉพาะรายการ (พร้อม TxID)
- ` + "`!mydebts`" + `: แสดงรายการหนี้สินที่คุณต้องจ่ายให้ผู้อื่น
- ` + "`!owedtome`" + ` (หรือ ` + "`!mydues`" + `): แสดงรายการที่ผู้อื่นเป็นหนี้คุณ
- ` + "`!debts @user`" + `: แสดงหนี้สินของ @user ที่ระบุ
- ` + "`!dues @user`" + `: แสดงรายการที่ผู้อื่นเป็นหนี้ @user ที่ระบุ
- ` + "`!paid <TxID>`" + `: ทำเครื่องหมายว่าธุรกรรม (TxID) ได้รับการชำระแล้ว
- ` + "`!request @user <PromptPayID>`" + `: สร้าง QR code เพื่อร้องขอให้ @user ชำระหนี้คงค้างทั้งหมดให้คุณ (จะลิสต์ TxIDs ที่เกี่ยวข้อง)
- ` + "`!upweb [prefix]`" + `: สร้างเว็บไซต์ง่ายๆ บน Firebase Hosting (สามารถระบุ [prefix] เพื่อใช้เป็นส่วนหนึ่งของชื่อเว็บไซต์)
- ` + "`!downweb <site_name>`" + `: ปิดการใช้งานเว็บไซต์ที่สร้างด้วย !upweb
- ` + "`!help`" + `: แสดงข้อความช่วยเหลือนี้ หรือความช่วยเหลือสำหรับคำสั่งเฉพาะ

**การยืนยันสลิปอัตโนมัติ:**
ตอบกลับข้อความ QR code จากบอทนี้พร้อมแนบรูปภาพสลิปการชำระเงินของคุณ เพื่อยืนยันและทำเครื่องหมายการชำระเงินโดยอัตโนมัติ (หากข้อความ QR มี TxID(s) จะพยายามเคลียร์รายการเหล่านั้นก่อน ถ้าไม่มี TxID หรือเคลียร์ TxID ไม่สำเร็จ จะลดหนี้สินรวม)
`

	billHelp := `
**คำสั่ง ` + "`!bill`" + ` - วิธีใช้งาน**

ใช้สำหรับบันทึกค่าใช้จ่ายและหารหนี้สิน สามารถใส่ได้หลายรายการในคำสั่งเดียวโดยขึ้นบรรทัดใหม่

**รูปแบบ:**
` + "```" + `
!bill [YourOptionalPromptPayID]
<จำนวนเงิน1> for <รายละเอียด1> with @userA @userB...
<จำนวนเงิน2> for <รายละเอียด2> with @userC @userA...
<จำนวนเงิน3> for <รายละเอียด3> with @userB...
` + "```" + `
- บรรทัดแรก: ` + "`!bill`" + ` ตามด้วย PromptPay ID ของคุณ (ไม่จำเป็น) หากใส่ จะมีการสร้าง QR code (พร้อม TxIDs) ให้แต่ละคนที่ติดหนี้คุณจากบิลนี้
- บรรทัดถัดๆ ไป: แต่ละบรรทัดคือรายการค่าใช้จ่าย โดยใช้รูปแบบ ` + "`<จำนวนเงิน> for <รายละเอียด> with @user1 @user2...`" + `
- บอทจะคำนวณยอดรวมที่แต่ละคนต้องจ่ายจากทุกรายการในคำสั่งนี้ แล้วบันทึก transaction แยกสำหรับแต่ละรายการ และอัปเดตยอดหนี้รวมใน ` + "`user_debts`" + `

**ตัวอย่าง:**
` + "```" + `
!bill 0812345678
100 for ค่ากาแฟ with @Bob @Alice
350 for ค่าอาหารเที่ยง with @Alice @Charlie @Bob
50 for ค่าขนม with @Bob
` + "```" + `
บอทจะสรุปยอดที่ Bob, Alice, และ Charlie ต้องจ่ายให้คุณ แล้วส่ง QR code (พร้อม TxIDs ของแต่ละรายการที่เกี่ยวข้อง) ให้แต่ละคน
`

	qrHelp := `
**คำสั่ง ` + "`!qr`" + ` - วิธีใช้งาน**

สร้าง QR code สำหรับจำนวนเงินที่ระบุ ให้ผู้ใช้ที่ระบุชำระเงินให้คุณ (พร้อม TxID)
รูปแบบ: ` + "`!qr <จำนวนเงิน> to @user for <รายละเอียด> <YourPromptPayID>`" + `
- ` + "`<จำนวนเงิน>`" + `: จำนวนเงินที่ผู้ใช้ต้องชำระ
- ` + "`@user`" + `: ผู้ใช้ที่ต้องชำระเงินให้คุณ
- ` + "`<รายละเอียด>`" + `: เหตุผลสำหรับการชำระเงิน
- ` + "`<YourPromptPayID>`" + `: หมายเลขพร้อมเพย์ของคุณ (เบอร์โทรศัพท์, เลขบัตรประชาชน, หรือ ewallet-id) สำหรับ QR code (จำเป็นต้องใส่)

ตัวอย่าง: ` + "`!qr 75 to @Eve for หนี้เก่า 0888777666`" + `
คำสั่งนี้จะบันทึกเป็นหนี้สินจาก @Eve ถึงคุณด้วย และ QR code จะมี TxID ของรายการนี้
`
	debtsHelp := `
**คำสั่งดูหนี้สิน - วิธีใช้งาน**

- ` + "`!mydebts`" + `: แสดงรายการคนที่คุณเป็นหนี้ และจำนวนเงินทั้งหมดสำหรับแต่ละคน พร้อมรายละเอียดธุรกรรมที่ยังไม่ชำระล่าสุด
- ` + "`!owedtome`" + ` (หรือ ` + "`!mydues`" + `): แสดงรายการคนที่ติดหนี้คุณ และจำนวนเงินทั้งหมดสำหรับแต่ละคน พร้อมรายละเอียดธุรกรรมที่ยังไม่ชำระล่าสุด
- ` + "`!debts @user`" + `: แสดงว่า ` + "`@user`" + ` ที่ระบุเป็นหนี้ใครบ้าง
- ` + "`!dues @user`" + `: แสดงว่าใครบ้างที่เป็นหนี้ ` + "`@user`" + ` ที่ระบุ

หมายเลขธุรกรรม (TxID) จะแสดงขึ้น ซึ่งสามารถใช้กับคำสั่ง ` + "`!paid`" + ` ได้
`

	paidHelp := `
**คำสั่ง ` + "`!paid`" + ` - วิธีใช้งาน**

ทำเครื่องหมายธุรกรรมหนึ่งรายการหรือมากกว่าว่าได้รับการชำระแล้ว โดยทั่วไปจะใช้โดยผู้ที่ *ได้รับ* การชำระเงิน
รูปแบบ: ` + "`!paid <TxID1>[,<TxID2>,...]`" + `
- ` + "`<TxID>`" + `: หมายเลขธุรกรรมของหนี้สิน สามารถดู TxID ได้จากคำสั่งดูหนี้สินต่างๆ
- สามารถทำเครื่องหมายหลายธุรกรรมพร้อมกันได้โดยคั่น TxID ด้วยเครื่องหมายจุลภาค (,) โดยไม่มีเว้นวรรค

ตัวอย่าง (รายการเดียว): ` + "`!paid 123`" + `
ตัวอย่าง (หลายรายการ): ` + "`!paid 123,124,125`" + `

คำสั่งนี้จะอัปเดตสถานะธุรกรรมและปรับปรุงยอดหนี้สินรวมระหว่างผู้ใช้
`
	requestPaymentHelp := `
**คำสั่ง ` + "`!request`" + ` - วิธีใช้งาน**

สร้าง QR code เพื่อร้องขอให้ผู้ใช้อื่นชำระหนี้คงค้าง *ทั้งหมด* ที่เขามีต่อคุณ
รูปแบบ: ` + "`!request @ลูกหนี้ <PromptPayIDของคุณ>`" + `
- ` + "`@ลูกหนี้`" + `: คือคนที่คุณต้องการร้องขอให้ชำระเงิน
- ` + "`<PromptPayIDของคุณ>`" + `: คือหมายเลขพร้อมเพย์ *ของคุณ* (ผู้ร้องขอ) เพื่อให้ลูกหนี้ชำระเข้ามา
- จำนวนเงินจะเป็นยอดรวมหนี้สินปัจจุบันทั้งหมดที่ลูกหนี้ค้างคุณโดยอัตโนมัติ
- ข้อความที่ส่งไปพร้อม QR code จะแสดงรายการ TxID ที่เกี่ยวข้องกับหนี้นั้นๆ ด้วย (ถ้าไม่เยอะเกินไป)

ตัวอย่าง: ` + "`!request @Alice 081xxxxxxx`" + `

*หมายเหตุ: การยืนยันสลิปสำหรับ QR นี้จะพยายามเคลียร์ TxID ทั้งหมดที่เกี่ยวข้อง ถ้าสำเร็จ หรือจะลดหนี้สินรวมหากไม่สามารถเคลียร์ TxID ทั้งหมดได้*
`
	upwebHelp := `
**คำสั่ง ` + "`!upweb [prefix]`" + ` - วิธีใช้งาน**

สร้างเว็บไซต์ HTML แบบคงที่ (static HTML) ง่ายๆ และปรับใช้ (deploy) กับ Firebase Hosting ภายใต้โปรเจกต์หลักของบอท
- ` + "`[prefix]`" + ` (ทางเลือก): คำนำหน้า (prefix) ที่คุณกำหนดเองสำหรับชื่อไซต์ของคุณ (ต้องเป็นตัวอักษรพิมพ์เล็ก, ตัวเลข, และขีดกลางเท่านั้น, ยาว 3-20 ตัวอักษร, และห้ามขึ้นต้นหรือลงท้ายด้วยขีดกลาง)
  ชื่อไซต์ที่สมบูรณ์จะเป็น ` + "`<bot_prefix>-[your_prefix]-<random_suffix>`" + ` (เช่น ` + "`psweb-mysite-a1b2c3`" + `)
- บอทจะสร้างไฟล์ ` + "`index.html`" + ` พื้นฐานพร้อมข้อมูลของคุณและเวลาที่ปรับใช้
- เมื่อสำเร็จ บอทจะตอบกลับด้วย URL ของไซต์ที่ใช้งานจริง
- **ข้อกำหนด:** ผู้ดูแลระบบบอทต้องกำหนดค่า Firebase project ID และการรับรองความถูกต้อง (authentication) สำหรับบอทอย่างถูกต้อง

ตัวอย่าง:
` + "`!upweb mycoolsite`" + ` (อาจสร้างไซต์เช่น ` + "`psweb-mycoolsite-a1b2c3.web.app`" + `)
` + "`!upweb`" + ` (อาจสร้างไซต์เช่น ` + "`psweb-d4e5f6.web.app`" + `)
`

	downwebHelp := `
**คำสั่ง ` + "`!downweb <site_name>`" + ` - วิธีใช้งาน**

ปิดการใช้งาน (disable) เว็บไซต์ Firebase Hosting ที่คุณสร้างไว้ก่อนหน้านี้ด้วยคำสั่ง ` + "`!upweb`" + `
- ` + "`<site_name>`" + `: ชื่อเต็มของไซต์ที่คุณต้องการปิดการใช้งาน (เช่น ` + "`psweb-mycoolsite-a1b2c3`" + `)
  คุณจะได้รับชื่อไซต์นี้เมื่อคุณใช้คำสั่ง ` + "`!upweb`" + `
- คำสั่งนี้จะปิดการใช้งานโฮสติ้งสำหรับไซต์นั้น ทำให้ไม่สามารถเข้าถึงได้แบบสาธารณะ แต่จะไม่ลบออกจากโปรเจกต์ Firebase โดยสมบูรณ์ (ผู้ดูแลระบบสามารถเปิดใช้งานใหม่ได้)

ตัวอย่าง:
` + "`!downweb psweb-mycoolsite-a1b2c3`" + `
`

	if len(args) > 1 {
		topic := strings.ToLower(args[1])
		var helpMsg string
		switch topic {
		case "bill":
			helpMsg = billHelp
		case "qr":
			helpMsg = qrHelp
		case "mydebts", "owedtome", "mydues", "debts", "dues":
			helpMsg = debtsHelp
		case "paid":
			helpMsg = paidHelp
		case "request":
			helpMsg = requestPaymentHelp
		case "upweb":
			helpMsg = upwebHelp
		case "downweb":
			helpMsg = downwebHelp
		default:
			helpMsg = "ขออภัย ไม่พบความช่วยเหลือสำหรับหัวข้อ `" + topic + "` ลองพิมพ์ `!help` เพื่อดูรายการคำสั่งหลัก"
		}
		s.ChannelMessageSend(m.ChannelID, helpMsg)
	} else {
		s.ChannelMessageSend(m.ChannelID, baseHelp)
	}
}

// --- Discord Connection ---
func DiscordConnect() (err error) {
	dg, err = discordgo.New("Bot " + viper.GetString("DiscordBot.Token"))
	if err != nil {
		log.Println("FATAL: error creating Discord session,", err)
		return
	}
	// Specify necessary intents. MessageContent is privileged and needs to be enabled in Developer Portal.
	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsGuildMessageReactions | discordgo.IntentsMessageContent

	log.Println("INFO: บอทกำลังเปิด...")
	dg.AddHandler(messageHandler) // Register messageCreate func as a callback for MessageCreate events.
	err = dg.Open()               // Open a websocket connection to Discord and begin listening.
	if err != nil {
		log.Println("FATAL: Error Open():", err)
		return
	}
	// Get bot's own user details to confirm login
	_, err = dg.User("@me")
	if err != nil {
		log.Println("FATAL: Login unsuccessful (cannot get @me):", err)
		return
	}
	log.Println("INFO: บอทกำลังทำงาน กด CTRL-C เพื่อออก")
	return nil
}

// --- Initialization and Main ---
func init() {
	viper.SetConfigName("config")                          // name of config file (without extension)
	viper.AddConfigPath(".")                               // look for config in the working directory
	viper.AutomaticEnv()                                   // read in environment variables that match
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_")) // For env vars like FIREBASE_MAINPROJECTID

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Println("ไม่พบไฟล์ config, จะใช้ค่าเริ่มต้นและตัวแปรสภาพแวดล้อม")
		} else {
			log.Fatalf("CRITICAL: ไม่สามารถอ่าน config: %v\n", err)
		}
	} else {
		log.Println("กำลังใช้ไฟล์ config:", viper.ConfigFileUsed())
	}

	// Set defaults for critical configurations
	viper.SetDefault("DiscordBot.Token", "")
	viper.SetDefault("PostgreSQL.Host", "localhost")
	viper.SetDefault("PostgreSQL.Port", "5432")
	viper.SetDefault("PostgreSQL.User", "postgres")
	viper.SetDefault("PostgreSQL.Password", "")
	viper.SetDefault("PostgreSQL.DBName", "discordbotdb")
	viper.SetDefault("PostgreSQL.Schema", "public") // Default schema
	viper.SetDefault("PostgreSQL.PoolMaxConns", 10)

	// New Firebase defaults
	viper.SetDefault("Firebase.MainProjectID", "")         // MUST be configured by the user
	viper.SetDefault("Firebase.ServiceAccountKeyPath", "") // Optional, for service account auth
	viper.SetDefault("Firebase.SiteNamePrefix", "lnwzaa")  // Bot's prefix for sites
	viper.SetDefault("Firebase.CliPath", "firebase")       // Path to firebase CLI, defaults to "firebase" (in PATH)
}

func main() {
	initPostgresPool() // Initialize database connection pool
	if dbPool != nil {
		defer dbPool.Close() // Ensure pool is closed when main exits
		migrateDatabase()    // Run database migrations
	} else {
		log.Fatal("CRITICAL: dbPool is nil after initPostgresPool. Exiting.")
	}

	// Start Discord bot connection
	err := DiscordConnect()
	if err != nil {
		log.Fatalf("CRITICAL: Failed to connect to Discord: %v", err)
	}

	// Wait indefinitely until a signal is received (e.g., CTRL-C)
	<-make(chan struct{})
	log.Println("Bot shutting down...")
}

// --- Slip Verification API Structs and Functions ---
type VerifySlipResponse struct {
	Message string `json:"message"`
	Data    struct {
		Ref          string  `json:"ref"`
		Date         string  `json:"date"`
		SenderBank   string  `json:"sender_bank"`
		SenderName   string  `json:"sender_name"`
		SenderID     string  `json:"sender_id"`
		ReceiverBank string  `json:"receiver_bank"`
		ReceiverName string  `json:"receiver_name"`
		ReceiverID   string  `json:"receiver_id"`
		Amount       float64 `json:"amount"`
	} `json:"data"`
}

func DownloadFile(filepath string, url string) error {
	resp, err := http.Get(url) //nolint:gosec // URL is from Discord CDN, considered safe enough for this context
	if err != nil {
		return fmt.Errorf("http.Get failed for %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s for %s", resp.Status, url)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("os.Create failed for %s: %w", filepath, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("io.Copy failed for %s: %w", filepath, err)
	}
	log.Printf("Downloaded file from %s to %s", url, filepath)
	return nil
}

func VerifySlip(amount float64, imgPath string) (*VerifySlipResponse, error) {
	log.Printf("VerifySlip called for amount %.2f, image %s", amount, imgPath)

	imgBytes, err := os.ReadFile(imgPath)
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to read image file: %v", err)
	}
	imgBase64 := base64.StdEncoding.EncodeToString(imgBytes)
	payload := map[string]string{
		"img": fmt.Sprintf("data:image/png;base64,%s", imgBase64), // Assuming PNG, adjust if other formats are common
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to marshal JSON: %v", err)
	}
	// URL for slip verification API
	url := fmt.Sprintf("https://slip-c.oiioioiiioooioio.download/api/slip/%.2f", amount)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Custom HTTP client to skip TLS verification and set timeout
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // WARNING: Insecure, use only if you trust the endpoint or for local dev
		},
		Timeout: 20 * time.Second, // Request timeout
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to send request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("VerifySlip: API returned status %d. Body: %s", resp.StatusCode, string(body))
	}

	var result VerifySlipResponse
	if err := json.Unmarshal(body, &result); err != nil {
		// Try to log the body if unmarshal fails for debugging
		return nil, fmt.Errorf("VerifySlip: failed to unmarshal response: %v, body: %s", err, string(body))
	}
	log.Printf("VerifySlip successful for amount %.2f. API Response Ref: %s", amount, result.Data.Ref)
	return &result, nil
}

// --- Payment Update Functions ---
func findMatchingTransaction(payerDiscordID, payeeDiscordID string, amount float64) (int, error) {
	var payerDbID, payeeDbID int
	var err error
	payerDbID, err = getOrCreateDBUser(payerDiscordID)
	if err != nil {
		return 0, fmt.Errorf("payer %s not found in DB for transaction matching: %w", payerDiscordID, err)
	}
	payeeDbID, err = getOrCreateDBUser(payeeDiscordID)
	if err != nil {
		return 0, fmt.Errorf("payee %s not found in DB for transaction matching: %w", payeeDiscordID, err)
	}

	var txID int
	// Find the most recent unpaid transaction matching the criteria
	query := `
SELECT id FROM transactions
WHERE payer_id = $1 AND payee_id = $2
AND ABS(amount - $3::numeric) < 0.01 -- Allow for small floating point discrepancies
AND already_paid = false
ORDER BY created_at DESC LIMIT 1;`
	err = dbPool.QueryRow(context.Background(), query, payerDbID, payeeDbID, amount).Scan(&txID)
	if err != nil {
		if err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows in result set") { // pgx might return "no rows in result set"
			return 0, fmt.Errorf("no matching unpaid transaction found")
		}
		return 0, fmt.Errorf("error querying for matching transaction: %w", err)
	}
	log.Printf("Found matching transaction ID %d for Payer %s, Payee %s, Amount %.2f", txID, payerDiscordID, payeeDiscordID, amount)
	return txID, nil
}

func markTransactionPaidAndUpdateDebt(txID int) error {
	var payerDbID, payeeDbID int
	var amount float64

	// Begin a database transaction
	tx, err := dbPool.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("failed to begin database transaction: %w", err)
	}
	defer tx.Rollback(context.Background()) // Ensure rollback if not committed

	// Retrieve transaction details and lock the row for update
	err = tx.QueryRow(context.Background(),
		`SELECT payer_id, payee_id, amount FROM transactions WHERE id = $1 AND already_paid = false FOR UPDATE`, txID,
	).Scan(&payerDbID, &payeeDbID, &amount)
	if err != nil {
		if err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows in result set") {
			log.Printf("TxID %d already paid or does not exist.", txID)
			// This is not an error for the caller if the goal is to ensure it's paid
			return fmt.Errorf("TxID %d ไม่พบ หรือถูกชำระไปแล้ว", txID) // Return specific error for !paid command
		}
		return fmt.Errorf("failed to retrieve unpaid transaction %d: %w", txID, err)
	}

	// Mark the transaction as paid
	_, err = tx.Exec(context.Background(), `UPDATE transactions SET already_paid = TRUE, paid_at = CURRENT_TIMESTAMP WHERE id = $1`, txID)
	if err != nil {
		return fmt.Errorf("failed to mark transaction %d as paid: %w", txID, err)
	}

	// Update the corresponding user_debts record by subtracting the amount
	// Note: This relies on updateUserDebt which uses ON CONFLICT to add, so we need direct subtraction here.
	_, err = tx.Exec(context.Background(),
		`UPDATE user_debts SET amount = amount - $1, updated_at = CURRENT_TIMESTAMP
WHERE debtor_id = $2 AND creditor_id = $3`,
		amount, payerDbID, payeeDbID)
	if err != nil {
		// Log error but don't necessarily fail the whole operation if transaction was marked paid
		// This could happen if the user_debts record was already cleared or inconsistent.
		log.Printf("Warning/Error updating user_debts for txID %d (debtor %d, creditor %d, amount %.2f): %v. This might be okay if debt was already cleared or manually adjusted.", txID, payerDbID, payeeDbID, amount, err)
	}

	// Commit the database transaction
	if err = tx.Commit(context.Background()); err != nil {
		return fmt.Errorf("failed to commit database transaction for txID %d: %w", txID, err)
	}
	log.Printf("Transaction ID %d marked as paid and debts updated.", txID)
	return nil
}

func updatePaidStatus(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	if len(parts) < 2 {
		sendErrorMessage(s, m.ChannelID, "รูปแบบคำสั่งไม่ถูกต้อง โปรดใช้ `!paid <TxID1>[,<TxID2>,...]`")
		return
	}
	txIDStrings := strings.Split(parts[1], ",") // Allow comma-separated TxIDs
	var successMessages, errorMessages []string

	authorDbID, err := getOrCreateDBUser(m.Author.ID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "ไม่สามารถยืนยันบัญชีผู้ใช้ของคุณสำหรับการดำเนินการนี้")
		return
	}

	for _, txIDStr := range txIDStrings {
		trimmedTxIDStr := strings.TrimSpace(txIDStr)
		if trimmedTxIDStr == "" {
			continue
		}
		txID, err := strconv.Atoi(trimmedTxIDStr)
		if err != nil {
			errorMessages = append(errorMessages, fmt.Sprintf("รูปแบบ TxID '%s' ไม่ถูกต้อง", trimmedTxIDStr))
			continue
		}

		var payeeDbID int
		var alreadyPaid bool
		// Check who is the payee for this transaction and if it's already paid
		err = dbPool.QueryRow(context.Background(),
			`SELECT t.payee_id, t.already_paid FROM transactions t WHERE t.id = $1`, txID).Scan(&payeeDbID, &alreadyPaid)
		if err != nil {
			if err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows in result set") {
				errorMessages = append(errorMessages, fmt.Sprintf("ไม่พบ TxID %d", txID))
			} else {
				errorMessages = append(errorMessages, fmt.Sprintf("เกิดข้อผิดพลาดในการตรวจสอบ TxID %d: %v", txID, err))
				log.Printf("Error fetching payee for TxID %d: %v", txID, err)
			}
			continue
		}

		// Only the designated payee can mark a transaction as paid
		if payeeDbID != authorDbID {
			errorMessages = append(errorMessages, fmt.Sprintf("คุณไม่ใช่ผู้รับเงินที่กำหนดไว้สำหรับ TxID %d", txID))
			continue
		}

		if alreadyPaid {
			// If already marked paid, it's a "success" in terms of state, but inform user.
			successMessages = append(successMessages, fmt.Sprintf("TxID %d ถูกทำเครื่องหมายว่าชำระแล้วอยู่แล้ว", txID))
			continue
		}

		err = markTransactionPaidAndUpdateDebt(txID)
		if err != nil {
			// markTransactionPaidAndUpdateDebt might return "already paid" error if race condition, handle it
			if strings.Contains(err.Error(), "ไม่พบ หรือถูกชำระไปแล้ว") {
				successMessages = append(successMessages, fmt.Sprintf("TxID %d ถูกทำเครื่องหมายว่าชำระแล้ว (อาจจะโดยการดำเนินการอื่น)", txID))
			} else {
				errorMessages = append(errorMessages, fmt.Sprintf("ไม่สามารถอัปเดต TxID %d: %v", txID, err))
			}
		} else {
			successMessages = append(successMessages, fmt.Sprintf("TxID %d ถูกทำเครื่องหมายว่าชำระแล้วเรียบร้อย", txID))
		}
	}

	var response strings.Builder
	if len(successMessages) > 0 {
		response.WriteString("✅ **การประมวลผลเสร็จสมบูรณ์:**\n")
		for _, msg := range successMessages {
			response.WriteString(fmt.Sprintf("- %s\n", msg))
		}
	}
	if len(errorMessages) > 0 {
		if response.Len() > 0 {
			response.WriteString("\n")
		} // Add newline if successes were also reported
		response.WriteString("⚠️ **พบข้อผิดพลาด:**\n")
		for _, msg := range errorMessages {
			response.WriteString(fmt.Sprintf("- %s\n", msg))
		}
	}
	if response.Len() == 0 { // Should not happen if input was provided
		response.WriteString("ไม่มี TxID ที่ถูกประมวลผล หรือ TxID ที่ให้มาไม่ถูกต้อง")
	}
	s.ChannelMessageSend(m.ChannelID, response.String())
}
