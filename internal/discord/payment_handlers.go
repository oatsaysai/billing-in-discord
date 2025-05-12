package discord

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// handleViewDuesButton handles the "View Dues" button
func handleViewDuesButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "_")
	if len(parts) < 3 {
		respondWithError(s, i, "รูปแบบ custom ID ไม่ถูกต้อง")
		return
	}

	debtorID := parts[2]           // The person who owes money
	creditorID := i.Member.User.ID // The current user viewing the dues

	debtorDbID, err := db.GetOrCreateUser(debtorID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่สามารถระบุลูกหนี้ <@%s> ในระบบ", debtorID))
		return
	}

	creditorDbID, err := db.GetOrCreateUser(creditorID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถระบุตัวตนของคุณในระบบ")
		return
	}

	// Get recent unpaid transactions
	txs, err := db.GetRecentTransactions(debtorDbID, creditorDbID, 10, false)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่สามารถดึงข้อมูลรายการล่าสุดได้: %v", err))
		return
	}

	// Get total debt amount
	totalDebt, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่สามารถดึงยอดหนี้รวมได้: %v", err))
		return
	}

	// Get debtor's username
	debtorName := GetDiscordUsername(s, debtorID)

	// Create details message
	detailsMessage := fmt.Sprintf("**รายละเอียดหนี้ที่ %s (<@%s>) ค้างชำระคุณ**\n"+
		"ยอดรวมทั้งหมด: **%.2f บาท**\n\n"+
		"รายการค้างชำระล่าสุด (แสดง 10 รายการ):\n",
		debtorName, debtorID, totalDebt)

	if len(txs) == 0 {
		detailsMessage += "ไม่พบรายการค้างชำระล่าสุด"
	} else {
		for i, tx := range txs {
			detailsMessage += fmt.Sprintf("%d. **%.2f บาท** - %s (TxID: %d)\n",
				i+1, tx["amount"].(float64), tx["description"].(string), tx["id"].(int))
		}
	}

	// Get PromptPay ID for creating payment request
	_, err = db.GetUserPromptPayID(creditorDbID)
	if err != nil {
		_ = "ไม่พบข้อมูล (กรุณาใช้ !setpromptpay)"
	}

	// Create action buttons
	var components []discordgo.MessageComponent

	// Add request payment button
	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "ส่งคำขอชำระเงิน",
				Style:    discordgo.PrimaryButton,
				CustomID: fmt.Sprintf("%s%s", requestPaymentButtonPrefix, debtorID),
			},
		},
	})

	// Add mark paid buttons for individual transactions if there are any
	if len(txs) > 0 {
		var markPaidButtons []discordgo.MessageComponent
		// Add up to 5 mark paid buttons for recent transactions
		for i := 0; i < min(5, len(txs)); i++ {
			txID := txs[i]["id"].(int)
			amount := txs[i]["amount"].(float64)

			markPaidButtons = append(markPaidButtons, discordgo.Button{
				Label:    fmt.Sprintf("รายการ #%d: %.2f บาท ✓", txID, amount),
				Style:    discordgo.SuccessButton,
				CustomID: fmt.Sprintf("%stx%d", markPaidButtonPrefix, txID),
			})
		}

		if len(markPaidButtons) > 0 {
			components = append(components, discordgo.ActionsRow{
				Components: markPaidButtons,
			})
		}
	}

	// Add close button
	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "ปิด",
				Style:    discordgo.DangerButton,
				CustomID: fmt.Sprintf("%s%s", cancelActionButtonPrefix, debtorID),
			},
		},
	})

	// Respond with the details message and buttons
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    detailsMessage,
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// handleRequestPaymentButton handles the request payment button interaction
func handleRequestPaymentButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "_")
	if len(parts) < 3 {
		respondWithError(s, i, "รูปแบบ custom ID ไม่ถูกต้อง")
		return
	}

	// Extract IDs
	debtorDiscordID := parts[2]
	creditorDiscordID := i.Member.User.ID

	// Get DB IDs
	creditorDbID, err := db.GetOrCreateUser(creditorDiscordID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถระบุตัวตนของคุณในระบบ")
		return
	}

	debtorDbID, err := db.GetOrCreateUser(debtorDiscordID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่สามารถระบุลูกหนี้ <@%s> ในระบบ", debtorDiscordID))
		return
	}

	// Get total debt amount
	totalDebtAmount, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลยอดหนี้รวมได้")
		return
	}

	if totalDebtAmount <= 0.01 {
		// Respond with ephemeral message
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("ยอดเยี่ยม! <@%s> ไม่ได้มีหนี้คงค้างกับคุณในขณะนี้", debtorDiscordID),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get PromptPay ID
	promptPayID, err := db.GetUserPromptPayID(creditorDbID)
	if err != nil {
		promptPayID = "ไม่พบข้อมูล (กรุณาใช้ !setpromptpay)"
	}

	// Get unpaid transactions
	unpaidTxIDs, unpaidTxDetails, _, err := db.GetUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID, 10)
	if err != nil {
		log.Printf("Error fetching transaction details for request payment: %v", err)
		// Continue even if this fails
	}

	// Create content
	debtorName := GetDiscordUsername(s, debtorDiscordID)
	content := fmt.Sprintf("**คำขอชำระเงินถึง %s (<@%s>)**\n"+
		"ยอดค้างชำระทั้งหมด: **%.2f บาท**\n\n"+
		"PromptPay ที่ใช้รับชำระ: `%s`\n\n",
		debtorName, debtorDiscordID, totalDebtAmount, promptPayID)

	if unpaidTxDetails != "" {
		content += "**รายการที่ค้างชำระ:**\n" + unpaidTxDetails
	}

	// Create components
	var components []discordgo.MessageComponent

	// Create payment and detail buttons for the debtor
	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "ชำระเงินทั้งหมด",
				Style:    discordgo.PrimaryButton,
				CustomID: fmt.Sprintf("%s%s", payDebtButtonPrefix, creditorDiscordID),
			},
			discordgo.Button{
				Label:    "ดูรายละเอียดเพิ่มเติม",
				Style:    discordgo.SecondaryButton,
				CustomID: fmt.Sprintf("%s%s", viewDetailButtonPrefix, creditorDiscordID),
			},
		},
	})

	// First, respond to the interaction with an ephemeral message
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "กำลังส่งคำขอชำระเงินไปยังช่องสนทนา...",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	// Then, send a public message with QR code if promptPayID is available
	if promptPayID != "" && promptPayID != "ไม่พบข้อมูล (กรุณาใช้ !setpromptpay)" {
		// Send message to channel (not ephemeral)
		_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Content:    content,
			Components: components,
		})
		if err != nil {
			log.Printf("Error sending request payment message: %v", err)
			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: fmt.Sprintf("⚠️ เกิดข้อผิดพลาดในการส่งคำขอชำระเงิน: %v", err),
			})
			return
		}

		// Generate and send QR code
		generateAndSendQrCode(s, i.ChannelID, promptPayID, totalDebtAmount, debtorDiscordID,
			fmt.Sprintf("คำร้องขอชำระหนี้คงค้างจาก <@%s>", creditorDiscordID), unpaidTxIDs)

		// Send follow-up success message to the interaction
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "✅ คำขอชำระเงินได้ถูกส่งพร้อม QR Code แล้ว",
		})
	} else {
		// Send message without QR code
		_, err = s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Content:    content + "\n⚠️ ไม่สามารถสร้าง QR Code เนื่องจากไม่พบ PromptPay ID ที่ถูกต้อง",
			Components: components,
		})
		if err != nil {
			log.Printf("Error sending request payment message: %v", err)
			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: fmt.Sprintf("⚠️ เกิดข้อผิดพลาดในการส่งคำขอชำระเงิน: %v", err),
			})
			return
		}

		// Send follow-up message
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "✅ คำขอชำระเงินได้ถูกส่งแล้ว แต่ไม่สามารถสร้าง QR Code ได้เนื่องจากไม่พบ PromptPay ID\nกรุณาตั้งค่า PromptPay ID ของคุณด้วยคำสั่ง `!setpromptpay <promptpayID>`",
		})
	}
}

