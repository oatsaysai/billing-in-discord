package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
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

var (
	dg *discordgo.Session
)

type BillItem struct {
	Description string
	Amount      float64
	SharedWith  []string // Slice of Discord User IDs
}

// Removed MultiItemBill struct as stateful multi-line input is removed

var (
	// Removed activeBills map & mutex
	userMentionRegex = regexp.MustCompile(`<@!?(\d+)>`)
	txIDRegex        = regexp.MustCompile(`\(TxID:\s?(\d+)\)`)
	txIDsRegex       = regexp.MustCompile(`\(TxIDs:\s?([\d,]+)\)`)
)

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
		os.Remove(filename)
		return
	}
	file, err := os.Open(filename)
	if err != nil {
		sendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถส่งรูปภาพ QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Could not open QR image %s for sending: %v", filename, err)
		os.Remove(filename)
		return
	}
	defer file.Close()
	defer os.Remove(filename)

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

// handleBillCommand processes multi-line bill entries.
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
			promptPayID = ""
		}
	}

	payeeDiscordID := m.Author.ID
	payeeDbID, err := getOrCreateDBUser(payeeDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", payeeDiscordID))
		return
	}

	userTotalDebts := make(map[string]float64)
	userTxIDs := make(map[string][]int)
	var billItemsSummary strings.Builder
	billItemsSummary.WriteString(fmt.Sprintf("สรุปบิลโดย <@%s>:\n", m.Author.ID))
	totalBillAmount := 0.0
	hasErrors := false

	for i, line := range lines[1:] {
		lineNum := i + 2
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
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
		if amountPerPerson < 0.01 && amount > 0 {
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
				continue
			}

			var txID int
			txErr := dbPool.QueryRow(context.Background(),
				`INSERT INTO transactions (payer_id, payee_id, amount, description) VALUES ($1, $2, $3, $4) RETURNING id`,
				payerDbID, payeeDbID, amountPerPerson, description).Scan(&txID)
			if txErr != nil {
				log.Printf("Failed to save transaction for user %s, item '%s' line %d: %v", payerDiscordID, description, lineNum, txErr)
				sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาดในการบันทึก transaction สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true
				continue
			}

			userTotalDebts[payerDiscordID] += amountPerPerson
			userTxIDs[payerDiscordID] = append(userTxIDs[payerDiscordID], txID)

			debtErr := updateUserDebt(payerDbID, payeeDbID, amountPerPerson)
			if debtErr != nil {
				log.Printf("Failed to update debt for user %s, item '%s' line %d: %v", payerDiscordID, description, lineNum, debtErr)
				sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาดในการอัปเดตยอดหนี้สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true
			}
		}
	}

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
			if promptPayID != "" && totalOwed > 0.009 {
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
	payeeDiscordID := m.Author.ID
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

	creditorDiscordID := m.Author.ID

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

	if totalDebtAmount <= 0.009 {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("ยอดเยี่ยม! <@%s> ไม่ได้ติดหนี้คุณอยู่", debtorDiscordID))
		return
	}

	unpaidTxIDs, unpaidTxDetails, unpaidTotal, err := getUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID, 10)
	if err != nil {
		log.Printf("Error fetching transaction details for !request: %v", err)
	}

	if !(unpaidTotal > totalDebtAmount-0.01 && unpaidTotal < totalDebtAmount+0.01) {
		log.Printf("Data Inconsistency Alert: Unpaid transactions sum (%.2f) does not match user_debts amount (%.2f) for debtor %d -> creditor %d. Sending QR for total debt without TxIDs.", unpaidTotal, totalDebtAmount, debtorDbID, creditorDbID)
		description := fmt.Sprintf("คำร้องขอชำระหนี้คงค้างจาก <@%s> (ยอดรวม)", creditorDiscordID)
		generateAndSendQrCode(s, m.ChannelID, creditorPromptPayID, totalDebtAmount, debtorDiscordID, description, nil)
		return
	}

	description := fmt.Sprintf("คำร้องขอชำระหนี้คงค้างจาก <@%s>", creditorDiscordID)
	if unpaidTxDetails != "" {
		maxDescLen := 1500
		detailsHeader := "\nประกอบด้วยรายการ (TxIDs):\n"
		availableSpace := maxDescLen - len(description) - len(detailsHeader) - 50
		if len(unpaidTxDetails) > availableSpace && availableSpace > 0 {
			unpaidTxDetails = unpaidTxDetails[:availableSpace] + "...\n(และรายการอื่นๆ)"
		} else if availableSpace <= 0 {
			unpaidTxDetails = "(แสดงรายการไม่ได้เนื่องจากข้อความยาวเกินไป)"
		}
		description += detailsHeader + unpaidTxDetails
	}

	generateAndSendQrCode(s, m.ChannelID, creditorPromptPayID, totalDebtAmount, debtorDiscordID, description, unpaidTxIDs)
}

