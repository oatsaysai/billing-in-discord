package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
	promptpayqr "github.com/kazekim/promptpay-qr-go"
	"github.com/spf13/viper"
)

var (
	dg *discordgo.Session
)

func MessageHandler(s *discordgo.Session, m *discordgo.MessageCreate) {

	if m.Author.ID == s.State.User.ID { // Ignore the bot's own messages
		return
	}

	nameUserMap := viper.GetStringMapString("UsernameMapping")
	if strings.Contains(m.Message.Content, "!genQR") {
		tempMsg := m.Message.Content
		tempArr := strings.Split(tempMsg, "\n")

		targetQR := ""
		for _, msg := range tempArr {

			if strings.Contains(msg, "!genQR") {
				data := strings.Split(msg, " ")
				targetQR = data[1]
			}

			if !strings.Contains(msg, "!genQR") {
				data := strings.Split(msg, " ")

				// Gen QR
				qr, err := promptpayqr.QRForTargetWithAmount(targetQR, data[1])
				if err != nil {
					log.Fatal(err)
				}

				_, err = s.ChannelFileSendWithMessage(
					m.ChannelID,
					fmt.Sprintf("<@%s> %s บาท", nameUserMap[data[0]], data[1]),
					data[0]+"temp.png",
					bytes.NewBuffer(*qr),
				)
				if err != nil {
					log.Fatal(err)
				}

			}
		}
	}
}

// DiscordConnect make a new connection to Discord
func DiscordConnect() (err error) {
	dg, err = discordgo.New("Bot " + viper.GetString("DiscordBot.Token"))
	if err != nil {
		log.Println("FATAL: error creating Discord session,", err)
		return
	}

	log.Println("INFO: Bot is Opening")

	dg.AddHandler(MessageHandler)

	// Open Websocket
	err = dg.Open()
	if err != nil {
		log.Println("FATAL: Error Open():", err)
		return
	}

	_, err = dg.User("@me")
	if err != nil {
		// Login unsuccessful
		log.Println("FATAL:", err)
		return
	}

	// Login successful
	log.Println("INFO: Bot is now running. Press CTRL-C to exit.")
	// initRoutine()
	return nil
}

func init() {
	viper.SetConfigName("config")
	viper.AddConfigPath(".")

	viper.SetDefault("Log.Level", "debug")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("unable to read config: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	DiscordConnect()
	<-make(chan struct{})
}
