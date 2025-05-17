package handlers

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// HandleInteractiveMyDebts shows debts with interactive UI
func HandleInteractiveMyDebts(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	userID := m.Author.ID
	userDbID, err := db.GetOrCreateUser(userID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลผู้ใช้ได้")
		return
	}

	// Get debts
	debts, err := db.GetUserDebtsWithDetails(userDbID, true)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลหนี้สินได้")
		return
	}

	// เพิ่มชื่อผู้ใช้จาก Discord
	EnhanceDebtsWithUsernames(s, debts)

	// Create message content
	content := fmt.Sprintf("**หนี้สินของ <@%s> (ที่ต้องจ่ายคนอื่น):**\n\n", userID)

	if len(debts) == 0 {
		content += "ไม่มีหนี้สินค้างชำระในขณะนี้ 🎉"
		s.ChannelMessageSend(m.ChannelID, content)
		return
	}

	// Add summary
	var totalAmount float64
	for _, debt := range debts {
		totalAmount += debt.Amount
		content += fmt.Sprintf("- **%.2f บาท** ให้ <@%s>\n", debt.Amount, debt.OtherPartyDiscordID)
	}
	content += fmt.Sprintf("\n**ยอดรวมทั้งหมด: %.2f บาท**\n", totalAmount)
	content += "คลิกปุ่มด้านล่างเพื่อดูรายละเอียดหรือชำระเงิน"

	// Create components (buttons)
	var components []discordgo.MessageComponent

	// Create ActionsRow for each debt
	for _, debt := range debts {
		// ใช้ชื่อจริงที่ดึงมาจาก Discord
		displayName := debt.OtherPartyName
		if displayName == "" {
			displayName = GetDiscordUsername(s, debt.OtherPartyDiscordID)
		}

		components = append(components, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    fmt.Sprintf("ดูรายละเอียดหนี้ให้ %s", displayName),
					Style:    discordgo.SecondaryButton,
					CustomID: fmt.Sprintf("%sc%s", viewDetailButtonPrefix, debt.OtherPartyDiscordID),
				},
				discordgo.Button{
					Label:    fmt.Sprintf("ชำระเงินให้ %s", displayName),
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("%s%s", payDebtButtonPrefix, debt.OtherPartyDiscordID),
				},
			},
		})
	}

	// Send message with components
	_, err = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:    content,
		Components: components,
	})
	if err != nil {
		log.Printf("Error sending interactive mydebts message: %v", err)
	}
}

// HandleInteractiveOwedToMe shows dues with interactive UI
func HandleInteractiveOwedToMe(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	userID := m.Author.ID
	userDbID, err := db.GetOrCreateUser(userID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลผู้ใช้ได้")
		return
	}

	// Get debts (as creditor, not debtor)
	debts, err := db.GetUserDebtsWithDetails(userDbID, false)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลยอดค้างชำระได้")
		return
	}

	// เพิ่มชื่อผู้ใช้จาก Discord
	EnhanceDebtsWithUsernames(s, debts)

	// Create message content
	content := fmt.Sprintf("**ยอดค้างชำระถึง <@%s> (ที่คนอื่นต้องจ่าย):**\n\n", userID)

	if len(debts) == 0 {
		content += "ไม่มีผู้ใดค้างชำระคุณในขณะนี้ 👍"
		s.ChannelMessageSend(m.ChannelID, content)
		return
	}

	// Add summary
	var totalAmount float64
	for _, debt := range debts {
		totalAmount += debt.Amount
		content += fmt.Sprintf("- <@%s> เป็นหนี้ **%.2f บาท**\n", debt.OtherPartyDiscordID, debt.Amount)
	}
	content += fmt.Sprintf("\n**ยอดรวมทั้งหมด: %.2f บาท**\n", totalAmount)
	content += "คลิกปุ่มด้านล่างเพื่อดูรายละเอียด"

	// Create components (buttons) - one row per debtor
	var components []discordgo.MessageComponent

	for _, debt := range debts {
		// ใช้ชื่อจริงที่ดึงมาจาก Discord
		displayName := debt.OtherPartyName
		if displayName == "" {
			displayName = GetDiscordUsername(s, debt.OtherPartyDiscordID)
		}

		components = append(components, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    fmt.Sprintf("ดูรายละเอียดที่ %s ค้างชำระ", displayName),
					Style:    discordgo.SecondaryButton,
					CustomID: fmt.Sprintf("view_detail_d%s", debt.OtherPartyDiscordID),
				},
				discordgo.Button{
					Label:    fmt.Sprintf("ส่งคำขอชำระเงินไปยัง %s", displayName),
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("request_payment_%s", debt.OtherPartyDiscordID),
				},
			},
		})
	}

	// Send message with components
	_, err = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:    content,
		Components: components,
	})
	if err != nil {
		log.Printf("Error sending interactive owedtome message: %v", err)
	}
}

