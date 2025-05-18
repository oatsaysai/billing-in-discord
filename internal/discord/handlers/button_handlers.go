package handlers

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	pp "github.com/Frontware/promptpay"
	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
	"os"
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

	// Generate QR code if PromptPay ID is available
	if promptPayID != "ไม่พบข้อมูล" {
		// Create a file name for the QR code
		filename := fmt.Sprintf("qr_%s_%d.jpg", creditorDiscordID, time.Now().UnixNano())

		// Generate QR code
		payment := pp.PromptPay{PromptPayID: promptPayID, Amount: totalDebtAmount}
		qrcodeStr, err := payment.Gen()
		if err != nil {
			log.Printf("Error generating PromptPay string for creditor %s: %v", creditorDiscordID, err)
		} else {
			qrc, err := qrcode.New(qrcodeStr)
			if err != nil {
				log.Printf("Error creating QR code for creditor %s: %v", creditorDiscordID, err)
			} else {
				fileWriter, err := standard.New(filename)
				if err != nil {
					log.Printf("Error creating file writer for QR: %v", err)
				} else {
					if err = qrc.Save(fileWriter); err != nil {
						log.Printf("Error saving QR image: %v", err)
						os.Remove(filename) // Clean up
					} else {
						// QR code successfully generated, now send it as a DM
						file, err := os.Open(filename)
						if err != nil {
							log.Printf("Error opening QR file: %v", err)
						} else {
							defer file.Close()
							defer os.Remove(filename) // Clean up after sending

							// Create DM channel
							channel, err := s.UserChannelCreate(debtorDiscordID)
							if err != nil {
								log.Printf("Error creating DM channel: %v", err)
							} else {
								dmContent := fmt.Sprintf("**QR Code สำหรับชำระเงิน %.2f บาท ให้กับ <@%s>**\n\n**PromptPay ID:** `%s`",
									totalDebtAmount, creditorDiscordID, promptPayID)

								_, err = s.ChannelFileSendWithMessage(channel.ID, dmContent, filename, file)
								if err != nil {
									log.Printf("Error sending QR in DM: %v", err)
								}
							}
						}
					}
				}
			}
		}
	}

	content.WriteString("\nคุณสามารถยืนยันการชำระเงินด้วยสลิป หรือขอให้เจ้าหนี้ยืนยันการชำระด้วยตนเองได้")

	// Respond with a message and two buttons
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content.String(),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "ยืนยันการชำระเงินด้วยสลิป",
							Style:    discordgo.SuccessButton,
							CustomID: fmt.Sprintf("confirm_payment_%s_%s", creditorDiscordID, txIDsString),
						},
						discordgo.Button{
							Label:    "ยืนยันการชำระเงินโดยไม่มีสลิป",
							Style:    discordgo.PrimaryButton,
							CustomID: fmt.Sprintf("confirm_payment_no_slip_%s_%s", creditorDiscordID, txIDsString),
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
		// ตรวจสอบว่าเป็นการดูเงินที่คนอื่นค้างเรา หรือเราค้างคนอื่น
		// ด้วยการดูตัวแรกของ targetID - ถ้ามี "c" นำหน้า แสดงว่าเป็นการดูเงินที่เราค้างคนอื่น
		// ถ้ามี "d" นำหน้า แสดงว่าเป็นการดูเงินที่คนอื่นค้างเรา

		var debtorID, creditorID string
		var isOwed bool = false // เราเป็นเจ้าหนี้หรือไม่ (คนอื่นเป็นหนี้เรา)

		if strings.HasPrefix(targetID, "c") {
			// รูปแบบ "c123456789" - เราเป็นลูกหนี้ คนอื่นเป็นเจ้าหนี้
			creditorID = strings.TrimPrefix(targetID, "c")
			debtorID = i.Member.User.ID
			isOwed = false
		} else if strings.HasPrefix(targetID, "d") {
			// รูปแบบ "d123456789" - เราเป็นเจ้าหนี้ คนอื่นเป็นลูกหนี้
			debtorID = strings.TrimPrefix(targetID, "d")
			creditorID = i.Member.User.ID
			isOwed = true
		} else {
			// รูปแบบเดิมเพื่อความเข้ากันได้กับโค้ดเก่า
			creditorID = targetID
			debtorID = i.Member.User.ID
			isOwed = false
		}

		debtorDbID, err := db.GetOrCreateUser(debtorID)
		if err != nil {
			respondWithError(s, i, "ไม่สามารถระบุตัวตนของลูกหนี้ในระบบ")
			return
		}

		creditorDbID, err := db.GetOrCreateUser(creditorID)
		if err != nil {
			respondWithError(s, i, "ไม่สามารถระบุตัวตนของเจ้าหนี้ในระบบ")
			return
		}

		// Get recent unpaid transactions - ส่ง debtorDbID และ creditorDbID ให้ถูกต้อง
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

		// ปรับข้อความตามกรณี
		if isOwed {
			// แสดงว่าคนอื่นเป็นหนี้เรา
			detailsMessage = fmt.Sprintf("**รายละเอียดหนี้ที่ <@%s> ค้างชำระให้คุณ**\n"+
				"ยอดรวมทั้งหมด: %.2f บาท\n\n"+
				"รายการค้างชำระล่าสุด (แสดง 5 รายการ):\n",
				debtorID, totalDebt)
		} else {
			// แสดงว่าเราเป็นหนี้คนอื่น
			detailsMessage = fmt.Sprintf("**รายละเอียดหนี้ที่คุณค้างชำระให้ <@%s>**\n"+
				"ยอดรวมทั้งหมด: %.2f บาท\n\n"+
				"รายการค้างชำระล่าสุด (แสดง 5 รายการ):\n",
				creditorID, totalDebt)
		}

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
			payerDbID := txInfo["payer_id"].(int)
			payeeDiscordID, _ := db.GetDiscordIDFromDbID(payeeDbID)
			payerDiscordID, _ := db.GetDiscordIDFromDbID(payerDbID)

			currentUserID := i.Member.User.ID
			var actionButtons []discordgo.MessageComponent

			if currentUserID == payerDiscordID && !isPaid {
				// ลูกหนี้เห็นปุ่มชำระเงินเฉพาะรายการนี้
				actionButtons = append(actionButtons, discordgo.Button{
					Label:    "ชำระเงินเฉพาะรายการนี้",
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("pay_tx_%d", txID),
					Disabled: isPaid,
				})
			}

			if currentUserID == payeeDiscordID && !isPaid {
				// เจ้าหนี้เห็นปุ่มทำเครื่องหมายว่าชำระแล้ว และขอชำระเงิน
				actionButtons = append(actionButtons, discordgo.Button{
					Label:    "ทำเครื่องหมายว่าชำระแล้ว",
					Style:    discordgo.SuccessButton,
					CustomID: fmt.Sprintf("%s%s", markPaidButtonPrefix, txIDStr),
				})
				actionButtons = append(actionButtons, discordgo.Button{
					Label:    "ขอชำระเงิน",
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("%s%s", requestPaymentButtonPrefix, payerDiscordID),
				})
			}

			if len(actionButtons) > 0 {
				components = append(components, discordgo.ActionsRow{
					Components: actionButtons,
				})
			}
		}
	} else {
		// ตรวจสอบว่าเป็นลูกหนี้หรือเจ้าหนี้จาก targetID
		var actionButtons []discordgo.MessageComponent
		var debtorDiscordID, creditorDiscordID string

		if strings.HasPrefix(targetID, "c") {
			// เราเป็นลูกหนี้ คนอื่นเป็นเจ้าหนี้
			creditorDiscordID = strings.TrimPrefix(targetID, "c")
			debtorDiscordID = i.Member.User.ID

			// ลูกหนี้เห็นปุ่มชำระเงิน
			actionButtons = append(actionButtons, discordgo.Button{
				Label:    "ชำระเงิน",
				Style:    discordgo.PrimaryButton,
				CustomID: fmt.Sprintf("%s%s", payDebtButtonPrefix, creditorDiscordID),
			})
		} else if strings.HasPrefix(targetID, "d") {
			// เราเป็นเจ้าหนี้ คนอื่นเป็นลูกหนี้
			debtorDiscordID = strings.TrimPrefix(targetID, "d")
			creditorDiscordID = i.Member.User.ID

			// เจ้าหนี้เห็นปุ่มขอชำระเงิน
			actionButtons = append(actionButtons, discordgo.Button{
				Label:    "ขอชำระเงิน",
				Style:    discordgo.PrimaryButton,
				CustomID: fmt.Sprintf("%s%s", requestPaymentButtonPrefix, debtorDiscordID),
			})
		} else {
			// รูปแบบเดิมเพื่อความเข้ากันได้กับโค้ดเก่า
			creditorDiscordID = targetID
			debtorDiscordID = i.Member.User.ID
			actionButtons = append(actionButtons, discordgo.Button{
				Label:    "ชำระเงิน",
				Style:    discordgo.PrimaryButton,
				CustomID: fmt.Sprintf("%s%s", payDebtButtonPrefix, creditorDiscordID),
			})
		}

		if len(actionButtons) > 0 {
			components = append(components, discordgo.ActionsRow{
				Components: actionButtons,
			})
		}
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

	// Generate QR code and send to the channel where the interaction happened
	// This way, the debtor can see the payment request
	description := fmt.Sprintf("ชำระหนี้ให้ <@%s>", creditorDiscordID)
	GenerateAndSendQrCode(s, i.ChannelID, promptPayID, totalDebtAmount, debtorDiscordID, description, unpaidTxIDs)

	// Send a confirmation message to the creditor (person who requested the payment)
	followUpMessage(s, i, fmt.Sprintf("ได้ส่งคำขอชำระเงิน %.2f บาท ไปยัง <@%s> แล้ว และได้ส่ง QR code ไปทางข้อความส่วนตัวด้วย", totalDebtAmount, debtorDiscordID))
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

// handleConfirmPaymentNoSlipButton handles the confirmation of payment without slip button
func handleConfirmPaymentNoSlipButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the creditor's Discord ID and TxIDs from the custom ID
	customID := i.MessageComponentData().CustomID
	parts := strings.SplitN(strings.TrimPrefix(customID, confirmPaymentNoSlipPrefix), "_", 2)

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

	debtorDiscordID := i.Member.User.ID

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

	// First respond to the interaction
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("กำลังส่งคำขอยืนยันการชำระเงิน %.2f บาท ไปยัง <@%s> โปรดรอการยืนยันจากผู้รับเงิน", totalDebtAmount, creditorDiscordID),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error responding to confirm payment no slip button: %v", err)
		return
	}

	// Create a DM channel with the creditor
	creditorChannel, err := s.UserChannelCreate(creditorDiscordID)
	if err != nil {
		log.Printf("Could not create DM channel with creditor %s: %v", creditorDiscordID, err)
		followUpError(s, i, "ไม่สามารถส่งข้อความไปยังผู้รับเงินได้")
		return
	}

	// Get debtor's name
	debtorName := GetDiscordUsername(s, debtorDiscordID)

	// Send DM to creditor with confirmation buttons
	verificationMessage := fmt.Sprintf("<@%s> (**%s**) แจ้งว่าได้ชำระเงิน **%.2f บาท** ให้คุณแล้ว โดยไม่มีสลิปการโอนเงิน\n\nกรุณายืนยันว่าคุณได้รับเงินจำนวนนี้แล้วจริงๆ",
		debtorDiscordID, debtorName, totalDebtAmount)

	// If we have TxIDs, include them in the content for reference
	if txIDsString != "ไม่พบรายการ" {
		verificationMessage += fmt.Sprintf("\n(เกี่ยวข้องกับรายการ TxIDs: %s)", txIDsString)
	}

	// Create unique custom IDs for the verification buttons that include both user IDs and transaction IDs
	confirmButtonID := fmt.Sprintf("verify_payment_confirm_%s_%s_%s", debtorDiscordID, creditorDiscordID, txIDsString)
	rejectButtonID := fmt.Sprintf("verify_payment_reject_%s_%s_%s", debtorDiscordID, creditorDiscordID, txIDsString)

	// Send DM to creditor with buttons
	_, err = s.ChannelMessageSendComplex(creditorChannel.ID, &discordgo.MessageSend{
		Content: verificationMessage,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "ยืนยัน ฉันได้รับเงินแล้ว",
						Style:    discordgo.SuccessButton,
						CustomID: confirmButtonID,
					},
					discordgo.Button{
						Label:    "ปฏิเสธ ฉันยังไม่ได้รับเงิน",
						Style:    discordgo.DangerButton,
						CustomID: rejectButtonID,
					},
				},
			},
		},
	})

	if err != nil {
		log.Printf("Error sending verification message to creditor: %v", err)
		followUpError(s, i, "ไม่สามารถส่งคำขอยืนยันไปยังผู้รับเงินได้")
		return
	}

	// Also send a notification in the channel where the interaction happened if it's not a DM
	if !strings.HasPrefix(i.ChannelID, "@me") {
		// This is a public channel, send a confirmation message
		s.ChannelMessageSend(i.ChannelID, fmt.Sprintf("<@%s> ได้แจ้งว่าชำระเงิน %.2f บาท ให้กับ <@%s> แล้ว และกำลังรอการยืนยันจากผู้รับเงิน",
			debtorDiscordID, totalDebtAmount, creditorDiscordID))
	}
}