// handleConfirmPaymentButton handles the confirmation of a payment
func handleConfirmPaymentButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "_")
	if len(parts) < 3 {
		respondWithError(s, i, "รูปแบบ custom ID ไม่ถูกต้อง")
		return
	}

	paymentID := parts[2]

	// Get payment details (in a real implementation, this would likely fetch from a database)
	// For this implementation, we'll assume the paymentID contains the transaction ID or creditorID

	if strings.HasPrefix(paymentID, "tx") {
		// Transaction-specific payment confirmation
		txIDStr := strings.TrimPrefix(paymentID, "tx")
		txID, err := strconv.Atoi(txIDStr)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("รหัสรายการไม่ถูกต้อง: %s", txIDStr))
			return
		}

		// Mark the transaction as paid
		err = db.MarkTransactionPaidAndUpdateDebt(txID)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถทำเครื่องหมายว่าชำระแล้ว: %v", err))
			return
		}

		// Respond with success message
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("✅ ยืนยันการชำระเงินสำหรับรายการ #%d เรียบร้อยแล้ว", txID),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	} else {
		// General debt payment confirmation
		creditorID := paymentID
		debtorID := i.Member.User.ID

		debtorDbID, err := db.GetOrCreateUser(debtorID)
		if err != nil {
			respondWithError(s, i, "ไม่สามารถระบุตัวตนของคุณในระบบ")
			return
		}

		creditorDbID, err := db.GetOrCreateUser(creditorID)
		if err != nil {
			respondWithError(s, i, "ไม่สามารถระบุเจ้าหนี้ในระบบ")
			return
		}

		// Get total debt
		totalDebt, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถดึงยอดหนี้รวมได้: %v", err))
			return
		}

		// Process the payment by getting all unpaid transactions and marking them as paid
		txs, err := db.GetRecentTransactions(debtorDbID, creditorDbID, 100, false) // Get up to 100 unpaid transactions
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถดึงข้อมูลรายการค้างชำระได้: %v", err))
			return
		}

		// Mark each transaction as paid
		var markedCount int
		for _, tx := range txs {
			txID := tx["id"].(int)
			err = db.MarkTransactionPaidAndUpdateDebt(txID)
			if err == nil {
				markedCount++
			}
		}

		// Respond with success message
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("✅ ยืนยันการชำระหนี้ให้ <@%s> จำนวน %.2f บาท เรียบร้อยแล้ว\nทำเครื่องหมายรายการชำระแล้ว %d รายการ",
					creditorID, totalDebt, markedCount),
				Flags: discordgo.MessageFlagsEphemeral,
			},
		})
	}
}

