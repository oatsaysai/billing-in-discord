package discord

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	pp "github.com/Frontware/promptpay"
	"github.com/bwmarrin/discordgo"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"

	"github.com/oatsaysai/billing-in-discord/internal/db"
	"github.com/oatsaysai/billing-in-discord/pkg/ocr"
	"github.com/oatsaysai/billing-in-discord/pkg/verifier"
)

var (
	session          *discordgo.Session
	userMentionRegex = regexp.MustCompile(`<@!?(\d+)>`)
	txIDRegex        = regexp.MustCompile(`\(TxID:\s?(\d+)\)`)
	txIDsRegex       = regexp.MustCompile(`\(TxIDs:\s?([\d,]+)\)`)
	verifierClient   *verifier.Client
	ocrClient        *ocr.Client
)

// SetOCRClient sets the OCR client
func SetOCRClient(client *ocr.Client) {
	ocrClient = client
}

// SetVerifierClient sets the verifier client
func SetVerifierClient(client *verifier.Client) {
	verifierClient = client
}

// Initialize sets up the Discord session and registers handlers
func Initialize(token string) error {
	var err error
	session, err = discordgo.New("Bot " + token)
	if err != nil {
		return fmt.Errorf("error creating Discord session: %w", err)
	}

	// Register handlers
	session.AddHandler(messageHandler)

	// Open connection to Discord
	err = session.Open()
	if err != nil {
		return fmt.Errorf("error opening connection to Discord: %w", err)
	}

	log.Println("Connected to Discord successfully")
	return nil
}

// Close closes the Discord session
func Close() {
	if session != nil {
		session.Close()
	}
}

// messageHandler routes incoming messages to appropriate handlers
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
	case command == "!setpromptpay":
		go handleSetPromptPayCommand(s, m)
	case command == "!mypromptpay":
		go handleGetMyPromptPayCommand(s, m)
	case command == "!upweb":
		go handleUpWebCommand(s, m)
	case command == "!downweb":
		go handleDownWebCommand(s, m)
	case command == "!help":
		go handleHelpCommand(s, m, args)
	}
}

// --- Helper Functions ---

// sendErrorMessage sends an error message to the specified Discord channel
func sendErrorMessage(s *discordgo.Session, channelID, message string) {
	log.Printf("ERROR to user (Channel: %s): %s", channelID, message)
	_, err := s.ChannelMessageSend(channelID, fmt.Sprintf("⚠️ เกิดข้อผิดพลาด: %s", message))
	if err != nil {
		log.Printf("Failed to send error message to Discord: %v", err)
	}
}

