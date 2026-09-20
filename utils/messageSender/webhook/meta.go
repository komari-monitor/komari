package webhook

type Addition struct {
	URL         string `json:"url" name:"Webhook URL" required:"true"`
	Method      string `json:"method" name:"HTTP Method" default:"GET" type:"option" options:"POST,GET"`
	ContentType string `json:"content_type" name:"Content Type" default:"application/json"`
	Headers     string `json:"headers" name:"Headers" help:"HTTP headers in JSON format"`
	Body        string `json:"body" name:"Body" default:"{\"message\":\"{{message}}\",\"title\":\"{{title}}\"}"` // 默认使用message和title字段
	Username    string `json:"username" name:"Username"`
	Password    string `json:"password" name:"Password"`
}
