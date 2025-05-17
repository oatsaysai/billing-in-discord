package handlers

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// handlePayDebtButton handles the pay debt button interaction
func handlePayDebtButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the creditor's Discord ID from the custom ID
	customID := i.MessageComponentData().CustomID
	creditorDiscordID := strings.TrimPrefix(customID, payDebtButtonPrefix)

	if creditorDiscordID == "" {
		respondWithError(s, i, "ไม่พบข้อมูลผู้รับเงินที่ถูกต้อง")
		return
	}

	debtorDiscordID := i.Member.User.ID

	// Prevent paying yourself
	if debtorDiscordID == creditorDiscordID {
		respondWithError(s, i, "คุณไม่สามารถชำระเงินให้ตัวเองได้")
		return
	}

	// Get DB IDs
	debtorDbID, err := db.GetOrCreateUser(debtorDiscordID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลผู้ใช้ได้")
		return
	}

	creditorDbID, err := db.GetOrCreateUser(creditorDiscordID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลผู้รับเงินได้")
		return
	}

	// Get total debt amount
	totalDebtAmount, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลยอดหนี้รวมได้")
		return
	}

	if totalDebtAmount <= 0.01 {
		respondWithError(s, i, "คุณไม่มีหนี้คงค้างกับผู้ใช้รายนี้")
		return
	}

	// Get creditor's PromptPay ID
	promptPayID, err := db.GetUserPromptPayID(creditorDbID)
	if err != nil {
		// Continue but note no PromptPay
		promptPayID = "ไม่พบข้อมูล"
	}

	// Get unpaid transaction IDs and details
	unpaidTxIDs, unpaidTxDetails, _, err := db.GetUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID, 5)
	if err != nil {
		log.Printf("Error fetching transaction details for pay debt button: %v", err)
		// Continue even if this fails
	}

	// Build transaction list for the modal
	txIDsString := "ไม่พบรายการ"
	if len(unpaidTxIDs) > 0 {
		txIDsString = fmt.Sprintf("%v", unpaidTxIDs)
	}

	var content strings.Builder
	content.WriteString(fmt.Sprintf("**ยอดหนี้ที่คุณค้างชำระทั้งหมด: %.2f บาท**\n\n", totalDebtAmount))
	content.WriteString(fmt.Sprintf("**PromptPay ID ของผู้รับเงิน:** `%s`\n\n", promptPayID))

	if unpaidTxDetails != "" {
		content.WriteString("**รายการที่ค้างชำระ:**\n")
		content.WriteString(unpaidTxDetails)
	}

	content.WriteString("\nโปรดแนบสลิปการโอนเงินเพื่อยืนยันการชำระหนี้")

	// Respond with a modal and a message
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content.String(),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "ฉันได้โอนเงินแล้ว",
							Style:    discordgo.SuccessButton,
							CustomID: fmt.Sprintf("confirm_payment_%s_%s", creditorDiscordID, txIDsString),
						},
					},
				},
			},
		},
	})

	if err != nil {
		log.Printf("Error responding to pay debt button: %v", err)
	}
}