// Helper to get unpaid transaction details string AND slice of IDs AND total amount
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
		if detailLimit <= 0 || count < detailLimit {
			details.WriteString(fmt.Sprintf("- `%.2f` บาท: %s (TxID: %d)\n", amount, descText, id))
		} else if count == detailLimit {
			details.WriteString("- ... (และรายการอื่นๆ)\n")
		}
		txIDs = append(txIDs, id)
		totalAmount += amount
		count++
	}
	if count == 0 {
		return nil, "", 0, nil
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
	WHERE rtd.rn <= 5
	GROUP BY rtd.payer_id, rtd.payee_id
	`
	if mode == "debtor" {
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
	} else {
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
		maxDetailLen := 150
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
		default:
			helpMsg = "ขออภัย ไม่พบความช่วยเหลือสำหรับหัวข้อ `" + topic + "` ลองพิมพ์ `!help` เพื่อดูรายการคำสั่งหลัก"
		}
		s.ChannelMessageSend(m.ChannelID, helpMsg)
	} else {
		s.ChannelMessageSend(m.ChannelID, baseHelp)
	}
}

func handleSlipVerification(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.MessageReference == nil || m.MessageReference.MessageID == "" || len(m.Attachments) == 0 {
		return
	}
	refMsg, err := s.ChannelMessage(m.ChannelID, m.MessageReference.MessageID)
	if err != nil {
		log.Printf("SlipVerify: Error fetching referenced message %s: %v", m.MessageReference.MessageID, err)
		return
	}
	if refMsg.Author == nil || refMsg.Author.ID != s.State.User.ID {
		return
	}
	parsedDebtorDiscordID, parsedAmount, parsedTxIDs, err := parseBotQRMessageContent(refMsg.Content)
	if err != nil {
		log.Printf("SlipVerify: Could not parse bot message content: %v", err)
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
		return
	}

	if slipUploaderID != parsedDebtorDiscordID {
		log.Printf("SlipVerify: Slip uploaded by %s for debtor %s - ignoring.", slipUploaderID, parsedDebtorDiscordID)
		return
	}

	tmpFile := fmt.Sprintf("slip_%s_%s.png", m.ID, parsedDebtorDiscordID)
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

	if !(verifyResp.Data.Amount > parsedAmount-0.01 && verifyResp.Data.Amount < parsedAmount+0.01) {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("จำนวนเงินในสลิป (%.2f) ไม่ตรงกับจำนวนที่คาดไว้ (%.2f)", verifyResp.Data.Amount, parsedAmount))
		return
	}

	intendedPayeeDiscordID := "???"
	if len(parsedTxIDs) > 0 {
		payeeDbID, fetchErr := getPayeeDbIdFromTx(parsedTxIDs[0])
		if fetchErr == nil {
			intendedPayeeDiscordID, _ = getDiscordIdFromDbId(payeeDbID)
		}
	} else {
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

	if len(parsedTxIDs) > 0 {
		log.Printf("SlipVerify: Attempting batch update using TxIDs: %v", parsedTxIDs)
		successCount := 0
		failCount := 0
		var failMessages []string

		for _, txID := range parsedTxIDs {
			err = markTransactionPaidAndUpdateDebt(txID)
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

	} else {
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
	var count int
	query := `
		SELECT u.discord_id, COUNT(*) OVER()
		FROM user_debts ud
		JOIN users u ON ud.creditor_id = u.id
		WHERE ud.debtor_id = $1
		  AND ABS(ud.amount - $2::numeric) < 0.01
		  AND ud.amount > 0.009
		LIMIT 1;
	`
	err = dbPool.QueryRow(context.Background(), query, debtorDbID, amount).Scan(&payeeDiscordID, &count)
	if err == nil && count == 1 {
		log.Printf("findIntendedPayee: Found single matching creditor %s based on total debt amount %.2f for debtor %s", payeeDiscordID, amount, debtorDiscordID)
		return payeeDiscordID, nil
	}
	if err == nil && count > 1 {
		log.Printf("findIntendedPayee: Ambiguous - Debtor %s owes %.2f to multiple creditors.", debtorDiscordID, amount)
	}

	query = `
		SELECT u.discord_id, COUNT(*) OVER() as payee_count
		FROM transactions t
		JOIN users u ON t.payee_id = u.id
		WHERE t.payer_id = $1
		  AND ABS(t.amount - $2::numeric) < 0.01
		  AND t.already_paid = false
		GROUP BY u.discord_id
		LIMIT 2;
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
		if err := rows.Scan(&payee, &count); err != nil {
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
		log.Printf("findIntendedPayee: Ambiguous - Found multiple potential payees based on transaction amount %.2f for debtor %s", amount, debtorDiscordID)
		return "", fmt.Errorf("พบผู้รับเงินที่เป็นไปได้หลายคนสำหรับจำนวนเงินนี้ โปรดใช้คำสั่ง `!paid <TxID>` โดยผู้รับเงิน")
	}

	log.Printf("findIntendedPayee: Could not determine unique intended payee for debtor %s, amount %.2f", debtorDiscordID, amount)
	return "", fmt.Errorf("ไม่สามารถระบุผู้รับเงินที่แน่นอนสำหรับยอดนี้ได้ โปรดให้ผู้รับเงินยืนยันด้วย `!paid <TxID>`")
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
	defer tx.Rollback(context.Background())

	result, err := tx.Exec(context.Background(),
		`UPDATE user_debts SET amount = amount - $1, updated_at = CURRENT_TIMESTAMP
         WHERE debtor_id = $2 AND creditor_id = $3 AND amount > 0.009`,
		amount, debtorDbID, payeeDbID)

	if err != nil {
		return fmt.Errorf("เกิดข้อผิดพลาดขณะอัปเดตหนี้สินรวม: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		log.Printf("Debt reduction update affected 0 rows for debtor %d paying creditor %d amount %.2f. Debt might be zero or negative already.", debtorDbID, payeeDbID, amount)
		zeroResult, errZero := tx.Exec(context.Background(),
			`UPDATE user_debts SET amount = 0, updated_at = CURRENT_TIMESTAMP
		   WHERE debtor_id = $1 AND creditor_id = $2 AND amount > 0.009 AND amount < $3`,
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

	txsMatch := txIDsRegex.FindStringSubmatch(content)
	if len(txsMatch) == 2 {
		idStrings := strings.Split(txsMatch[1], ",")
		txIDs = make([]int, 0, len(idStrings))
		for _, idStr := range idStrings {
			trimmedIDStr := strings.TrimSpace(idStr)
			if parsedTxID, txErr := strconv.Atoi(trimmedIDStr); txErr == nil {
				txIDs = append(txIDs, parsedTxID)
			} else {
				log.Printf("Warning: Failed to parse TxID '%s' from multi-ID list: %v", trimmedIDStr, txErr)
			}
		}
		if len(txIDs) > 0 {
			return debtorDiscordID, amount, txIDs, nil
		}
	}

	txMatch := txIDRegex.FindStringSubmatch(content)
	if len(txMatch) == 2 {
		if parsedTxID, txErr := strconv.Atoi(txMatch[1]); txErr == nil {
			txIDs = []int{parsedTxID}
			return debtorDiscordID, amount, txIDs, nil
		} else {
			log.Printf("Warning: Failed to parse single TxID '%s': %v", txMatch[1], txErr)
		}
	}

	return debtorDiscordID, amount, nil, nil
}

func DiscordConnect() (err error) {
	dg, err = discordgo.New("Bot " + viper.GetString("DiscordBot.Token"))
	if err != nil {
		log.Println("FATAL: error creating Discord session,", err)
		return
	}
	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsGuildMessageReactions | discordgo.IntentsMessageContent

	log.Println("INFO: บอทกำลังเปิด...")
	dg.AddHandler(messageHandler)
	err = dg.Open()
	if err != nil {
		log.Println("FATAL: Error Open():", err)
		return
	}
	_, err = dg.User("@me")
	if err != nil {
		log.Println("FATAL: Login unsuccessful:", err)
		return
	}
	log.Println("INFO: บอทกำลังทำงาน กด CTRL-C เพื่อออก")
	return nil
}

func init() {
	viper.SetConfigName("config")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Println("ไม่พบไฟล์ config, จะใช้ค่าเริ่มต้นและตัวแปรสภาพแวดล้อม")
		} else {
			log.Fatalf("CRITICAL: ไม่สามารถอ่าน config: %v\n", err)
		}
	} else {
		log.Println("กำลังใช้ไฟล์ config:", viper.ConfigFileUsed())
	}

	viper.SetDefault("DiscordBot.Token", "")
	viper.SetDefault("PostgreSQL.Host", "localhost")
	viper.SetDefault("PostgreSQL.Port", "5432")
	viper.SetDefault("PostgreSQL.User", "postgres")
	viper.SetDefault("PostgreSQL.Password", "")
	viper.SetDefault("PostgreSQL.DBName", "discordbotdb")
	viper.SetDefault("PostgreSQL.Schema", "public")
	viper.SetDefault("PostgreSQL.PoolMaxConns", 10)
}

func main() {
	initPostgresPool()
	if dbPool != nil {
		defer dbPool.Close()
	} else {
		log.Fatal("CRITICAL: dbPool is nil after initPostgresPool. Exiting.")
	}

	migrateDatabase()

	err := DiscordConnect()
	if err != nil {
		log.Fatalf("CRITICAL: Failed to connect to Discord: %v", err)
	}
	<-make(chan struct{})
}

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
	resp, err := http.Get(url)
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
		"img": fmt.Sprintf("data:image/png;base64,%s", imgBase64),
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to marshal JSON: %v", err)
	}
	url := fmt.Sprintf("https://slip-c.oiioioiiioooioio.download/api/slip/%.2f", amount)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 20 * time.Second,
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
		return nil, fmt.Errorf("VerifySlip: failed to unmarshal response: %v, body: %s", err, string(body))
	}
	log.Printf("VerifySlip successful for amount %.2f. API Response Ref: %s", amount, result.Data.Ref)
	return &result, nil
}

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
	query := `
SELECT id FROM transactions
WHERE payer_id = $1 AND payee_id = $2
AND ABS(amount - $3::numeric) < 0.01
AND already_paid = false
ORDER BY created_at DESC LIMIT 1;`
	err = dbPool.QueryRow(context.Background(), query, payerDbID, payeeDbID, amount).Scan(&txID)
	if err != nil {
		return 0, fmt.Errorf("no matching unpaid transaction")
	}
	log.Printf("Found matching transaction ID %d for Payer %s, Payee %s, Amount %.2f", txID, payerDiscordID, payeeDiscordID, amount)
	return txID, nil
}