// handleCancelActionButton handles the cancellation of an action
func handleCancelActionButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// This button simply dismisses the message
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "ยกเลิกการดำเนินการแล้ว",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// handleDebtDropdown handles the debt dropdown selection
func handleDebtDropdown(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Get the selected value from the dropdown
	selectedOption := i.MessageComponentData().Values[0]

	if strings.HasPrefix(selectedOption, "tx_") {
		// Extract transaction ID
		txIDStr := strings.TrimPrefix(selectedOption, "tx_")
		txID, err := strconv.Atoi(txIDStr)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("รหัสรายการไม่ถูกต้อง: %s", txIDStr))
			return
		}

		// Get transaction details
		txInfo, err := db.GetTransactionInfo(txID)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่พบข้อมูลรายการ ID %d: %v", txID, err))
			return
		}

		payerDbID := txInfo["payer_id"].(int)
		payeeDbID := txInfo["payee_id"].(int)
		amount := txInfo["amount"].(float64)
		description := txInfo["description"].(string)
		created := txInfo["created_at"].(string)
		isPaid := txInfo["already_paid"].(bool)

		payerDiscordID, _ := db.GetDiscordIDFromDbID(payerDbID)
		payeeDiscordID, _ := db.GetDiscordIDFromDbID(payeeDbID)

		status := "ค้างชำระ"
		if isPaid {
			status = "ชำระแล้ว"
		}

		detailsMessage := fmt.Sprintf("**รายละเอียดรายการ #%d**\n"+
			"ผู้ชำระ: <@%s>\n"+
			"ผู้รับ: <@%s>\n"+
			"จำนวน: %.2f บาท\n"+
			"รายละเอียด: %s\n"+
			"วันที่สร้าง: %s\n"+
			"สถานะ: %s",
			txID, payerDiscordID, payeeDiscordID, amount, description, created, status)

		// Create action buttons based on transaction status
		var components []discordgo.MessageComponent

		// Add Pay button if transaction is not paid and user is the debtor
		if !isPaid && i.Member.User.ID == payerDiscordID {
			components = append(components, discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "ชำระเงิน",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("%stx%d", payDebtButtonPrefix, txID),
					},
				},
			})
		}

		// Add Mark as Paid button if transaction is not paid and user is the creditor
		if !isPaid && i.Member.User.ID == payeeDiscordID {
			components = append(components, discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "ทำเครื่องหมายว่าชำระแล้ว",
						Style:    discordgo.SuccessButton,
						CustomID: fmt.Sprintf("%stx%d", markPaidButtonPrefix, txID),
					},
				},
			})
		}

		// Add Close button
		components = append(components, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "ปิด",
					Style:    discordgo.DangerButton,
					CustomID: fmt.Sprintf("%stx%d", cancelActionButtonPrefix, txID),
				},
			},
		})

		// Respond with transaction details and buttons
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content:    detailsMessage,
				Components: components,
				Flags:      discordgo.MessageFlagsEphemeral,
			},
		})
	} else {
		// Handle other dropdown options if needed
		log.Printf("Unknown dropdown value: %s", selectedOption)
		respondWithError(s, i, "ตัวเลือกไม่ถูกต้องหรือไม่ได้รับการรองรับ")
	}
}
