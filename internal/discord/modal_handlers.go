package discord

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// handlePayDebtModalSubmit handles the payment modal submission
func handlePayDebtModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	// Get the target ID from the modal custom ID
	// Format: modal_pay_debt_<targetID>
	parts := strings.Split(data.CustomID, "_")
	if len(parts) < 4 {
		respondWithError(s, i, "รูปแบบ modal ID ไม่ถูกต้อง")
		return
	}

	targetID := parts[3]

	// Get the input values directly from the data
	amountStr := data.Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	promptPayID := data.Components[1].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	note := data.Components[2].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value

	// Parse the amount
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount <= 0 {
		respondWithError(s, i, fmt.Sprintf("จำนวนเงินไม่ถูกต้อง: %s", amountStr))
		return
	}

	// Get the creditor information
	var creditorID string
	var description string
	var txIDs []int

	if strings.HasPrefix(targetID, "tx") {
		// Transaction-specific payment
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

		payeeDbID := txInfo["payee_id"].(int)
		originalAmount := txInfo["amount"].(float64)
		originalDesc := txInfo["description"].(string)

		// Get Discord ID from DB ID
		creditorDiscordID, err := db.GetDiscordIDFromDbID(payeeDbID)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถระบุผู้รับเงินได้: %v", err))
			return
		}
		creditorID = creditorDiscordID

		// Set description for the payment
		if amount == originalAmount {
			description = fmt.Sprintf("รายการ #%d: %s", txID, originalDesc)
		} else {
			description = fmt.Sprintf("การชำระบางส่วนสำหรับรายการ #%d: %s", txID, originalDesc)
		}

		if note != "" {
			description += fmt.Sprintf(" (หมายเหตุ: %s)", note)
		}

		// Add transaction ID to list
		txIDs = []int{txID}
	} else {
		// General debt payment
		creditorID = targetID

		// Set description for the payment
		description = "การชำระหนี้ทั่วไป"
		if note != "" {
			description += fmt.Sprintf(" (หมายเหตุ: %s)", note)
		}

		// Get unpaid transactions up to the amount
		debtorDbID, err := db.GetOrCreateUser(i.Member.User.ID)
		if err == nil {
			creditorDbID, err := db.GetOrCreateUser(creditorID)
			if err == nil {
				ids, _, _, err := db.GetUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID, 20)
				if err == nil {
					txIDs = ids
				}
			}
		}
	}

	// Validate PromptPay ID
	if promptPayID != "" && !db.IsValidPromptPayID(promptPayID) {
		respondWithError(s, i, fmt.Sprintf("PromptPay ID ไม่ถูกต้อง: %s", promptPayID))
		return
	}

	// Generate QR code for payment
	if promptPayID == "" {
		// If no promptpay provided, try to get from database
		creditorDbID, err := db.GetOrCreateUser(creditorID)
		if err == nil {
			promptPayID, err = db.GetUserPromptPayID(creditorDbID)
			if err != nil {
				promptPayID = ""
			}
		}
	}

	// Acknowledge the modal submission
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "กำลังสร้าง QR Code สำหรับการชำระเงิน...",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	// If we have a valid PromptPay ID, generate QR code
	if promptPayID != "" {
		// Generate QR code
		debtorDiscordID := i.Member.User.ID
		// Use the existing function to generate and send QR code
		generateAndSendQrCode(s, i.ChannelID, promptPayID, amount, debtorDiscordID, description, txIDs)

		// Send a follow-up message to the interaction (ephemeral)
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "✅ QR Code ได้ถูกสร้างและส่งไปในช่องสนทนาแล้ว\n" +
				"หากต้องการยืนยันการชำระเงิน โปรดตอบกลับข้อความ QR Code พร้อมแนบสลิปของคุณ",
		})
	} else {
		// If no PromptPay ID, just send a message
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "⚠️ ไม่สามารถสร้าง QR Code ได้เนื่องจากไม่พบ PromptPay ID ของผู้รับเงิน\n" +
				"กรุณาติดต่อผู้รับเงินเพื่อขอ PromptPay ID หรือช่องทางการชำระเงินอื่น",
		})
	}
}
