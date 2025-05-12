package discord

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// --- Button Handlers ---

// handlePayDebtButton handles the "Pay Debt" button
func handlePayDebtButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "_")
	if len(parts) < 3 {
		respondWithError(s, i, "รูปแบบ custom ID ไม่ถูกต้อง")
		return
	}

	// Extract the txID (or creditorID if it's a general debt)
	targetID := parts[2]

	var payeeID string
	var amount float64
	var description string
	var txIDs []int

	// Check if this is a transaction-specific button or general debt
	if strings.HasPrefix(targetID, "tx") {
		// Transaction-specific: remove "tx" prefix and get transaction details
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
		amount = txInfo["amount"].(float64)
		description = fmt.Sprintf("ชำระรายการ #%d", txID)
		txIDs = []int{txID}

		// Get Discord ID from DB ID
		payeeDiscordID, err := db.GetDiscordIDFromDbID(payeeDbID)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถระบุผู้รับเงินได้: %v", err))
			return
		}
		payeeID = payeeDiscordID
	} else {
		// General debt: target ID is directly the creditor's Discord ID
		payeeID = targetID
		description = "ชำระหนี้รวม"

		// Get total debt amount
		debtorDbID, err := db.GetOrCreateUser(i.Member.User.ID)
		if err != nil {
			respondWithError(s, i, "ไม่สามารถระบุตัวตนของคุณในระบบ")
			return
		}

		payeeDbID, err := db.GetOrCreateUser(payeeID)
		if err != nil {
			respondWithError(s, i, "ไม่สามารถระบุผู้รับเงินในระบบ")
			return
		}

		amount, err = db.GetTotalDebtAmount(debtorDbID, payeeDbID)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถดึงยอดหนี้สินได้: %v", err))
			return
		}

		// Get unpaid transaction IDs
		ids, _, _, err := db.GetUnpaidTransactionIDsAndDetails(debtorDbID, payeeDbID, 20)
		if err == nil {
			txIDs = ids
		}
	}

	// Get promptPayID for the payee
	payeeDbID, err := db.GetOrCreateUser(payeeID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่สามารถรับข้อมูลผู้รับเงิน: %v", err))
		return
	}

	promptPayID, err := db.GetUserPromptPayID(payeeDbID)
	if err != nil || promptPayID == "" {
		respondWithError(s, i, "ไม่พบข้อมูล PromptPay ของผู้รับเงิน")
		return
	}

	// Acknowledge the interaction
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "กำลังสร้าง QR Code สำหรับการชำระเงิน...",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	// Generate QR code directly
	debtorDiscordID := i.Member.User.ID
	generateAndSendQrCode(s, i.ChannelID, promptPayID, amount, debtorDiscordID, description, txIDs)

	// Send a follow-up message
	s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: "✅ QR Code ได้ถูกสร้างและส่งไปในช่องสนทนาแล้ว\n" +
			"หากต้องการยืนยันการชำระเงิน โปรดตอบกลับข้อความ QR Code พร้อมแนบสลิปของคุณ",
	})
}