// handleVerifyPaymentConfirmButton handles the confirmation of payment verification button
func handleVerifyPaymentConfirmButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the debtor's Discord ID, creditor's Discord ID, and TxIDs from the custom ID
	customID := i.MessageComponentData().CustomID
	parts := strings.SplitN(strings.TrimPrefix(customID, verifyPaymentConfirmPrefix), "_", 3)

	if len(parts) != 3 {
		respondWithError(s, i, "รูปแบบ ID ไม่ถูกต้อง")
		return
	}

	debtorDiscordID := parts[0]
	creditorDiscordID := parts[1]
	txIDsString := parts[2]

	creditorUserID := i.Member.User.ID

	// Verify that the creditor is actually the person clicking the button
	if creditorUserID != creditorDiscordID {
		respondWithError(s, i, "คุณไม่มีสิทธิ์ยืนยันการชำระเงินนี้")
		return
	}

	// Get DB IDs
	debtorDbID, err := db.GetOrCreateUser(debtorDiscordID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลลูกหนี้ได้")
		return
	}

	creditorDbID, err := db.GetOrCreateUser(creditorDiscordID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลเจ้าหนี้ได้")
		return
	}

	// Get total debt amount
	totalDebtAmount, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลยอดหนี้รวมได้")
		return
	}

	// Update transaction records if specific TxIDs were provided
	var txIDs []int
	if txIDsString != "ไม่พบรายการ" && txIDsString != "[]" {
		// Try to parse the TxIDs string (format like "[1, 2, 3]")
		idStr := strings.Trim(txIDsString, "[]")
		idParts := strings.Split(idStr, ",")

		for _, part := range idParts {
			// Trim spaces and convert to int
			trimmed := strings.TrimSpace(part)
			id, err := strconv.Atoi(trimmed)
			if err != nil {
				log.Printf("Error parsing TxID '%s': %v", trimmed, err)
				continue
			}
			txIDs = append(txIDs, id)
		}
	}

	if len(txIDs) > 0 {
		// Mark specific transactions as paid
		for _, txID := range txIDs {
			err := db.MarkTransactionPaidAndUpdateDebt(txID)
			if err != nil {
				log.Printf("Error marking transaction %d as paid: %v", txID, err)
				// Continue with other transactions
			}
		}
	} else {
		// If no specific transactions were provided, just reduce the debt by the total amount
		err = db.ReduceDebtFromPayment(debtorDiscordID, creditorDiscordID, totalDebtAmount)
		if err != nil {
			log.Printf("Error reducing debt: %v", err)
			respondWithError(s, i, "ไม่สามารถอัปเดตข้อมูลหนี้สินในระบบได้")
			return
		}
	}

	// Respond to the creditor with confirmation
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ คุณได้ยืนยันการรับชำระหนี้จำนวน %.2f บาท จาก <@%s> เรียบร้อยแล้ว ระบบได้อัปเดตข้อมูลหนี้สินแล้ว",
				totalDebtAmount, debtorDiscordID),
			Components: []discordgo.MessageComponent{}, // Remove buttons
		},
	})

	if err != nil {
		log.Printf("Error responding to verification button: %v", err)
	}

	// Notify the debtor via DM
	debtorChannel, err := s.UserChannelCreate(debtorDiscordID)
	if err != nil {
		log.Printf("Could not create DM channel with debtor %s: %v", debtorDiscordID, err)
	} else {
		//debtorName := GetDiscordUsername(s, debtorDiscordID)
		creditorName := GetDiscordUsername(s, creditorDiscordID)

		_, err = s.ChannelMessageSend(debtorChannel.ID, fmt.Sprintf("✅ <@%s> (**%s**) ได้ยืนยันการรับชำระหนี้จำนวน %.2f บาท จากคุณเรียบร้อยแล้ว ระบบได้อัปเดตข้อมูลหนี้สินแล้ว",
			creditorDiscordID, creditorName, totalDebtAmount))

		if err != nil {
			log.Printf("Error sending DM to debtor: %v", err)
		}
	}
}

