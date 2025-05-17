package handlers

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// HandleMyDebts handles the !mydebts command
func HandleMyDebts(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	queryAndSendDebts(s, m, m.Author.ID, "debtor")
}

// HandleOwedToMe handles the !owedtome and !mydues commands
func HandleOwedToMe(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	queryAndSendDebts(s, m, m.Author.ID, "creditor")
}

// HandleDebtsOfUser handles the !debts command
func HandleDebtsOfUser(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	if len(args) < 2 || !userMentionRegex.MatchString(args[1]) {
		SendErrorMessage(s, m.ChannelID, "รูปแบบไม่ถูกต้อง โปรดใช้ `!debts @user`")
		return
	}
	targetUserDiscordID := userMentionRegex.FindStringSubmatch(args[1])[1]
	queryAndSendDebts(s, m, targetUserDiscordID, "debtor")
}

// HandleDuesForUser handles the !dues command
func HandleDuesForUser(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	if len(args) < 2 || !userMentionRegex.MatchString(args[1]) {
		SendErrorMessage(s, m.ChannelID, "รูปแบบไม่ถูกต้อง โปรดใช้ `!dues @user`")
		return
	}
	targetUserDiscordID := userMentionRegex.FindStringSubmatch(args[1])[1]
	queryAndSendDebts(s, m, targetUserDiscordID, "creditor")
}

// queryAndSendDebts queries and sends debt information
func queryAndSendDebts(s *discordgo.Session, m *discordgo.MessageCreate, principalDiscordID string, mode string) {
	principalDbID, err := db.GetOrCreateUser(principalDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่พบ <@%s> ในฐานข้อมูล", principalDiscordID))
		return
	}

	// Get debts with transaction details from the db package
	isDebtor := mode == "debtor"
	debts, err := db.GetUserDebtsWithDetails(principalDbID, isDebtor)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, "ไม่สามารถดึงข้อมูลหนี้สินได้")
		log.Printf("Error querying debts with details (mode: %s) for %s (dbID %d): %v",
			mode, principalDiscordID, principalDbID, err)
		return
	}

	// ดึงชื่อผู้ใช้จริงจาก Discord
	principalName := GetDiscordUsername(s, principalDiscordID)

	// Format the title based on the mode
	var title string
	if isDebtor {
		title = fmt.Sprintf("หนี้สินของ %s (<@%s>) (ที่ต้องจ่ายคนอื่น):\n", principalName, principalDiscordID)
	} else {
		title = fmt.Sprintf("ยอดค้างชำระถึง %s (<@%s>) (ที่คนอื่นต้องจ่าย):\n", principalName, principalDiscordID)
	}

	// Build the response
	var response strings.Builder
	response.WriteString(title)

	// Handle case with no debts
	if len(debts) == 0 {
		if isDebtor {
			response.WriteString(fmt.Sprintf("%s ไม่มีหนี้สินค้างชำระ! 🎉\n", principalName))
		} else {
			response.WriteString(fmt.Sprintf("ดูเหมือนว่าทุกคนจะชำระหนี้ให้ %s หมดแล้ว 👍\n", principalName))
		}
	} else {
		// Format each debt with its details
		for _, debt := range debts {
			// ดึงชื่อผู้ใช้จริงของฝั่งตรงข้าม
			otherPartyName := GetDiscordUsername(s, debt.OtherPartyDiscordID)

			// Truncate details if too long
			details := debt.Details
			maxDetailLen := 150 // Max length for details string in the summary
			if len(details) > maxDetailLen {
				details = details[:maxDetailLen-3] + "..."
			}

			// Format based on the mode
			if isDebtor {
				response.WriteString(fmt.Sprintf("- **%.2f บาท** ให้ %s (<@%s>) (รายละเอียดล่าสุด: %s)\n",
					debt.Amount, otherPartyName, debt.OtherPartyDiscordID, details))
			} else {
				response.WriteString(fmt.Sprintf("- %s (<@%s>) เป็นหนี้ **%.2f บาท** (รายละเอียดล่าสุด: %s)\n",
					otherPartyName, debt.OtherPartyDiscordID, debt.Amount, details))
			}
		}
	}

	// Send the response
	s.ChannelMessageSend(m.ChannelID, response.String())
}
