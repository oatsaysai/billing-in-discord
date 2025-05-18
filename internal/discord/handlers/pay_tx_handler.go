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

// handlePayTxButton handles the pay specific transaction button interaction
func handlePayTxButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the transaction ID from the custom ID
	customID := i.MessageComponentData().CustomID
	txIDStr := strings.TrimPrefix(customID, payTxButtonPrefix)

	txID, err := strconv.Atoi(txIDStr)
	if err != nil {
		respondWithError(s, i, "รหัสรายการไม่ถูกต้อง")
		return
	}

	// Get transaction info
	txInfo, err := db.GetTransactionInfo(txID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่พบข้อมูลรายการ ID %d: %v", txID, err))
		return
	}

	// Check if already paid
	isPaid := txInfo["already_paid"].(bool)
	if isPaid {
		respondWithError(s, i, fmt.Sprintf("รายการ ID %d ได้ชำระเงินแล้ว", txID))
		return
	}

	// Get user IDs
	payerDbID := txInfo["payer_id"].(int)
	payeeDbID := txInfo["payee_id"].(int)
	amount := txInfo["amount"].(float64)

	payerDiscordID, err := db.GetDiscordIDFromDbID(payerDbID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถระบุตัวตนของผู้จ่ายเงินได้")
		return
	}

	payeeDiscordID, err := db.GetDiscordIDFromDbID(payeeDbID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถระบุตัวตนของผู้รับเงินได้")
		return
	}

	// Verify user is the payer
	if i.Member.User.ID != payerDiscordID {
		respondWithError(s, i, "คุณไม่ใช่ผู้จ่ายเงินของรายการนี้")
		return
	}

	// Get creditor's PromptPay ID
	promptPayID, err := db.GetUserPromptPayID(payeeDbID)
	if err != nil {
		// Continue but note no PromptPay
		promptPayID = "ไม่พบข้อมูล"
	}

	var content strings.Builder
	content.WriteString(fmt.Sprintf("**รายละเอียดรายการที่ต้องชำระเงิน (ID %d):**\n\n", txID))
	content.WriteString(fmt.Sprintf("**จำนวน:** %.2f บาท\n", amount))
	content.WriteString(fmt.Sprintf("**รายละเอียด:** %s\n", txInfo["description"].(string)))
	content.WriteString(fmt.Sprintf("**ผู้รับเงิน:** <@%s>\n", payeeDiscordID))
	content.WriteString(fmt.Sprintf("**PromptPay ID ของผู้รับเงิน:** `%s`\n\n", promptPayID))

	// Create txIDsString for passing to the confirmation buttons
	txIDsString := fmt.Sprintf("[%d]", txID)

	// Generate QR code if PromptPay ID is available
	if promptPayID != "ไม่พบข้อมูล" {
		// Create a file name for the QR code
		filename := fmt.Sprintf("qr_tx_%d_%d.jpg", txID, time.Now().UnixNano())

		// Generate QR code
		payment := pp.PromptPay{PromptPayID: promptPayID, Amount: amount}
		qrcodeStr, err := payment.Gen()
		if err != nil {
			log.Printf("Error generating PromptPay string for tx %d: %v", txID, err)
		} else {
			qrc, err := qrcode.New(qrcodeStr)
			if err != nil {
				log.Printf("Error creating QR code for tx %d: %v", txID, err)
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
							channel, err := s.UserChannelCreate(payerDiscordID)
							if err != nil {
								log.Printf("Error creating DM channel: %v", err)
							} else {
								dmContent := fmt.Sprintf("**QR Code สำหรับชำระเงิน %.2f บาท ให้กับ <@%s>**\n\n**รายการ ID:** %d\n**PromptPay ID:** `%s`",
									amount, payeeDiscordID, txID, promptPayID)

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
			Flags:   discordgo.MessageFlagsEphemeral, // Show only to the user
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "ยืนยันการชำระเงินด้วยสลิป",
							Style:    discordgo.SuccessButton,
							CustomID: fmt.Sprintf("confirm_payment_%s_%s", payeeDiscordID, txIDsString),
						},
						discordgo.Button{
							Label:    "ยืนยันการชำระเงินโดยไม่มีสลิป",
							Style:    discordgo.PrimaryButton,
							CustomID: fmt.Sprintf("confirm_payment_no_slip_%s_%s", payeeDiscordID, txIDsString),
						},
					},
				},
			},
		},
	})

	if err != nil {
		log.Printf("Error responding to pay tx button: %v", err)
	}
}
