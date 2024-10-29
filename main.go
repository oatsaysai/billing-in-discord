package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/makiuchi-d/gozxing"
	gozxingQR "github.com/makiuchi-d/gozxing/qrcode"

	pp "github.com/Frontware/promptpay"
	"github.com/bwmarrin/discordgo"
	"github.com/spf13/viper"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
)

type Person struct {
	Name    string
	Amounts []float64
}

var (
	dg           *discordgo.Session
	waitToVerify = make(map[string]Person)
)

func genQR(s *discordgo.Session, m *discordgo.MessageCreate) {

	nameUserMap := viper.GetStringMapString("UsernameMapping")

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
			defer os.Remove(filename)

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
				filename,
				bytes.NewBuffer(buffer.Bytes()),
			)
			if err != nil {
				log.Fatal(err)
			}

		}
	}
}

func callBill(s *discordgo.Session, m *discordgo.MessageCreate) {

	nameUserMap := viper.GetStringMapString("UsernameMapping")

	tempMsg := m.Message.Content
	tempArr := strings.Split(tempMsg, "\n")

	balances := make(map[string]float64)

	targetQR := ""
	for _, msg := range tempArr {

		if strings.Contains(msg, "!calBill") {
			data := strings.Split(msg, " ")
			targetQR = data[1]
		}

		if !strings.Contains(msg, "!calBill") {

			parts := strings.Fields(msg)
			price, _ := strconv.ParseFloat(parts[1], 64)
			persons := strings.Split(strings.Join(parts[2:], " "), ", ")

			// คำนวณยอดเงินต่อคน
			amountPerPerson := price / float64(len(persons))

			// บันทึกยอดเงินลงใน map
			for _, person := range persons {
				balances[person] += amountPerPerson
			}
		}
	}

	for person, balance := range balances {
		fmt.Printf("%s: %.2f\n", person, balance)

		payment := pp.PromptPay{
			PromptPayID: targetQR, // Tax-ID/ID Card/E-Wallet
			Amount:      balance,  // Positive amount
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

		filename := person + ".jpg"

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
		defer os.Remove(filename)

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
			fmt.Sprintf("<@%s> %.2f บาท", nameUserMap[person], balance),
			filename,
			bytes.NewBuffer(buffer.Bytes()),
		)
		if err != nil {
			log.Fatal(err)
		}

		// Save data to Ram
		tmpPerson, ok := waitToVerify[nameUserMap[person]]
		if ok {
			tmpPerson.Amounts = append(tmpPerson.Amounts, balance)
			waitToVerify[nameUserMap[person]] = tmpPerson
		} else {
			waitToVerify[nameUserMap[person]] = Person{
				Name:    person,
				Amounts: []float64{balance},
			}
		}

		fmt.Println(waitToVerify[nameUserMap[person]])
	}
}

func verifyQR(s *discordgo.Session, m *discordgo.MessageCreate) {
	if len(m.Attachments) > 0 {
		for _, file := range m.Attachments {

			tmpPerson, ok := waitToVerify[m.Author.ID]
			if ok {
				// Download file
				err := DownloadFile(file.Filename, file.URL)
				if err != nil {
					fmt.Println("Error downloading file: ", err)
					return
				}

				// Extract QR
				fileIns, _ := os.Open(file.Filename)
				defer fileIns.Close()
				defer os.Remove(file.Filename)

				img, _, _ := image.Decode(fileIns)

				// prepare BinaryBitmap
				bmp, _ := gozxing.NewBinaryBitmapFromImage(img)

				// decode image
				qrReader := gozxingQR.NewQRCodeReader()
				result, _ := qrReader.Decode(bmp, nil)
				fmt.Println(result)

				for i, amt := range tmpPerson.Amounts {
					// Verify slip
					res, err := VerifySlip(
						viper.GetString("OpenSlipVerify.URL"),
						VerifySlipParams{
							RefNbr: result.String(),
							Amount: fmt.Sprintf("%.2f", amt),
							Token:  viper.GetString("OpenSlipVerify.Token"),
						},
					)
					if err != nil {
						fmt.Println("err: ", err)
						return
					}

					if res.Success {

						waitToVerify[m.Author.ID] = Person{
							Name:    tmpPerson.Name,
							Amounts: removeIndex(tmpPerson.Amounts, i),
						}

						_, err = s.ChannelMessageSend(
							m.ChannelID,
							fmt.Sprintf("รับยอดจาก %s %.2f บาท", res.Data.Sender.DisplayName, res.Data.Amount),
						)
						if err != nil {
							log.Fatal(err)
						}

						fmt.Println(waitToVerify[m.Author.ID])
						break
					}
				}
			}

		}
	}
}

type VerifySlipParams struct {
	RefNbr string `json:"refNbr"`
	Amount string `json:"amount"`
	Token  string `json:"token"`
}

type VerifySlipResponse struct {
	Success       bool   `json:"success"`
	StatusMessage string `json:"statusMessage"`
	Data          struct {
		ReceivingBank string `json:"receivingBank"`
		SendingBank   string `json:"sendingBank"`
		TransRef      string `json:"transRef"`
		TransDate     string `json:"transDate"`
		TransTime     string `json:"transTime"`
		Sender        struct {
			DisplayName string `json:"displayName"`
			Name        string `json:"name"`
			Account     struct {
				Value string `json:"value"`
			} `json:"account"`
		} `json:"sender"`
		Receiver struct {
			DisplayName string `json:"displayName"`
			Name        string `json:"name"`
		} `json:"receiver"`
		Amount float64 `json:"amount"`
		Ref1   string  `json:"ref1"`
		Ref2   string  `json:"ref2"`
		Ref3   string  `json:"ref3"`
	} `json:"data"`
}

func VerifySlip(url string, data VerifySlipParams) (*VerifySlipResponse, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))

	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err

	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	fmt.Printf("res body: %s", body)

	var res VerifySlipResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func messageHandler(s *discordgo.Session, m *discordgo.MessageCreate) {

	if m.Author.ID == s.State.User.ID { // Ignore the bot's own messages
		return
	}

	if strings.Contains(m.Message.Content, "!genQR") {
		genQR(s, m)
	} else if strings.Contains(m.Message.Content, "!calBill") {
		callBill(s, m)
	} else {
		verifyQR(s, m)
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

	dg.AddHandler(messageHandler)

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

func removeIndex(s []float64, index int) []float64 {
	return append(s[:index], s[index+1:]...)
}

func DownloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
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