// generateAndSendQrCode generates a QR code and sends it to the specified Discord channel
func generateAndSendQrCode(s *discordgo.Session, channelID, promptPayNum string, amount float64, targetUserDiscordID, description string, txIDs []int) {
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

// --- Parsing Functions ---

// parseQrArgs parses the arguments for the !qr command
func parseQrArgs(content string, userDbID int) (amount float64, toUser string, description string, promptPayID string, err error) {
	normalizedContent := strings.ToLower(content)
	trimmedContent := strings.TrimSpace(strings.TrimPrefix(normalizedContent, "!qr "))
	parts := strings.Fields(trimmedContent)

	// Check for minimum required parts (amount, to, @user)
	if len(parts) < 3 {
		return 0, "", "", "", fmt.Errorf("รูปแบบ `!qr` ไม่ถูกต้อง โปรดใช้: `!qr <จำนวนเงิน> to @user [for <รายละเอียด>] [<YourPromptPayID>]`")
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

	// Check if there are more parts for description/promptPay
	if len(parts) > 3 {
		// Initialize with defaults
		description = ""
		promptPayID = ""

		// Check if there's a "for" section (description)
		forIndex := -1
		for i, p := range parts[3:] {
			if p == "for" {
				forIndex = i + 3 // Adjust index to account for original parts array
				break
			}
		}

		if forIndex != -1 {
			// We have a description section

			// Check the last part to see if it's a potentially valid PromptPay ID
			lastPart := parts[len(parts)-1]
			if db.IsValidPromptPayID(lastPart) {
				// The last part is a PromptPay ID
				promptPayID = lastPart
				// Description is everything between "for" and the PromptPay ID
				description = strings.Join(parts[forIndex+1:len(parts)-1], " ")
			} else {
				// No PromptPay ID specified, description is everything after "for"
				description = strings.Join(parts[forIndex+1:], " ")
			}
		} else {
			// No "for" section, check if last part could be a PromptPay ID
			lastPart := parts[len(parts)-1]
			if db.IsValidPromptPayID(lastPart) {
				promptPayID = lastPart
			}
		}
	}

	// If promptPayID is still empty, try to get it from the database
	if promptPayID == "" {
		dbPromptPayID, err := db.GetUserPromptPayID(userDbID)
		if err != nil {
			return 0, "", "", "", fmt.Errorf("ไม่พบ PromptPayID ในข้อความและคุณยังไม่ได้ตั้งค่า PromptPayID ส่วนตัว กรุณาระบุ PromptPayID หรือใช้คำสั่ง !setpromptpay ก่อน")
		}
		promptPayID = dbPromptPayID
	}

	return amount, toUser, description, promptPayID, nil
}

// parseRequestPaymentArgs parses the arguments for the !request command
func parseRequestPaymentArgs(content string, creditorDbID int) (debtorDiscordID string, creditorPromptPayID string, err error) {
	parts := strings.Fields(content)

	// Check basic syntax
	if len(parts) < 2 {
		return "", "", fmt.Errorf("รูปแบบไม่ถูกต้อง โปรดใช้: `!request @ลูกหนี้ [PromptPayID]`")
	}

	// Extract debtor
	if !userMentionRegex.MatchString(parts[1]) {
		return "", "", fmt.Errorf("ต้องระบุ @ลูกหนี้ ที่ถูกต้อง")
	}
	debtorDiscordID = userMentionRegex.FindStringSubmatch(parts[1])[1]

	// Extract PromptPay ID if provided
	if len(parts) > 2 {
		creditorPromptPayID = parts[2]
		if !db.IsValidPromptPayID(creditorPromptPayID) {
			return "", "", fmt.Errorf("PromptPayID '%s' ของคุณไม่ถูกต้อง", creditorPromptPayID)
		}
	} else {
		// Try to get saved PromptPay ID
		savedPromptPayID, err := db.GetUserPromptPayID(creditorDbID)
		if err != nil {
			return "", "", fmt.Errorf("คุณยังไม่ได้ระบุ PromptPayID และยังไม่ได้ตั้งค่า PromptPayID ส่วนตัว กรุณาระบุ PromptPayID หรือใช้คำสั่ง !setpromptpay ก่อน")
		}
		creditorPromptPayID = savedPromptPayID
	}

	return debtorDiscordID, creditorPromptPayID, nil
}

// parseBillItem parses a bill item line from the !bill command
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

// parseAltBillItem parses an alternative format for a bill item
func parseAltBillItem(line string) (amount float64, description string, mentions []string, err error) {
	normalizedContent := strings.ToLower(line)
	parts := strings.Fields(normalizedContent)
	if len(parts) < 3 {
		return 0, "", nil, fmt.Errorf("รูปแบบรายการไม่ถูกต้อง โปรดใช้: `<จำนวนเงิน> <รายละเอียด> @user1 @user2...`")
	}
	amountNum, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, "", nil, fmt.Errorf("จำนวนเงินในรายการไม่ถูกต้อง: '%s'", parts[0])
	}
	amount = amountNum
	description = parts[1]
	mentionParts := parts[2:]
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

// --- Command Handlers ---

// handleBillCommand handles the !bill command
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
		// Check if the second part is a valid PromptPay ID
		if db.IsValidPromptPayID(firstLineParts[1]) {
			promptPayID = firstLineParts[1]
		} else {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("PromptPayID '%s' ในบรรทัดแรกดูเหมือนจะไม่ถูกต้อง", firstLineParts[1]))
			return
		}
	}

	payeeDiscordID := m.Author.ID
	payeeDbID, err := db.GetOrCreateUser(payeeDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", payeeDiscordID))
		return
	}

	// If promptPayID is not provided in the command, try to get it from the database
	if promptPayID == "" {
		dbPromptPayID, err := db.GetUserPromptPayID(payeeDbID)
		if err != nil {
			// If there's no promptPayID stored, just notify the user but continue processing
			log.Printf("No PromptPay ID found for user %s (dbID %d): %v", payeeDiscordID, payeeDbID, err)
			s.ChannelMessageSend(m.ChannelID, "⚠️ ไม่พบ PromptPay ID ที่บันทึกไว้ จะดำเนินการต่อโดยไม่สร้าง QR Code\nคุณสามารถตั้งค่า PromptPay ID ได้ด้วยคำสั่ง `!setpromptpay <PromptPayID>`")
		} else {
			promptPayID = dbPromptPayID
		}
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

		// Try parsing as a regular bill item
		amount, description, mentions, parseErr := parseAltBillItem(trimmedLine)
		if parseErr != nil {
			amount, description, mentions, parseErr = parseBillItem(trimmedLine)
			if parseErr != nil {
				sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d มีข้อผิดพลาด: %v", lineNum, parseErr))
				hasErrors = true
				continue
			}
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
			payerDbID, dbErr := db.GetOrCreateUser(payerDiscordID)
			if dbErr != nil {
				log.Printf("Error DB user %s for item '%s' line %d: %v", payerDiscordID, description, lineNum, dbErr)
				sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาด DB สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true
				continue // Skip this specific payer for this item
			}

			txID, txErr := db.CreateTransaction(payerDbID, payeeDbID, amountPerPerson, description)
			if txErr != nil {
				log.Printf("Failed to save transaction for user %s, item '%s' line %d: %v", payerDiscordID, description, lineNum, txErr)
				sendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาดในการบันทึก transaction สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true
				continue // Skip this specific payer for this item
			}

			userTotalDebts[payerDiscordID] += amountPerPerson
			userTxIDs[payerDiscordID] = append(userTxIDs[payerDiscordID], txID)

			// Update user_debts table
			debtErr := db.UpdateUserDebt(payerDbID, payeeDbID, amountPerPerson)
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

		// Only mention QR codes if we have a PromptPay ID
		if promptPayID != "" {
			qrSummary.WriteString("\nสร้าง QR Code สำหรับชำระเงิน:\n")
		}
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

// handleQrCommand handles the !qr command
func handleQrCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	payeeDiscordID := m.Author.ID // The one creating the QR is the payee
	payeeDbID, err := db.GetOrCreateUser(payeeDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", payeeDiscordID))
		return
	}

	amount, toUserDiscordID, description, promptPayID, err := parseQrArgs(m.Content, payeeDbID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, err.Error())
		return
	}

	payerDbID, err := db.GetOrCreateUser(toUserDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับผู้รับ <@%s>", toUserDiscordID))
		return
	}

	txID, err := db.CreateTransaction(payerDbID, payeeDbID, amount, description)
	if err != nil {
		log.Printf("Failed to save transaction for !qr from %s to %s: %v", payeeDiscordID, toUserDiscordID, err)
		sendErrorMessage(s, m.ChannelID, "เกิดข้อผิดพลาดในการบันทึก Transaction")
		return
	}

	err = db.UpdateUserDebt(payerDbID, payeeDbID, amount)
	if err != nil {
		log.Printf("Failed to update debt for !qr from %s to %s: %v", payeeDiscordID, toUserDiscordID, err)
		sendErrorMessage(s, m.ChannelID, "เกิดข้อผิดพลาดในการอัปเดตยอดหนี้")
		return
	}

	// Generate and send QR code
	generateAndSendQrCode(s, m.ChannelID, promptPayID, amount, toUserDiscordID, description, []int{txID})
}

