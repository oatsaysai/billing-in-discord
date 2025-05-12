package discord

import (
	"fmt"
	_ "log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

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

	// Add the Close button to both cases
	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "ปิด",
				Style:    discordgo.DangerButton,
				CustomID: fmt.Sprintf("%s%s", cancelActionButtonPrefix, targetID),
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

// handleMarkPaidButton handles the "Mark as Paid" button
func handleMarkPaidButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "_")
	if len(parts) < 3 {
		respondWithError(s, i, "รูปแบบ custom ID ไม่ถูกต้อง")
		return
	}

	targetID := parts[2]

	// Check if this is for a transaction
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

		// Verify that the user is the payee
		payeeDbID := txInfo["payee_id"].(int)
		payeeDiscordID, err := db.GetDiscordIDFromDbID(payeeDbID)
		if err != nil || payeeDiscordID != i.Member.User.ID {
			respondWithError(s, i, "คุณไม่ใช่ผู้รับเงินสำหรับรายการนี้ ไม่สามารถทำเครื่องหมายว่าชำระแล้ว")
			return
		}

		// Check if already paid
		alreadyPaid := txInfo["already_paid"].(bool)
		if alreadyPaid {
			respondWithError(s, i, "รายการนี้ถูกทำเครื่องหมายว่าชำระแล้ว")
			return
		}

		// Mark as paid
		err = db.MarkTransactionPaidAndUpdateDebt(txID)
		if err != nil {
			respondWithError(s, i, fmt.Sprintf("ไม่สามารถทำเครื่องหมายว่าชำระแล้ว: %v", err))
			return
		}

		// Respond with success message
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("✅ รายการ #%d ถูกทำเครื่องหมายว่าชำระแล้ว", txID),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	} else {
		respondWithError(s, i, "ไม่รองรับการทำเครื่องหมายว่าชำระสำหรับหนี้ทั่วไป โปรดทำเครื่องหมายสำหรับแต่ละรายการ")
	}
}