// handleViewDetailButton handles the "View Details" button
func handleViewDetailButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "_")
	if len(parts) < 3 {
		respondWithError(s, i, "รูปแบบ custom ID ไม่ถูกต้อง")
		return
	}

	targetID := parts[2]
	var detailsMessage string

	// Check if this is for a transaction or a general debt
	if strings.HasPrefix(targetID, "tx") {
		// Transaction detail
		txIDStr := strings.TrimPrefix(targetID, "tx")
		txID, err := strconv.Atoi(txIDStr)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("รหัสรายการไม่ถูกต้อง: %s", txIDStr))
			return
		}

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

		detailsMessage = fmt.Sprintf("**รายละเอียดรายการ #%d**\n"+
			"ผู้ชำระ: <@%s>\n"+
			"ผู้รับ: <@%s>\n"+
			"จำนวน: %.2f บาท\n"+
			"รายละเอียด: %s\n"+
			"วันที่สร้าง: %s\n"+
			"สถานะ: %s",
			txID, payerDiscordID, payeeDiscordID, amount, description, created, status)

	} else {
		// General debt detail
		creditorID := targetID
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

		// Get recent unpaid transactions
		txs, err := db.GetRecentTransactions(debtorDbID, creditorDbID, 5, false)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถดึงข้อมูลรายการล่าสุดได้: %v", err))
			return
		}

		// Get total debt
		totalDebt, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถดึงยอดหนี้รวมได้: %v", err))
			return
		}

		detailsMessage = fmt.Sprintf("**รายละเอียดหนี้ถึง <@%s>**\n"+
			"ยอดรวมทั้งหมด: %.2f บาท\n\n"+
			"รายการค้างชำระล่าสุด (แสดง 5 รายการ):\n",
			creditorID, totalDebt)

		if len(txs) == 0 {
			detailsMessage += "ไม่พบรายการค้างชำระล่าสุด"
		} else {
			for i, tx := range txs {
				detailsMessage += fmt.Sprintf("%d. **%.2f บาท** - %s (TxID: %d)\n",
					i+1, tx["amount"].(float64), tx["description"].(string), tx["id"].(int))
			}
		}
	}

	// Create action buttons for the message
	var components []discordgo.MessageComponent

	if strings.HasPrefix(targetID, "tx") {
		// Transaction-specific buttons
		txIDStr := strings.TrimPrefix(targetID, "tx")
		txID, _ := strconv.Atoi(txIDStr)

		txInfo, err := db.GetTransactionInfo(txID)
		if err == nil {
			isPaid := txInfo["already_paid"].(bool)
			payeeDbID := txInfo["payee_id"].(int)
			payeeDiscordID, _ := db.GetDiscordIDFromDbID(payeeDbID)

			components = append(components, discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "ชำระเงิน",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("%s%s", payDebtButtonPrefix, targetID),
						Disabled: isPaid,
					},
					discordgo.Button{
						Label:    "ทำเครื่องหมายว่าชำระแล้ว",
						Style:    discordgo.SuccessButton,
						CustomID: fmt.Sprintf("%s%s", markPaidButtonPrefix, targetID),
						Disabled: isPaid || i.Member.User.ID != payeeDiscordID, // Only payee can mark as paid
					},
				},
			})
		}
	} else {
		// General debt buttons
		components = append(components, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "ชำระเงิน",
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("%s%s", payDebtButtonPrefix, targetID),
				},
			},
		})
	}

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