// handleMyDebts handles the !mydebts command
func handleMyDebts(s *discordgo.Session, m *discordgo.MessageCreate) {
	queryAndSendDebts(s, m, m.Author.ID, "debtor")
}

// handleOwedToMe handles the !owedtome and !mydues commands
func handleOwedToMe(s *discordgo.Session, m *discordgo.MessageCreate) {
	queryAndSendDebts(s, m, m.Author.ID, "creditor")
}

// handleDebtsOfUser handles the !debts command
func handleDebtsOfUser(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 || !userMentionRegex.MatchString(args[0]) {
		sendErrorMessage(s, m.ChannelID, "รูปแบบไม่ถูกต้อง โปรดใช้ `!debts @user`")
		return
	}
	targetUserDiscordID := userMentionRegex.FindStringSubmatch(args[0])[1]
	queryAndSendDebts(s, m, targetUserDiscordID, "debtor")
}

// handleDuesForUser handles the !dues command
func handleDuesForUser(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	if len(args) == 0 || !userMentionRegex.MatchString(args[0]) {
		sendErrorMessage(s, m.ChannelID, "รูปแบบไม่ถูกต้อง โปรดใช้ `!dues @user`")
		return
	}
	targetUserDiscordID := userMentionRegex.FindStringSubmatch(args[0])[1]
	queryAndSendDebts(s, m, targetUserDiscordID, "creditor")
}

