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

// handleMarkPaidButton handles marking a transaction as paid
func handleMarkPaidButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract the transaction ID from the custom ID
	customID := i.MessageComponentData().CustomID
	txIDStr := strings.TrimPrefix(customID, "mark_paid_")

	txID, err := strconv.Atoi(txIDStr)
	if err != nil {
		respondWithError(s, i, "รหัสรายการไม่ถูกต้อง")
		return
	}

	// Get transaction info
	txInfo, err := db.GetTransactionInfo(txID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่พบรายการ TxID %d", txID))
		return
	}

	// Ensure the user is the payee
	payeeDbID := txInfo["payee_id"].(int)
	userDiscordID := i.Member.User.ID
	userDbID, err := db.GetOrCreateUser(userDiscordID)

	if err != nil || userDbID != payeeDbID {
		respondWithError(s, i, "คุณไม่มีสิทธิ์ทำเครื่องหมายรายการนี้เป็นชำระแล้ว")
		return
	}

	// Check if already paid
	alreadyPaid := txInfo["already_paid"].(bool)
	if alreadyPaid {
		respondWithError(s, i, fmt.Sprintf("รายการ TxID %d ถูกทำเครื่องหมายว่าชำระแล้วอยู่แล้ว", txID))
		return
	}

	// Mark as paid
	err = db.MarkTransactionPaidAndUpdateDebt(txID)
	if err != nil {
		respondWithError(s, i, fmt.Sprintf("ไม่สามารถทำเครื่องหมายรายการเป็นชำระแล้ว: %v", err))
		return
	}

	// Get payer info for notification
	payerDiscordID := txInfo["payer_discord_id"].(string)
	amount := txInfo["amount"].(float64)
	description := txInfo["description"].(string)

	// Respond with success message
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ ทำเครื่องหมายรายการ TxID %d เป็นชำระแล้วเรียบร้อย", txID),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error responding to mark paid button: %v", err)
	}

	// Send a public notification
	publicMessage := fmt.Sprintf("✅ <@%s> ได้ทำเครื่องหมายรายการ \"**%s**\" (%.2f บาท) จาก <@%s> เป็นชำระแล้ว",
		userDiscordID, description, amount, payerDiscordID)

	_, err = s.ChannelMessageSend(i.ChannelID, publicMessage)
	if err != nil {
		log.Printf("Error sending public mark paid notification: %v", err)
	}
}
