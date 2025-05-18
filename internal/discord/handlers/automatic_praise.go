package handlers

import (
	"context"
	"fmt"
	"log"
	"math/rand"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// AutomaticPraiseMessage represents a praise message template
type AutomaticPraiseMessage struct {
	Message string
	Emoji   string
}

// Praise message templates
var praiseMessages = []AutomaticPraiseMessage{
	{Message: "เยี่ยมมาก! ชำระเงินได้เร็วที่สุด คุณเป็นตัวอย่างที่ดีให้กับทุกคน", Emoji: "🏆"},
	{Message: "ขอบคุณมากที่ชำระเงินรวดเร็ว เรารู้สึกดีที่มีคนจ่ายเงินตรงเวลา", Emoji: "⚡"},
	{Message: "ว้าว! นั่นเร็วมาก คุณเป็นผู้ชำระเงินที่รวดเร็วที่สุดในกลุ่มนี้", Emoji: "🚀"},
	{Message: "ขอบคุณสำหรับความรวดเร็ว ทำให้ทุกคนไม่ต้องรอนาน", Emoji: "⏱️"},
	{Message: "คุณเป็นเศรษฐีใจบุญที่ชำระเงินเร็วที่สุด!", Emoji: "💸"},
	{Message: "ความรวดเร็วในการชำระหนี้แสดงถึงความรับผิดชอบของคุณ ขอบคุณมาก", Emoji: "👏"},
	{Message: "ขอปรบมือให้กับผู้ชำระเงินคนแรก! พวกเราซาบซึ้งในความรวดเร็ว", Emoji: "🌟"},
}

// SendAutomaticPraise sends an automatic praise message to the first person who paid
func SendAutomaticPraise(s *discordgo.Session, channelID string, txID int, payerDiscordID string) error {
	// Check if this transaction's rank 1 has already been praised
	var alreadyPraised bool
	err := db.Pool.QueryRow(context.Background(), `
		SELECT received_praise FROM bill_payment_ranking 
		WHERE bill_id = $1 AND rank = 1
	`, txID).Scan(&alreadyPraised)

	if err != nil {
		log.Printf("Error checking if praise already given for TxID %d: %v", txID, err)
		// Continue anyway - might be a brand new payment
		alreadyPraised = false
	}

	if alreadyPraised {
		// Already praised this payment, no need to do it again
		return nil
	}

	// Get transaction details
	txInfo, err := db.GetTransactionInfo(txID)
	if err != nil {
		return fmt.Errorf("error getting transaction info: %w", err)
	}

	// Get creditor (payee) discord ID
	creditorDbID := txInfo["payee_id"].(int)
	creditorDiscordID, err := db.GetDiscordIDFromDbID(creditorDbID)
	if err != nil {
		return fmt.Errorf("error getting creditor discord ID: %w", err)
	}

	// Select a random praise message
	praiseTemplate := praiseMessages[rand.Intn(len(praiseMessages))]

	// Create embed message
	praiseEmbed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("%s การชำระเงินที่รวดเร็ว!", praiseTemplate.Emoji),
		Description: fmt.Sprintf("**<@%s>** %s\n\n"+
			"คุณเป็นคนแรกที่ชำระเงินในรายการนี้ ขอบคุณสำหรับความรวดเร็ว!",
			payerDiscordID, praiseTemplate.Message),
		Color: 0x00FF00, // Green
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "รายละเอียดการชำระเงิน",
				Value: fmt.Sprintf("**จำนวนเงิน:** %.2f บาท\n"+
					"**ผู้รับเงิน:** <@%s>\n"+
					"**TxID:** %d",
					txInfo["amount"].(float64), creditorDiscordID, txID),
			},
		},
	}

	// Send the praise message
	_, err = s.ChannelMessageSendEmbed(channelID, praiseEmbed)
	if err != nil {
		return fmt.Errorf("error sending praise message: %w", err)
	}

	// Mark as praised in the database
	err = db.MarkPraiseGiven(txID, 1)
	if err != nil {
		log.Printf("Error marking praise given: %v", err)
		// Continue anyway
	}

	// Check for badges
	badges, err := db.CheckBadgeEligibility(payerDiscordID)
	if err != nil {
		log.Printf("Error checking badges after automatic praise: %v", err)
	} else if len(badges) > 0 {
		CheckAndAwardBadges(s, payerDiscordID, channelID)
	}

	return nil
}
