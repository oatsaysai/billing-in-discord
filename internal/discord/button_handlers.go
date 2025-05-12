package discord

import (
	"fmt"
	"log"
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
	var err error

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
	}

	// Get promptPayID for the payee
	payeeDbID, err := db.GetOrCreateUser(payeeID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่สามารถรับข้อมูลผู้รับเงิน: %v", err))
		return
	}

	promptPayID, err := db.GetUserPromptPayID(payeeDbID)
	if err != nil {
		promptPayID = "ไม่พบข้อมูล PromptPay"
	}

	// Create and show a modal for payment confirmation
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: fmt.Sprintf("modal_pay_debt_%s", targetID),
			Title:    "ชำระหนี้",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "amount",
							Label:       "จำนวนเงิน",
							Style:       discordgo.TextInputShort,
							Placeholder: "กรอกจำนวนเงินที่ต้องการชำระ",
							Required:    true,
							Value:       fmt.Sprintf("%.2f", amount),
							MinLength:   1,
							MaxLength:   10,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "recipient_prompt_pay",
							Label:       "PromptPay ผู้รับเงิน",
							Style:       discordgo.TextInputShort,
							Placeholder: "PromptPay ID ของผู้รับเงิน",
							Required:    false,
							Value:       promptPayID,
							MinLength:   0,
							MaxLength:   30,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "note",
							Label:       "บันทึกเพิ่มเติม (ถ้ามี)",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "บันทึกเพิ่มเติมสำหรับการชำระเงินนี้",
							Required:    false,
							MaxLength:   100,
						},
					},
				},
			},
		},
	})

	if err != nil {
		log.Printf("Error showing payment modal: %v", err)
	}
}
