package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	pp "github.com/Frontware/promptpay"
	"github.com/bwmarrin/discordgo"
	"github.com/spf13/viper"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
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
				fmt.Println(msg)

				var amountFloat64 float64
				data := strings.Split(msg, " ")

				if s, err := strconv.ParseFloat(data[1], 64); err == nil {
					amountFloat64 = s
				}

				payment := pp.PromptPay{
					PromptPayID: targetQR,      // Tax-ID/ID Card/E-Wallet
					Amount:      amountFloat64, // Positive amount
				}

				qrcodeStr, err := payment.Gen() // Generate string to be use in QRCode
				if err != nil {
					log.Fatal(err)
				}

				fmt.Println(qrcodeStr)

				qrc, err := qrcode.New(qrcodeStr)
				if err != nil {
					fmt.Printf("could not generate QRCode: %v", err)
					return
				}

				filename := data[0] + ".jpg"

				w, err := standard.New(filename)
				if err != nil {
					fmt.Printf("standard.New failed: %v", err)
					return
				}

				// save file
				if err = qrc.Save(w); err != nil {
					fmt.Printf("could not save image: %v", err)
				}

				// Open the file
				file, err := os.Open(filename)
				if err != nil {
					panic(err)
				}
				defer file.Close()

				// Read the entire file into a byte slice
				fileData, err := io.ReadAll(file)
				if err != nil {
					panic(err)
				}

				// Create a new bytes.Buffer and write the data to it
				var buffer bytes.Buffer
				_, err = buffer.Write(fileData)
				if err != nil {
					panic(err)
				}

				_, err = s.ChannelFileSendWithMessage(
					m.ChannelID,
					fmt.Sprintf("<@%s> %s บาท", nameUserMap[data[0]], data[1]),
					data[0]+".png",
					bytes.NewBuffer(buffer.Bytes()),
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