// HandleSelectTransaction displays a selection UI for transactions
func HandleSelectTransaction(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Parse command arguments: !selecttx [filter]
	var filter string
	if len(args) > 1 {
		filter = strings.ToLower(args[1])
	}

	userID := m.Author.ID
	userDbID, err := db.GetOrCreateUser(userID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลผู้ใช้ได้")
		return
	}

	// Get transactions based on filter
	var txs []map[string]interface{}
	switch filter {
	case "unpaid":
		// Get unpaid transactions where user is debtor
		txs, err = db.GetUserTransactions(userDbID, true, false, 25)
	case "paid":
		// Get paid transactions
		txs, err = db.GetUserTransactions(userDbID, true, true, 25)
	case "due":
		// Get unpaid transactions where user is creditor
		txs, err = db.GetUserTransactions(userDbID, false, false, 25)
	default:
		// Get all transactions involving user (limit to 25 most recent)
		txs, err = db.GetAllUserTransactions(userDbID, 25)
	}

	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลรายการได้")
		return
	}

	if len(txs) == 0 {
		s.ChannelMessageSend(m.ChannelID, "ไม่พบรายการที่ตรงตามเงื่อนไข")
		return
	}

	// Create content header based on filter
	var content string
	switch filter {
	case "unpaid":
		content = "**รายการที่คุณยังไม่ได้ชำระ:**\n"
	case "paid":
		content = "**รายการที่ชำระแล้ว:**\n"
	case "due":
		content = "**รายการที่ผู้อื่นยังไม่ได้ชำระให้คุณ:**\n"
	default:
		content = "**รายการทั้งหมดล่าสุด (25 รายการ):**\n"
	}

	content += "กรุณาเลือกรายการเพื่อดูรายละเอียดและดำเนินการ:\n"

	// Create a selection menu
	var options []discordgo.SelectMenuOption
	for _, tx := range txs {
		txID := tx["id"].(int)
		description := tx["description"].(string)
		amount := tx["amount"].(float64)
		isPaid := tx["already_paid"].(bool)
		otherPartyDiscordID := tx["other_party_discord_id"].(string)

		// ดึงชื่อจริงจาก Discord
		otherPartyName := GetDiscordUsername(s, otherPartyDiscordID)

		// Truncate description if too long
		shortDesc := description
		if len(shortDesc) > 45 {
			shortDesc = shortDesc[:42] + "..."
		}

		// Format option label
		var label string
		if isPaid {
			label = fmt.Sprintf("#%d: %.2f บาท (%s) - ชำระแล้ว", txID, amount, otherPartyName)
		} else {
			label = fmt.Sprintf("#%d: %.2f บาท (%s)", txID, amount, otherPartyName)
		}

		// ถ้าป้ายกำกับยาวเกินไป (Discord จำกัดความยาวที่ 100 ตัวอักษร)
		if len(label) > 90 {
			if isPaid {
				label = fmt.Sprintf("#%d: %.2f บาท - ชำระแล้ว", txID, amount)
			} else {
				label = fmt.Sprintf("#%d: %.2f บาท", txID, amount)
			}
		}

		// Create option
		options = append(options, discordgo.SelectMenuOption{
			Label:       label,
			Description: shortDesc,
			Value:       fmt.Sprintf("tx_%d", txID),
			Emoji: &discordgo.ComponentEmoji{
				Name: func() string {
					if isPaid {
						return "✅"
					}
					return "💸"
				}(),
			},
		})
	}

	// Create the dropdown component
	dropdown := discordgo.SelectMenu{
		CustomID:    debtDropdownID,
		Placeholder: "เลือกรายการที่ต้องการดู",
		Options:     options,
	}

	// Send message with dropdown
	_, err = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content: content,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					dropdown,
				},
			},
		},
	})
	if err != nil {
		log.Printf("Error sending transaction selection menu: %v", err)
	}
}