// queryAndSendDebts queries and sends debt information
func queryAndSendDebts(s *discordgo.Session, m *discordgo.MessageCreate, principalDiscordID string, mode string) {
	principalDbID, err := db.GetOrCreateUser(principalDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่พบ <@%s> ในฐานข้อมูล", principalDiscordID))
		return
	}

	// Get debts with transaction details from the db package
	isDebtor := mode == "debtor"
	debts, err := db.GetUserDebtsWithDetails(principalDbID, isDebtor)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลหนี้สินได้")
		log.Printf("Error querying debts with details (mode: %s) for %s (dbID %d): %v",
			mode, principalDiscordID, principalDbID, err)
		return
	}

	// Format the title based on the mode
	var title string
	if isDebtor {
		title = fmt.Sprintf("หนี้สินของ <@%s> (ที่ต้องจ่ายคนอื่น):\n", principalDiscordID)
	} else {
		title = fmt.Sprintf("ยอดค้างชำระถึง <@%s> (ที่คนอื่นต้องจ่าย):\n", principalDiscordID)
	}

	// Build the response
	var response strings.Builder
	response.WriteString(title)

	// Handle case with no debts
	if len(debts) == 0 {
		if isDebtor {
			response.WriteString(fmt.Sprintf("<@%s> ไม่มีหนี้สินค้างชำระ! 🎉\n", principalDiscordID))
		} else {
			response.WriteString(fmt.Sprintf("ดูเหมือนว่าทุกคนจะชำระหนี้ให้ <@%s> หมดแล้ว 👍\n", principalDiscordID))
		}
	} else {
		// Format each debt with its details
		for _, debt := range debts {
			// Truncate details if too long
			details := debt.Details
			maxDetailLen := 150 // Max length for details string in the summary
			if len(details) > maxDetailLen {
				details = details[:maxDetailLen-3] + "..."
			}

			// Format based on the mode
			if isDebtor {
				response.WriteString(fmt.Sprintf("- **%.2f บาท** ให้ <@%s> (รายละเอียดล่าสุด: %s)\n",
					debt.Amount, debt.OtherPartyDiscordID, details))
			} else {
				response.WriteString(fmt.Sprintf("- <@%s> เป็นหนี้ **%.2f บาท** (รายละเอียดล่าสุด: %s)\n",
					debt.OtherPartyDiscordID, debt.Amount, details))
			}
		}
	}

	// Send the response
	s.ChannelMessageSend(m.ChannelID, response.String())
}

