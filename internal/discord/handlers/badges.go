package handlers

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// HandleBadgesCommand handles the !badges command
func HandleBadgesCommand(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Check if command is for a specific user
	var targetDiscordID string
	if len(args) > 1 && userMentionRegex.MatchString(args[1]) {
		matches := userMentionRegex.FindStringSubmatch(args[1])
		targetDiscordID = matches[1]
	} else {
		targetDiscordID = m.Author.ID
	}

	// Get user badges
	badges, err := db.GetUserBadges(targetDiscordID)
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่สามารถดึงข้อมูลเหรียญตราได้: %v", err))
		return
	}

	// Get all available badges for showing what's still locked
	allBadges, err := db.GetAllBadges()
	if err != nil {
		SendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไม่สามารถดึงข้อมูลเหรียญตราทั้งหมดได้: %v", err))
		return
	}

	// Create a map of earned badge IDs for easy lookup
	earnedBadgeIDs := make(map[int]bool)
	for _, badge := range badges {
		earnedBadgeIDs[badge.BadgeID] = true
	}

	// Create message
	var messageContent strings.Builder

	// if viewing own badges
	if targetDiscordID == m.Author.ID {
		messageContent.WriteString("**🏆 เหรียญตราและความสำเร็จของคุณ:**\n\n")
	} else {
		username := GetDiscordUsername(s, targetDiscordID)
		messageContent.WriteString(fmt.Sprintf("**🏆 เหรียญตราและความสำเร็จของ %s:**\n\n", username))
	}

	// Show earned badges
	if len(badges) > 0 {
		messageContent.WriteString("**เหรียญที่ได้รับแล้ว:**\n")
		for _, badge := range badges {
			messageContent.WriteString(fmt.Sprintf("%s **%s** - %s\n",
				badge.BadgeEmoji, badge.BadgeName, badge.Description))
		}
	} else {
		messageContent.WriteString("**ยังไม่มีเหรียญที่ได้รับ** - ใช้บอทต่อไปเพื่อปลดล็อกความสำเร็จ!\n")
	}

	// Show locked badges
	var lockedBadges []db.Badge
	for _, badge := range allBadges {
		if !earnedBadgeIDs[badge.ID] {
			lockedBadges = append(lockedBadges, badge)
		}
	}

	if len(lockedBadges) > 0 {
		messageContent.WriteString("\n**เหรียญที่ยังไม่ได้รับ:**\n")
		for _, badge := range lockedBadges {
			messageContent.WriteString(fmt.Sprintf("🔒 **%s** - %s\n",
				badge.Name, badge.Description))
		}
	}

	// Send the message
	s.ChannelMessageSend(m.ChannelID, messageContent.String())
}

// CheckAndAwardBadges checks for new badges after relevant actions and announces them
func CheckAndAwardBadges(s *discordgo.Session, userDiscordID string, channelID string) {
	newBadges, err := db.CheckBadgeEligibility(userDiscordID)
	if err != nil {
		log.Printf("Error checking badge eligibility for user %s: %v", userDiscordID, err)
		return
	}

	if len(newBadges) > 0 {
		// User earned new badges, announce them
		var announcement strings.Builder
		announcement.WriteString(fmt.Sprintf("🎊 **ยินดีด้วย <@%s>!** คุณได้รับเหรียญใหม่:\n\n", userDiscordID))

		for _, badge := range newBadges {
			announcement.WriteString(fmt.Sprintf("%s **%s**\n%s\n\n",
				badge.Emoji, badge.Name, badge.Description))
		}

		announcement.WriteString("พิมพ์ `!badges` เพื่อดูเหรียญทั้งหมดของคุณ")

		s.ChannelMessageSend(channelID, announcement.String())
	}
}
