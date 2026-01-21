package meow

import (
	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

type Addition struct {
	Nickname   string `json:"nickname" required:"true" help:"您的 MeoW 昵称"`
	BaseURL    string `json:"base_url" default:"https://api.chuckfang.com/" help:"MeoW API 接口地址，详情阅读API文档：https://www.chuckfang.com/MeoW/api_doc.html"`
	Url        string `json:"url" help:"点击消息跳转的链接（可选）请注意链接安全！！"`
	MsgType    string `json:"msg_type" type:"option" default:"text" options:"text,html" help:"消息显示类型：text（默认）或 html，html模板可在消息模板中替换，或让AI给你生成一个。"`
	HtmlHeight int    `json:"html_height" default:"200" help:"HTML 内容高度（像素），仅在 msg_type==html 类型下生效"`
}

func init() {
	factory.RegisterMessageSender(func() factory.IMessageSender {
		return &MeowSender{}
	})
}