// updatePaidStatus handles the !paid command
func updatePaidStatus(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	if len(parts) < 2 {
		sendErrorMessage(s, m.ChannelID, "รูปแบบคำสั่งไม่ถูกต้อง โปรดใช้ `!paid <TxID1>[,<TxID2>,...]`")
		return
	}
	txIDStrings := strings.Split(parts[1], ",") // Allow comma-separated TxIDs
	var successMessages, errorMessages []string

	authorDbID, err := db.GetOrCreateUser(m.Author.ID)
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

		// Get transaction info
		txInfo, err := db.GetTransactionInfo(txID)
		if err != nil {
			errorMessages = append(errorMessages, fmt.Sprintf("ไม่พบ TxID %d", txID))
			continue
		}

		// Only the designated payee can mark a transaction as paid
		payeeDbID := txInfo["payee_id"].(int)
		alreadyPaid := txInfo["already_paid"].(bool)

		if payeeDbID != authorDbID {
			errorMessages = append(errorMessages, fmt.Sprintf("คุณไม่ใช่ผู้รับเงินที่กำหนดไว้สำหรับ TxID %d", txID))
			continue
		}

		if alreadyPaid {
			// If already marked paid, it's a "success" in terms of state, but inform user.
			successMessages = append(successMessages, fmt.Sprintf("TxID %d ถูกทำเครื่องหมายว่าชำระแล้วอยู่แล้ว", txID))
			continue
		}

		err = db.MarkTransactionPaidAndUpdateDebt(txID)
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

// handleRequestPayment handles the !request command
func handleRequestPayment(s *discordgo.Session, m *discordgo.MessageCreate) {
	creditorDiscordID := m.Author.ID // The one making the request is the creditor
	creditorDbID, err := db.GetOrCreateUser(creditorDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", creditorDiscordID))
		return
	}

	debtorDiscordID, creditorPromptPayID, err := parseRequestPaymentArgs(m.Content, creditorDbID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, err.Error())
		return
	}

	//if debtorDiscordID == creditorDiscordID {
	//	sendErrorMessage(s, m.ChannelID, "คุณไม่สามารถร้องขอการชำระเงินจากตัวเองได้")
	//	return
	//}

	debtorDbID, err := db.GetOrCreateUser(debtorDiscordID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับลูกหนี้ <@%s>", debtorDiscordID))
		return
	}

	// Get total debt amount
	totalDebtAmount, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
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
	unpaidTxIDs, unpaidTxDetails, unpaidTotal, err := db.GetUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID, 10) // Limit details to 10 items
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

