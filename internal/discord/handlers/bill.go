package handlers

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// HandleBillCommand handles the !bill command
func HandleBillCommand(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Check if there's an attachment (bill image)
	if len(m.Attachments) > 0 {
		// Process the first attachment as a bill image
		HandleOCRBillAttachment(s, m, m.Attachments[0])
		return
	}

	// No attachments, process as a regular text bill
	lines := strings.Split(strings.TrimSpace(m.Content), "\n")
	if len(lines) < 2 {
		SendErrorMessage(s, m.ChannelID, "รูปแบบ `!bill` ไม่ถูกต้อง ต้องมีอย่างน้อย 2 บรรทัด (บรรทัดแรกคือคำสั่ง บรรทัดถัดไปคือรายการ) หรือแนบรูปภาพบิล")
		return
	}

	firstLineParts := strings.Fields(lines[0])
	if strings.ToLower(firstLineParts[0]) != "!bill" {
		SendErrorMessage(s, m.ChannelID, "บรรทัดแรกต้องขึ้นต้นด้วย `!bill`")
		return
	}

	var promptPayID string
	if len(firstLineParts) > 1 {
		// Check if the second part is a valid PromptPay ID
		if db.IsValidPromptPayID(firstLineParts[1]) {
			promptPayID = firstLineParts[1]
		} else {
			SendErrorMessage(s, m.ChannelID, fmt.Sprintf("PromptPayID '%s' ในบรรทัดแรกดูเหมือนจะไม่ถูกต้อง", firstLineParts[1]))
			return
		}
	}

	payeeDiscordID := m.Author.ID
	payeeDbID, err := db.GetOrCreateUser(payeeDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", payeeDiscordID))
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
				SendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d มีข้อผิดพลาด: %v", lineNum, parseErr))
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
			SendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: จำนวนเงินต่อคนน้อยเกินไป (%.4f)", lineNum, amountPerPerson))
			hasErrors = true
			continue
		}

		for _, payerDiscordID := range mentions {
			payerDbID, dbErr := db.GetOrCreateUser(payerDiscordID)
			if dbErr != nil {
				log.Printf("Error DB user %s for item '%s' line %d: %v", payerDiscordID, description, lineNum, dbErr)
				SendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาด DB สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true
				continue // Skip this specific payer for this item
			}

			txID, txErr := db.CreateTransaction(payerDbID, payeeDbID, amountPerPerson, description)
			if txErr != nil {
				log.Printf("Failed to save transaction for user %s, item '%s' line %d: %v", payerDiscordID, description, lineNum, txErr)
				SendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาดในการบันทึก transaction สำหรับ <@%s>", lineNum, payerDiscordID))
				hasErrors = true
				continue // Skip this specific payer for this item
			}

			userTotalDebts[payerDiscordID] += amountPerPerson
			userTxIDs[payerDiscordID] = append(userTxIDs[payerDiscordID], txID)

			// Update user_debts table
			debtErr := db.UpdateUserDebt(payerDbID, payeeDbID, amountPerPerson)
			if debtErr != nil {
				log.Printf("Failed to update debt for user %s, item '%s' line %d: %v", payerDiscordID, description, lineNum, debtErr)
				SendErrorMessage(s, m.ChannelID, fmt.Sprintf("บรรทัดที่ %d: เกิดข้อผิดพลาดในการอัปเดตยอดหนี้สำหรับ <@%s>", lineNum, payerDiscordID))
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
				GenerateAndSendQrCode(s, m.ChannelID, promptPayID, totalOwed, payerDiscordID, fmt.Sprintf("ยอดรวมจากบิลนี้โดย <@%s>", m.Author.ID), relevantTxIDs)
			}
		}
	} else if !hasErrors {
		s.ChannelMessageSend(m.ChannelID, "ไม่พบรายการที่ถูกต้องในบิล")
	}
}

// HandleQrCommand handles the !qr command
func HandleQrCommand(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	payeeDiscordID := m.Author.ID // The one creating the QR is the payee
	payeeDbID, err := db.GetOrCreateUser(payeeDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", payeeDiscordID))
		return
	}

	amount, toUserDiscordID, description, promptPayID, err := parseQrArgs(m.Content, payeeDbID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, err.Error())
		return
	}

	payerDbID, err := db.GetOrCreateUser(toUserDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับผู้รับ <@%s>", toUserDiscordID))
		return
	}

	txID, err := db.CreateTransaction(payerDbID, payeeDbID, amount, description)
	if err != nil {
		log.Printf("Failed to save transaction for !qr from %s to %s: %v", payeeDiscordID, toUserDiscordID, err)
		SendErrorMessage(s, m.ChannelID, "เกิดข้อผิดพลาดในการบันทึก Transaction")
		return
	}

	err = db.UpdateUserDebt(payerDbID, payeeDbID, amount)
	if err != nil {
		log.Printf("Failed to update debt for !qr from %s to %s: %v", payeeDiscordID, toUserDiscordID, err)
		SendErrorMessage(s, m.ChannelID, "เกิดข้อผิดพลาดในการอัปเดตยอดหนี้")
		return
	}

	// Generate and send QR code
	GenerateAndSendQrCode(s, m.ChannelID, promptPayID, amount, toUserDiscordID, description, []int{txID})
}

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
