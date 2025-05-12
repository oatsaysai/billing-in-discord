package discord

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// handleSetPromptPayCommand handles the !setpromptpay command
func handleSetPromptPayCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	if len(parts) != 2 {
		sendErrorMessage(s, m.ChannelID, "รูปแบบไม่ถูกต้อง โปรดใช้: `!setpromptpay <PromptPayID>`")
		return
	}

	promptPayID := parts[1]
	if !db.IsValidPromptPayID(promptPayID) {
		sendErrorMessage(s, m.ChannelID, "PromptPayID ไม่ถูกต้อง โปรดใช้เบอร์โทร 10 หลัก หรือบัตรประชาชน 13 หลัก หรือเริ่มต้นด้วย 'ewallet-'")
		return
	}

	userDbID, err := db.GetOrCreateUser(m.Author.ID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่สามารถพบ/สร้างผู้ใช้ในฐานข้อมูล: %v", err))
		return
	}

	err = db.SetUserPromptPayID(userDbID, promptPayID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, err.Error())
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("✅ บันทึก PromptPay ID `%s` สำหรับ <@%s> เรียบร้อยแล้ว", promptPayID, m.Author.ID))
}

// handleGetMyPromptPayCommand handles the !mypromptpay command
func handleGetMyPromptPayCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	userDbID, err := db.GetOrCreateUser(m.Author.ID)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่สามารถพบ/สร้างผู้ใช้ในฐานข้อมูล: %v", err))
		return
	}

	promptPayID, err := db.GetUserPromptPayID(userDbID)
	if err != nil {
		if strings.Contains(err.Error(), "ยังไม่พบ PromptPay ID") {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("คุณยังไม่ได้ตั้งค่า PromptPay ID กรุณาใช้คำสั่ง `!setpromptpay <PromptPayID>` เพื่อตั้งค่า"))
		} else {
			sendErrorMessage(s, m.ChannelID, err.Error())
		}
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("PromptPay ID ของคุณคือ: `%s`", promptPayID))
}