// handleSlipVerification handles slip verification
func handleSlipVerification(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.MessageReference == nil || m.MessageReference.MessageID == "" || len(m.Attachments) == 0 {
		return
	}

	if verifierClient == nil {
		sendErrorMessage(s, m.ChannelID, "Slip verification service is not configured. Slip verification is not available.")
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

	debtorDiscordID, amount, txIDs, err := db.ParseBotQRMessageContent(refMsg.Content)
	if err != nil {
		log.Printf("SlipVerify: Could not parse bot message content: %v", err)
		// Don't send error to user, might be a reply to a non-QR bot message
		return
	}

	log.Printf("SlipVerify: Received slip verification for debtor %s, amount %.2f, TxIDs %v", debtorDiscordID, amount, txIDs)
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
	if slipUploaderID != debtorDiscordID {
		log.Printf("SlipVerify: Slip uploaded by %s for debtor %s - ignoring (uploader mismatch).", slipUploaderID, debtorDiscordID)
		return
	}

	tmpFile := fmt.Sprintf("slip_%s_%s.png", m.ID, debtorDiscordID) // Unique temp file name
	err = ocr.DownloadFile(tmpFile, slipURL)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "ไม่สามารถดาวน์โหลดรูปภาพสลิปเพื่อยืนยันได้")
		log.Printf("SlipVerify: Failed to download slip %s: %v", slipURL, err)
		return
	}
	defer os.Remove(tmpFile)

	verifyResp, err := verifierClient.VerifySlip(amount, tmpFile)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("การเรียก API ยืนยันสลิปล้มเหลว: %v", err))
		log.Printf("SlipVerify: API call failed for debtor %s, amount %.2f: %v", debtorDiscordID, amount, err)
		return
	}

	// Check if amount from slip matches expected amount (with tolerance)
	if !(verifyResp.Data.Amount > amount-0.01 && verifyResp.Data.Amount < amount+0.01) {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("จำนวนเงินในสลิป (%.2f) ไม่ตรงกับจำนวนที่คาดไว้ (%.2f)", verifyResp.Data.Amount, amount))
		return
	}

	intendedPayeeDiscordID := "???" // Placeholder
	// Try to determine intended payee
	if len(txIDs) > 0 {
		// If TxIDs are present, payee is determined from the first TxID
		payeeDbID, fetchErr := db.GetPayeeDbIDFromTx(txIDs[0])
		if fetchErr == nil {
			payeeDiscordID, _ := db.GetDiscordIDFromDbID(payeeDbID)
			intendedPayeeDiscordID = payeeDiscordID
		}
	} else {
		// If no TxIDs, try to find payee based on debtor and amount
		payee, findErr := db.FindIntendedPayee(debtorDiscordID, amount)
		if findErr != nil {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่สามารถระบุผู้รับเงินที่ถูกต้องสำหรับการชำระเงินนี้ได้: %v", findErr))
			log.Printf("SlipVerify: Could not determine intended payee for debtor %s, amount %.2f: %v", debtorDiscordID, amount, findErr)
			return
		}
		intendedPayeeDiscordID = payee
	}

	if intendedPayeeDiscordID == "???" || intendedPayeeDiscordID == "" {
		log.Printf("SlipVerify: Critical - Failed to determine intended payee for debtor %s, amount %.2f", debtorDiscordID, amount)
		sendErrorMessage(s, m.ChannelID, "เกิดข้อผิดพลาดร้ายแรง: ไม่สามารถระบุผู้รับเงินได้")
		return
	}

	// Process payment based on TxIDs if available
	if len(txIDs) > 0 {
		log.Printf("SlipVerify: Attempting batch update using TxIDs: %v", txIDs)
		successCount := 0
		failCount := 0
		var failMessages []string

		for _, txID := range txIDs {
			err = db.MarkTransactionPaidAndUpdateDebt(txID) // This function handles both transaction and user_debt updates
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
			debtorDiscordID, intendedPayeeDiscordID, amount,
			verifyResp.Data.SenderName, verifyResp.Data.SenderID,
			verifyResp.Data.ReceiverName, verifyResp.Data.ReceiverID,
			verifyResp.Data.Date, verifyResp.Data.Ref,
		))
		report.WriteString(fmt.Sprintf("อัปเดตสำเร็จ %d/%d รายการธุรกรรม (TxIDs: %v)\n", successCount, len(txIDs), txIDs))
		if failCount > 0 {
			report.WriteString(fmt.Sprintf("⚠️ เกิดข้อผิดพลาด %d รายการ: %s", failCount, strings.Join(failMessages, "; ")))
		}
		s.ChannelMessageSend(m.ChannelID, report.String())
		return

	} else { // No TxIDs, general debt reduction
		log.Printf("SlipVerify: No TxIDs found in message. Attempting general debt reduction for %s paying %s amount %.2f.", debtorDiscordID, intendedPayeeDiscordID, amount)

		errReduce := db.ReduceDebtFromPayment(debtorDiscordID, intendedPayeeDiscordID, amount)
		if errReduce != nil {
			sendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดในการลดหนี้สินทั่วไปสำหรับ <@%s> ถึง <@%s>: %v", debtorDiscordID, intendedPayeeDiscordID, errReduce))
			log.Printf("SlipVerify: Failed general debt reduction for %s to %s (%.2f): %v", debtorDiscordID, intendedPayeeDiscordID, amount, errReduce)
			return
		}
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf(
			"✅ สลิปได้รับการยืนยัน & ยอดหนี้สินจาก <@%s> ถึง <@%s> ลดลง %.2f บาท!\n- ผู้ส่ง (สลิป): %s (%s)\n- ผู้รับ (สลิป): %s (%s)\n- วันที่ (สลิป): %s\n- เลขอ้างอิง (สลิป): %s",
			debtorDiscordID, intendedPayeeDiscordID, amount,
			verifyResp.Data.SenderName, verifyResp.Data.SenderID,
			verifyResp.Data.ReceiverName, verifyResp.Data.ReceiverID,
			verifyResp.Data.Date, verifyResp.Data.Ref,
		))
	}
}

