package handlers

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// handlePayDebtModalSubmit handles the pay debt modal submission
func handlePayDebtModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the creditor's Discord ID from the custom ID
	customID := i.ModalSubmitData().CustomID
	creditorDiscordID := strings.TrimPrefix(customID, "modal_pay_debt_")

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

	// Extract form data
	var paymentAmount float64
	var paymentNote string

	for _, component := range i.ModalSubmitData().Components {
		for _, c := range component.(*discordgo.ActionsRow).Components {
			input := c.(*discordgo.TextInput)

			if input.CustomID == "payment_amount" {
				paymentAmount, err = strconv.ParseFloat(input.Value, 64)
				if err != nil {
					respondWithError(s, i, "จำนวนเงินไม่ถูกต้อง")
					return
				}
			} else if input.CustomID == "payment_note" {
				paymentNote = input.Value
			}
		}
	}

	// Validate payment amount
	totalDebtAmount, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถดึงข้อมูลยอดหนี้รวมได้")
		return
	}

	if paymentAmount <= 0 {
		respondWithError(s, i, "จำนวนเงินต้องมากกว่า 0")
		return
	}

	if paymentAmount > totalDebtAmount*1.1 { // Allow slight overpayment
		respondWithError(s, i, fmt.Sprintf("จำนวนเงินสูงกว่ายอดหนี้ (%.2f บาท) มากเกินไป", totalDebtAmount))
		return
	}

	// Process payment (for manual payments without slip verification)
	if paymentNote == "" {
		paymentNote = "การชำระเงินผ่านระบบบอท"
	}

	// Get unpaid transaction IDs to mark as paid
	unpaidTxIDs, _, unpaidTotal, err := db.GetUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID, 10)
	if err != nil {
		log.Printf("Error fetching unpaid transactions for modal payment: %v", err)
		// Continue with general debt reduction
		err = db.ReduceDebtFromPayment(debtorDiscordID, creditorDiscordID, paymentAmount)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("เกิดข้อผิดพลาดในการประมวลผลการชำระเงิน: %v", err))
			return
		}
	} else {
		// If payment amount closely matches unpaid total, mark those transactions as paid
		if paymentAmount >= unpaidTotal*0.99 && paymentAmount <= unpaidTotal*1.01 {
			for _, txID := range unpaidTxIDs {
				err = db.MarkTransactionPaidAndUpdateDebt(txID)
				if err != nil {
					log.Printf("Error marking transaction %d as paid: %v", txID, err)
				}
			}
		} else {
			// Otherwise do a general debt reduction
			err = db.ReduceDebtFromPayment(debtorDiscordID, creditorDiscordID, paymentAmount)
			if err != nil {
				respondWithError(s, i, fmt.Sprintf("เกิดข้อผิดพลาดในการประมวลผลการชำระเงิน: %v", err))
				return
			}
		}
	}

	// Respond with a success message
	content := fmt.Sprintf("✅ บันทึกการชำระเงิน %.2f บาท ให้กับ <@%s> เรียบร้อยแล้ว\n", paymentAmount, creditorDiscordID)

	if len(unpaidTxIDs) > 0 {
		content += fmt.Sprintf("รายการเกี่ยวข้อง: %v\n", unpaidTxIDs)
	}

	if paymentNote != "" {
		content += fmt.Sprintf("หมายเหตุ: %s", paymentNote)
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error responding to pay debt modal: %v", err)
	}

	// Notify the creditor in public channel
	publicMessage := fmt.Sprintf("💰 <@%s> ได้ชำระเงิน %.2f บาท ให้กับ <@%s> แล้ว",
		debtorDiscordID, paymentAmount, creditorDiscordID)

	_, err = s.ChannelMessageSend(i.ChannelID, publicMessage)
	if err != nil {
		log.Printf("Error sending public payment notification: %v", err)
	}
}