// HandleInteractiveRequestPayment displays an interactive payment request UI
func HandleInteractiveRequestPayment(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Parse command: !request @user
	if len(args) < 2 || !userMentionRegex.MatchString(args[1]) {
		SendErrorMessage(s, m.ChannelID, "รูปแบบคำสั่งไม่ถูกต้อง โปรดใช้: `!irequest @ลูกหนี้`")
		return
	}

	// Extract IDs
	creditorDiscordID := m.Author.ID
	debtorDiscordID := userMentionRegex.FindStringSubmatch(args[1])[1]

	// ดึงชื่อผู้ใช้จาก Discord
	debtorName := GetDiscordUsername(s, debtorDiscordID)

	// Get DB IDs
	creditorDbID, err := db.GetOrCreateUser(creditorDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลผู้ใช้ได้")
		return
	}

	debtorDbID, err := db.GetOrCreateUser(debtorDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่สามารถดึงข้อมูลลูกหนี้ <@%s> ได้", debtorDiscordID))
		return
	}

	// Get total debt amount
	totalDebtAmount, err := db.GetTotalDebtAmount(debtorDbID, creditorDbID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลยอดหนี้รวมได้")
		return
	}

	if totalDebtAmount <= 0.01 {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("ยอดเยี่ยม! %s ไม่ได้มีหนี้คงค้างกับคุณในขณะนี้", debtorName))
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
	content := fmt.Sprintf("**คำขอชำระเงินถึง %s (<@%s>)**\n"+
		"ยอดค้างชำระทั้งหมด: **%.2f บาท**\n\n"+
		"PromptPay ที่ใช้รับชำระ: `%s`\n\n",
		debtorName, debtorDiscordID, totalDebtAmount, promptPayID)

	if unpaidTxDetails != "" {
		content += "**รายการที่ค้างชำระ:**\n" + unpaidTxDetails
	}

	// Create components
	var components []discordgo.MessageComponent

	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "ส่งคำขอชำระเงิน",
				Style:    discordgo.PrimaryButton,
				CustomID: fmt.Sprintf("%s%s", requestPaymentButtonPrefix, debtorDiscordID),
			},
			discordgo.Button{
				Label:    "ดูรายละเอียดเพิ่มเติม",
				Style:    discordgo.SecondaryButton,
				CustomID: fmt.Sprintf("%sd%s", viewDetailButtonPrefix, debtorDiscordID),
			},
		},
	})

	// Add QR code generation button if we have promptPayID
	if promptPayID != "" && promptPayID != "ไม่พบข้อมูล (กรุณาใช้ !setpromptpay)" {
		// Send message first
		_, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Content:    content,
			Components: components,
		})
		if err != nil {
			log.Printf("Error sending interactive request message: %v", err)
			return
		}

		// Then generate QR code
		GenerateAndSendQrCode(s, m.ChannelID, promptPayID, totalDebtAmount, debtorDiscordID,
			fmt.Sprintf("คำร้องขอชำระหนี้คงค้างจาก <@%s>", creditorDiscordID), unpaidTxIDs)
	} else {
		// Just send the message without QR code
		_, err = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Content:    content + "\n⚠️ ไม่สามารถสร้าง QR Code เนื่องจากไม่พบ PromptPay ID ที่ถูกต้อง",
			Components: components,
		})
		if err != nil {
			log.Printf("Error sending interactive request message: %v", err)
		}
	}
}