// handleVerifyPaymentRejectButton handles the rejection of payment verification button
func handleVerifyPaymentRejectButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the debtor's Discord ID, creditor's Discord ID, and TxIDs from the custom ID
	customID := i.MessageComponentData().CustomID
	parts := strings.SplitN(strings.TrimPrefix(customID, verifyPaymentRejectPrefix), "_", 3)

	if len(parts) != 3 {
		respondWithError(s, i, "รูปแบบ ID ไม่ถูกต้อง")
		return
	}

	debtorDiscordID := parts[0]
	creditorDiscordID := parts[1]

	creditorUserID := i.Member.User.ID

	// Verify that the creditor is actually the person clicking the button
	if creditorUserID != creditorDiscordID {
		respondWithError(s, i, "คุณไม่มีสิทธิ์ยืนยันการชำระเงินนี้")
		return
	}

	// Get names for the notification messages
	debtorName := GetDiscordUsername(s, debtorDiscordID)
	creditorName := GetDiscordUsername(s, creditorDiscordID)

	// Respond to the creditor with confirmation
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("❌ คุณได้ปฏิเสธการยืนยันรับชำระหนี้จาก <@%s> (**%s**) ไม่มีการเปลี่ยนแปลงข้อมูลในระบบ",
				debtorDiscordID, debtorName),
			Components: []discordgo.MessageComponent{}, // Remove buttons
		},
	})

	if err != nil {
		log.Printf("Error responding to verification reject button: %v", err)
	}

	// Notify the debtor via DM
	debtorChannel, err := s.UserChannelCreate(debtorDiscordID)
	if err != nil {
		log.Printf("Could not create DM channel with debtor %s: %v", debtorDiscordID, err)
	} else {
		_, err = s.ChannelMessageSend(debtorChannel.ID, fmt.Sprintf("❌ <@%s> (**%s**) ได้ปฏิเสธการยืนยันรับชำระหนี้จากคุณ โปรดติดต่อเจ้าหนี้โดยตรงเพื่อตรวจสอบการชำระเงิน",
			creditorDiscordID, creditorName))

		if err != nil {
			log.Printf("Error sending DM to debtor: %v", err)
		}
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
						Label:    "ชำระเงินเฉพาะรายการนี้",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("pay_tx_%d", txID),
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

// handleMarkPaidButton handles the mark-paid button interaction
func handleMarkPaidButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract transaction ID from the custom ID
	customID := i.MessageComponentData().CustomID
	txIDStr := strings.TrimPrefix(customID, markPaidButtonPrefix)

	txID, err := strconv.Atoi(txIDStr)
	if err != nil {
		respondWithError(s, i, "รหัสรายการไม่ถูกต้อง")
		return
	}

	// Verify that the current user is actually the creditor
	userID := i.Member.User.ID

	// Get the transaction details
	txInfo, err := db.GetTransactionInfo(txID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่พบข้อมูลรายการ TxID %d", txID))
		return
	}

	// Check if the transaction is already paid
	isPaid := txInfo["already_paid"].(bool)
	if isPaid {
		respondWithError(s, i, fmt.Sprintf("รายการ TxID %d ถูกทำเครื่องหมายว่าชำระแล้ว", txID))
		return
	}

	// Verify that the user is the creditor
	payeeDbID := txInfo["payee_id"].(int)
	payeeDiscordID, err := db.GetDiscordIDFromDbID(payeeDbID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถยืนยันตัวตนของผู้รับเงินได้")
		return
	}

	if userID != payeeDiscordID {
		respondWithError(s, i, "คุณไม่มีสิทธิ์ทำเครื่องหมายว่ารายการนี้ชำระแล้ว (คุณไม่ใช่ผู้รับเงิน)")
		return
	}

	// Mark the transaction as paid
	err = db.MarkTransactionPaidAndUpdateDebt(txID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่สามารถทำเครื่องหมายว่ารายการ TxID %d ชำระแล้ว: %v", txID, err))
		return
	}

	// Get debtor's Discord ID for notification
	payerDbID := txInfo["payer_id"].(int)
	payerDiscordID, err := db.GetDiscordIDFromDbID(payerDbID)
	if err != nil {
		log.Printf("Warning: Could not get debtor's Discord ID for notification: %v", err)
		// Continue even if this fails
	} else {
		// Send a DM to the debtor if we got their ID
		SendDirectMessage(s, payerDiscordID, fmt.Sprintf("รายการชำระเงิน TxID %d จำนวน %.2f บาท ถูกทำเครื่องหมายว่าชำระแล้วโดย <@%s>",
			txID, txInfo["amount"].(float64), userID))
	}

	// Respond with a success message
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ ทำเครื่องหมายว่ารายการ TxID %d ชำระแล้วเรียบร้อย!", txID),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error responding to mark paid button: %v", err)
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
