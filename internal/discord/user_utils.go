package discord

import (
	"github.com/oatsaysai/billing-in-discord/internal/db"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// GetDiscordUsername ดึงชื่อผู้ใช้จาก Discord ID
func GetDiscordUsername(s *discordgo.Session, discordID string) string {
	// ตรวจสอบว่า session ไม่เป็น nil
	if s == nil {
		log.Println("ERROR: Discord session is nil in GetDiscordUsername")
		return "User"
	}

	// ดึงข้อมูลผู้ใช้จาก Discord API
	user, err := s.User(discordID)
	if err != nil {
		log.Printf("Error fetching user info for ID %s: %v", discordID, err)
		return "User"
	}

	// ถ้ามีชื่อแสดงผล (Username) ให้ใช้ Username
	if user.Username != "" {
		// ใช้ display_name หรือ global_name ถ้ามี
		if user.GlobalName != "" {
			return user.GlobalName
		}
		return user.Username
	}

	return "User"
}

// EnhanceDebtsWithUsernames เพิ่มชื่อผู้ใช้จาก Discord ให้กับข้อมูลหนี้สิน
func EnhanceDebtsWithUsernames(s *discordgo.Session, debts []db.DebtDetail) {
	for i := range debts {
		// ตัดอักษร @ หรือ <> ออกจาก Discord ID ถ้ามี
		discordID := strings.TrimPrefix(debts[i].OtherPartyDiscordID, "@")
		discordID = strings.TrimPrefix(discordID, "<@")
		discordID = strings.TrimSuffix(discordID, ">")

		// ดึงชื่อผู้ใช้จาก Discord และตั้งค่าให้กับ OtherPartyName
		debts[i].OtherPartyName = GetDiscordUsername(session, discordID)
	}
}