// handleRequestPaymentButton handles the request payment button
func handleRequestPaymentButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the debtor's Discord ID from the custom ID
	customID := i.MessageComponentData().CustomID
	debtorDiscordID := strings.TrimPrefix(customID, requestPaymentButtonPrefix)

	if debtorDiscordID == "" {
		respondWithError(s, i, "ไม่พบข้อมูลลูกหนี้ที่ถูกต้อง")
		return
	}

	creditorDiscordID := i.Member.User.ID

	// Get DB IDs
	creditorDbID, err := db.GetOrCreateUser(creditorDiscordID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลผู้ใช้ได้")
		return
	}

	debtorDbID, err := db.GetOrCreateUser(debtorDiscordID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลลูกหนี้ได้")
		return
	}

	// Get PromptPay ID
	promptPayID, err := db.GetUserPromptPayID(creditorDbID)
	if err != nil {
		respondWithError(s, i, "คุณยังไม่ได้ตั้งค่า PromptPay ID กรุณาใช้คำสั่ง !setpromptpay ก่อน")
		return
	}

	// Get total debt amount
	totalDebtAmount, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลยอดหนี้รวมได้")
		return
	}

	if totalDebtAmount <= 0.01 {
		respondWithError(s, i, "ผู้ใช้รายนี้ไม่มีหนี้คงค้างกับคุณ")
		return
	}

	// Get unpaid transaction IDs and details
	unpaidTxIDs, _, _, err := db.GetUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID, 10)
	if err != nil {
		log.Printf("Error fetching transaction details for request payment button: %v", err)
		// Continue even if this fails
	}

	// Respond with confirmation first
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("กำลังสร้าง QR Code สำหรับชำระเงิน %.2f บาท จาก <@%s>...", totalDebtAmount, debtorDiscordID),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error responding to request payment button: %v", err)
		return
	}

	// Generate QR code and send to DM
	dmChannel, err := s.UserChannelCreate(i.Member.User.ID)
	if err != nil {
		log.Printf("Error creating DM channel: %v", err)
		followUpError(s, i, "ไม่สามารถส่งข้อความส่วนตัวถึงคุณได้ กรุณาเปิดการรับข้อความส่วนตัวจากสมาชิกในเซิร์ฟเวอร์")
		return
	}

	description := fmt.Sprintf("คำร้องขอชำระหนี้คงค้างไปยัง <@%s>", debtorDiscordID)
	GenerateAndSendQrCode(s, dmChannel.ID, promptPayID, totalDebtAmount, debtorDiscordID, description, unpaidTxIDs)

	// Send a public message in the channel
	publicMessage := fmt.Sprintf("<@%s> ได้ส่งคำขอชำระเงิน %.2f บาท ไปยัง <@%s> แล้ว",
		creditorDiscordID, totalDebtAmount, debtorDiscordID)

	_, err = s.ChannelMessageSend(i.ChannelID, publicMessage)
	if err != nil {
		log.Printf("Error sending public message: %v", err)
	}

	// Send a follow-up to the interaction confirming the QR was sent to DM
	followUpMessage(s, i, "ส่ง QR Code ไปยังข้อความส่วนตัวของคุณแล้ว")
}

// handleConfirmPaymentButton handles the confirmation of payment button
func handleConfirmPaymentButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the creditor's Discord ID and TxIDs from the custom ID
	customID := i.MessageComponentData().CustomID
	parts := strings.SplitN(strings.TrimPrefix(customID, confirmPaymentButtonPrefix), "_", 2)

	if len(parts) != 2 {
		respondWithError(s, i, "รูปแบบ ID ไม่ถูกต้อง")
		return
	}

	creditorDiscordID := parts[0]
	txIDsString := parts[1]

	if creditorDiscordID == "" {
		respondWithError(s, i, "ไม่พบข้อมูลผู้รับเงินที่ถูกต้อง")
		return
	}

	// Respond with a message asking to upload the slip
	content := fmt.Sprintf("โปรดตอบกลับข้อความนี้พร้อมแนบสลิปการโอนเงินเพื่อยืนยันการชำระเงินให้กับ <@%s>\n", creditorDiscordID)

	// If we have TxIDs, include them in the content for reference
	if txIDsString != "ไม่พบรายการ" {
		content += fmt.Sprintf("(เกี่ยวข้องกับรายการ TxIDs: %s)", txIDsString)
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error responding to confirm payment button: %v", err)
	}
}

