package handlers

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// HandlePaidCommand handles the !paid command
func HandlePaidCommand(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	if len(args) < 2 {
		SendErrorMessage(s, m.ChannelID, "รูปแบบคำสั่งไม่ถูกต้อง โปรดใช้ `!paid <TxID1>[,<TxID2>,...]`")
		return
	}
	txIDStrings := strings.Split(args[1], ",") // Allow comma-separated TxIDs
	var successMessages, errorMessages []string

	authorDbID, err := db.GetOrCreateUser(m.Author.ID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถยืนยันบัญชีผู้ใช้ของคุณสำหรับการดำเนินการนี้")
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

// HandleRequestPayment handles the !request command
func HandleRequestPayment(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	creditorDiscordID := m.Author.ID // The one making the request is the creditor
	creditorDbID, err := db.GetOrCreateUser(creditorDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับคุณ (<@%s>)", creditorDiscordID))
		return
	}

	debtorDiscordID, creditorPromptPayID, err := parseRequestPaymentArgs(m.Content, creditorDbID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, err.Error())
		return
	}

	//if debtorDiscordID == creditorDiscordID {
	//	sendErrorMessage(s, m.ChannelID, "คุณไม่สามารถร้องขอการชำระเงินจากตัวเองได้")
	//	return
	//}

	debtorDbID, err := db.GetOrCreateUser(debtorDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดกับฐานข้อมูลสำหรับลูกหนี้ <@%s>", debtorDiscordID))
		return
	}

	// Get total debt amount
	totalDebtAmount, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("เกิดข้อผิดพลาดในการค้นหายอดหนี้รวมที่ <@%s> ค้างชำระกับคุณ", debtorDiscordID))
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
		GenerateAndSendQrCode(s, m.ChannelID, creditorPromptPayID, totalDebtAmount, debtorDiscordID, description, nil) // No specific TxIDs
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

	GenerateAndSendQrCode(s, m.ChannelID, creditorPromptPayID, totalDebtAmount, debtorDiscordID, description, unpaidTxIDs)
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