// handleUpWebCommand handles the !upweb command
func handleUpWebCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	// This is a placeholder for the Firebase functionality
	// Will need to be implemented with the Firebase client
	s.ChannelMessageSend(m.ChannelID, "The Firebase web hosting functionality will be implemented in a future update.")
}

// handleDownWebCommand handles the !downweb command
func handleDownWebCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	// This is a placeholder for the Firebase functionality
	// Will need to be implemented with the Firebase client
	s.ChannelMessageSend(m.ChannelID, "The Firebase web hosting functionality will be implemented in a future update.")
}

// handleHelpCommand handles the !help command
func handleHelpCommand(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	helpMessage := `
**คำสั่งพื้นฐาน:**
- ` + "`!bill [promptpay_id]`" + ` - สร้างบิลแบ่งจ่าย (ต้องตามด้วยรายการในบรรทัดถัดไป)
- ` + "`!qr <amount> to @user [for <description>] [promptpay_id]`" + ` - สร้าง QR รับชำระจากผู้ใช้
- ` + "`!mydebts`" + ` - ดูยอดหนี้ที่คุณต้องจ่ายผู้อื่น
- ` + "`!mydues`" + ` (หรือ ` + "`!owedtome`" + `) - ดูยอดเงินที่ผู้อื่นเป็นหนี้คุณ
- ` + "`!debts @user`" + ` - ดูยอดหนี้ที่ผู้ใช้รายนั้นเป็นหนี้ผู้อื่น
- ` + "`!dues @user`" + ` - ดูยอดเงินที่ผู้อื่นเป็นหนี้ผู้ใช้รายนั้น
- ` + "`!request @user [promptpay_id]`" + ` - ส่งคำขอชำระเงินไปยังผู้ใช้
- ` + "`!paid <txID>`" + ` - ทำเครื่องหมายว่ารายการชำระแล้ว (ต้องเป็นผู้รับเงินเท่านั้น)

**คำสั่งจัดการ PromptPay ID:**
- ` + "`!setpromptpay <promptpay_id>`" + ` - ตั้งค่า PromptPay ID ของคุณ
- ` + "`!mypromptpay`" + ` - แสดง PromptPay ID ที่คุณบันทึกไว้

**รูปแบบการสร้างบิล:**
- บรรทัดแรก: ` + "`!bill [promptpay_id]`" + ` (ถ้าไม่ระบุจะใช้ PromptPay ID ที่บันทึกไว้)
- บรรทัดถัดไป (รายการ): ` + "`<amount> for <description> with @user1 @user2...`" + `
- หรือ (รูปแบบสั้น): ` + "`<amount> <description> @user1 @user2...`" + `

**ตัวอย่าง:**
` + "```" + `
!bill 081-234-5678
100 for dinner with @UserA @UserB
50 drinks @UserB
` + "```" + `

**การตรวจสอบการชำระเงิน:**
คุณสามารถส่งสลิปโดยตอบกลับข้อความ QR code ที่บอทส่งให้ เพื่อตรวจสอบและปรับปรุงยอดหนี้โดยอัตโนมัติ
`
	s.ChannelMessageSend(m.ChannelID, helpMessage)
}