// handleDebtDropdown handles the debt selection dropdown
func handleDebtDropdown(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Get the selected value
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		respondWithError(s, i, "ไม่พบรายการที่เลือก")
		return
	}

	// Extract the transaction ID
	txIDStr := strings.TrimPrefix(values[0], "tx_")
	txID, err := strconv.Atoi(txIDStr)
	if err != nil {
		respondWithError(s, i, "รหัสรายการไม่ถูกต้อง")
		return
	}

	// Get transaction details
	txInfo, err := db.GetTransactionInfo(txID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่พบรายการ TxID %d", txID))
		return
	}

	// Format the details
	var content strings.Builder
	content.WriteString(fmt.Sprintf("**รายละเอียดรายการ TxID %d:**\n\n", txID))

	description := txInfo["description"].(string)
	amount := txInfo["amount"].(float64)
	createdAt := txInfo["created_at"].(time.Time)
	isPaid := txInfo["already_paid"].(bool)
	paidStatus := "🔴 ยังไม่ชำระ"
	if isPaid {
		paidStatus = "✅ ชำระแล้ว"
	}

	payerDiscordID, _ := db.GetDiscordIDFromDbID(txInfo["payer_id"].(int))
	payeeDiscordID, _ := db.GetDiscordIDFromDbID(txInfo["payee_id"].(int))

	payerName := GetDiscordUsername(s, payerDiscordID)
	payeeName := GetDiscordUsername(s, payeeDiscordID)

	content.WriteString(fmt.Sprintf("**จำนวนเงิน:** %.2f บาท\n", amount))
	content.WriteString(fmt.Sprintf("**สถานะ:** %s\n", paidStatus))
	content.WriteString(fmt.Sprintf("**รายละเอียด:** %s\n", description))
	content.WriteString(fmt.Sprintf("**วันที่สร้าง:** %s\n", createdAt.Format("02/01/2006 15:04:05")))
	content.WriteString(fmt.Sprintf("**ผู้จ่าย:** %s (<@%s>)\n", payerName, payerDiscordID))
	content.WriteString(fmt.Sprintf("**ผู้รับ:** %s (<@%s>)\n", payeeName, payeeDiscordID))

	// Add buttons based on the transaction status and user role
	var components []discordgo.MessageComponent
	userDiscordID := i.Member.User.ID

	if !isPaid {
		if userDiscordID == payerDiscordID {
			// Payer can pay the transaction
			components = append(components, discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "ชำระเงิน",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("%s%s", payDebtButtonPrefix, payeeDiscordID),
					},
				},
			})
		} else if userDiscordID == payeeDiscordID {
			// Payee can mark as paid or request payment
			components = append(components, discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "ทำเครื่องหมายว่าชำระแล้ว",
						Style:    discordgo.SuccessButton,
						CustomID: fmt.Sprintf("mark_paid_%d", txID),
					},
					discordgo.Button{
						Label:    "ขอชำระเงิน",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("%s%s", requestPaymentButtonPrefix, payerDiscordID),
					},
				},
			})
		}
	}

	// Respond with the transaction details
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    content.String(),
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error responding to debt dropdown: %v", err)
	}
}

// handleBillSkipButton handles the bill skip button
func handleBillSkipButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the item index from the custom ID
	customID := i.MessageComponentData().CustomID
	itemIndexStr := strings.TrimPrefix(customID, "bill_skip_")

	itemIndex, err := strconv.Atoi(itemIndexStr)
	if err != nil {
		respondWithError(s, i, "รหัสรายการไม่ถูกต้อง")
		return
	}

	// Respond with a confirmation message
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("ข้ามรายการที่ %d แล้ว", itemIndex+1),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error responding to bill skip button: %v", err)
	}

	// Update the original message to indicate the item was skipped
	// This would require storing and retrieving the original message content
	// For now, we'll just leave it as is
}

// handleBillCancelButton handles the cancel button for bills
func handleBillCancelButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Respond by updating the message to indicate cancellation
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "ยกเลิกการประมวลผลบิลแล้ว",
			Components: []discordgo.MessageComponent{},
		},
	})

	if err != nil {
		log.Printf("Error responding to bill cancel button: %v", err)
	}
}

// followUpMessage sends a follow-up ephemeral message to an interaction
func followUpMessage(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: message,
		Flags:   discordgo.MessageFlagsEphemeral,
	})

	if err != nil {
		log.Printf("Error sending follow-up message: %v", err)
	}
}

// followUpError sends an error follow-up message
func followUpError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	followUpMessage(s, i, "⚠️ "+message)
}
