package handlers

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// HandleStreakCommand handles the !streak command
func HandleStreakCommand(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Check if command is for a specific user
	var targetDiscordID string
	if len(args) > 1 && userMentionRegex.MatchString(args[1]) {
		matches := userMentionRegex.FindStringSubmatch(args[1])
		targetDiscordID = matches[1]
	} else {
		targetDiscordID = m.Author.ID
	}

	// Get user streak information
	streakInfo, err := db.GetUserPaymentStreak(targetDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่สามารถดึงข้อมูล streak ได้: %v", err))
		return
	}

	// Get the username
	var username string
	if targetDiscordID == m.Author.ID {
		username = "คุณ"
	} else {
		username = GetDiscordUsername(s, targetDiscordID)
	}

	// Create the message
	var messageContent strings.Builder
	messageContent.WriteString(fmt.Sprintf("**🔥 Payment Streak ของ %s:**\n\n", username))

	// Current streak info
	messageContent.WriteString(fmt.Sprintf("**Streak ปัจจุบัน:** %d วัน\n", streakInfo.CurrentStreak))
	messageContent.WriteString(fmt.Sprintf("**Streak สูงสุด:** %d วัน\n", streakInfo.LongestStreak))

	// Last payment info
	if !streakInfo.LastPaymentDate.IsZero() {
		timeAgo := formatTimeAgo(time.Since(streakInfo.LastPaymentDate))
		messageContent.WriteString(fmt.Sprintf("\n**การชำระล่าสุด:** %s (%s)\n",
			streakInfo.LastPaymentDate.Format("02/01/2006 15:04:05"), timeAgo))
	} else {
		messageContent.WriteString("\n**การชำระล่าสุด:** ยังไม่มีข้อมูล\n")
	}

	// Payment rank stats
	messageContent.WriteString("\n**สถิติการชำระเงิน:**\n")
	messageContent.WriteString(fmt.Sprintf("🥇 **อันดับ 1:** %d ครั้ง\n", streakInfo.Rank1Count))
	messageContent.WriteString(fmt.Sprintf("🥈 **อันดับ 2:** %d ครั้ง\n", streakInfo.Rank2Count))
	messageContent.WriteString(fmt.Sprintf("🥉 **อันดับ 3:** %d ครั้ง\n", streakInfo.Rank3Count))

	// Total payments (sum of all ranks)
	totalPayments := streakInfo.Rank1Count + streakInfo.Rank2Count + streakInfo.Rank3Count
	messageContent.WriteString(fmt.Sprintf("\n**จำนวนครั้งที่ชำระทั้งหมด:** %d ครั้ง\n", totalPayments))

	// Send the message
	s.ChannelMessageSend(m.ChannelID, messageContent.String())
}

// formatTimeAgo formats a duration as a human-readable "time ago" string
func formatTimeAgo(duration time.Duration) string {
	seconds := int(duration.Seconds())
	minutes := seconds / 60
	hours := minutes / 60
	days := hours / 24

	if days > 0 {
		return fmt.Sprintf("%d วันที่แล้ว", days)
	} else if hours > 0 {
		return fmt.Sprintf("%d ชั่วโมงที่แล้ว", hours)
	} else if minutes > 0 {
		return fmt.Sprintf("%d นาทีที่แล้ว", minutes)
	} else {
		return fmt.Sprintf("%d วินาทีที่แล้ว", seconds)
	}
}
