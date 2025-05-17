package handlers

import (
	"github.com/bwmarrin/discordgo"
)

// HandleHelpCommand handles the !help command
func HandleHelpCommand(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	helpMessage := `
**คำสั่งพื้นฐาน:**
- ` + "`!bill [promptpay_id]`" + ` - สร้างบิลแบ่งจ่าย (ต้องตามด้วยรายการในบรรทัดถัดไป หรือแนบรูปภาพบิล)
- ` + "`!qr <amount> to @user [for <description>] [promptpay_id]`" + ` - สร้าง QR รับชำระจากผู้ใช้
- ` + "`!mydebts`" + ` - ดูยอดหนี้ที่คุณต้องจ่ายผู้อื่น
- ` + "`!mydues`" + ` (หรือ ` + "`!owedtome`" + `) - ดูยอดเงินที่ผู้อื่นเป็นหนี้คุณ
- ` + "`!debts @user`" + ` - ดูยอดหนี้ที่ผู้ใช้รายนั้นเป็นหนี้ผู้อื่น
- ` + "`!dues @user`" + ` - ดูยอดเงินที่ผู้อื่นเป็นหนี้ผู้ใช้รายนั้น
- ` + "`!request @user [promptpay_id]`" + ` - ส่งคำขอชำระเงินไปยังผู้ใช้
- ` + "`!paid <txID>`" + ` - ทำเครื่องหมายว่ารายการชำระแล้ว (ต้องเป็นผู้รับเงินเท่านั้น)

**คำสั่ง Interactive UI:**
- ` + "`!imydebts`" + ` - แสดงยอดหนี้พร้อมปุ่มชำระเงินและดูรายละเอียด
- ` + "`!imydues`" + ` (หรือ ` + "`!iowedtome`" + `) - แสดงยอดเงินที่คนอื่นค้างชำระพร้อมปุ่มดำเนินการ
- ` + "`!selecttx [unpaid|paid|due]`" + ` - เลือกดูรายการธุรกรรมผ่านเมนูเลือก
- ` + "`!irequest @user`" + ` - ส่งคำขอชำระเงินแบบอินเตอร์แอคทีฟ

**คำสั่งจัดการ PromptPay ID:**
- ` + "`!setpromptpay <promptpay_id>`" + ` - ตั้งค่า PromptPay ID ของคุณ
- ` + "`!mypromptpay`" + ` - แสดง PromptPay ID ที่คุณบันทึกไว้

**รูปแบบการสร้างบิล:**
- บรรทัดแรก: ` + "`!bill [promptpay_id]`" + ` (ถ้าไม่ระบุจะใช้ PromptPay ID ที่บันทึกไว้)
- บรรทัดถัดไป (รายการ): ` + "`<amount> for <description> with @user1 @user2...`" + `
- หรือ (รูปแบบสั้น): ` + "`<amount> <description> @user1 @user2...`" + `
- หรือ แนบรูปภาพบิลพร้อมคำสั่ง ` + "`!bill`" + ` เพื่อให้ระบบวิเคราะห์รายการด้วย OCR

**ตัวอย่าง:**
` + "```" + `
!bill 081-234-5678
100 for dinner with @UserA @UserB
50 drinks @UserB
` + "```" + `
หรือ
` + "```" + `
!bill
[แนบรูปภาพบิล]
` + "```" + `

**การตรวจสอบการชำระเงิน:**
คุณสามารถส่งสลิปโดยตอบกลับข้อความ QR code ที่บอทส่งให้ เพื่อตรวจสอบและปรับปรุงยอดหนี้โดยอัตโนมัติ
`
	s.ChannelMessageSend(m.ChannelID, helpMessage)
}