func markTransactionPaidAndUpdateDebt(txID int) error {
	var payerDbID, payeeDbID int
	var amount float64

	tx, err := dbPool.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("failed to begin database transaction: %w", err)
	}
	defer tx.Rollback(context.Background())

	err = tx.QueryRow(context.Background(),
		`SELECT payer_id, payee_id, amount FROM transactions WHERE id = $1 AND already_paid = false FOR UPDATE`, txID,
	).Scan(&payerDbID, &payeeDbID, &amount)
	if err != nil {
		if err.Error() == "no rows in result set" {
			log.Printf("TxID %d already paid or does not exist.", txID)
			return fmt.Errorf("TxID %d ไม่พบ หรือถูกชำระไปแล้ว", txID)
		}
		return fmt.Errorf("failed to retrieve unpaid transaction %d: %w", txID, err)
	}

	_, err = tx.Exec(context.Background(), `UPDATE transactions SET already_paid = TRUE WHERE id = $1`, txID)
	if err != nil {
		return fmt.Errorf("failed to mark transaction %d as paid: %w", txID, err)
	}

	_, err = tx.Exec(context.Background(),
		`UPDATE user_debts SET amount = amount - $1, updated_at = CURRENT_TIMESTAMP
WHERE debtor_id = $2 AND creditor_id = $3`,
		amount, payerDbID, payeeDbID)
	if err != nil {
		log.Printf("Warning/Error updating user_debts for txID %d (debtor %d, creditor %d, amount %.2f): %v. This might be okay if debt was already cleared.", txID, payerDbID, payeeDbID, amount, err)
	}

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
	txIDStrings := strings.Split(parts[1], ",")
	var successMessages, errorMessages []string

	authorDbID, err := getOrCreateDBUser(m.Author.ID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "ไม่สามารถยืนยันบัญชีผู้ใช้ของคุณสำหรับการดำเนินการนี้")
		return
	}

	for _, txIDStr := range txIDStrings {
		trimmedTxIDStr := strings.TrimSpace(txIDStr)
		txID, err := strconv.Atoi(trimmedTxIDStr)
		if err != nil {
			errorMessages = append(errorMessages, fmt.Sprintf("รูปแบบ TxID '%s' ไม่ถูกต้อง", trimmedTxIDStr))
			continue
		}

		var payeeDbID int
		var alreadyPaid bool
		err = dbPool.QueryRow(context.Background(),
			`SELECT t.payee_id, t.already_paid FROM transactions t WHERE t.id = $1`, txID).Scan(&payeeDbID, &alreadyPaid)
		if err != nil {
			errorMessages = append(errorMessages, fmt.Sprintf("ไม่พบ TxID %d", txID))
			continue
		}

		if payeeDbID != authorDbID {
			errorMessages = append(errorMessages, fmt.Sprintf("คุณไม่ใช่ผู้รับเงินที่กำหนดไว้สำหรับ TxID %d", txID))
			continue
		}

		if alreadyPaid {
			successMessages = append(successMessages, fmt.Sprintf("TxID %d ถูกทำเครื่องหมายว่าชำระแล้ว", txID))
			continue
		}

		err = markTransactionPaidAndUpdateDebt(txID)
		if err != nil {
			errorMessages = append(errorMessages, fmt.Sprintf("ไม่สามารถอัปเดต TxID %d: %v", txID, err))
		} else {
			successMessages = append(successMessages, fmt.Sprintf("TxID %d ถูกทำเครื่องหมายว่าชำระแล้ว", txID))
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
		response.WriteString("⚠️ **พบข้อผิดพลาด:**\n")
		for _, msg := range errorMessages {
			response.WriteString(fmt.Sprintf("- %s\n", msg))
		}
	}
	if response.Len() == 0 {
		response.WriteString("ไม่มี TxID ที่ถูกประมวลผล")
	}
	s.ChannelMessageSend(m.ChannelID, response.String())
}
