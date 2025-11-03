package serverchan3

import (
    "github.com/komari-monitor/komari/utils/messageSender/factory"
)

// Addition 为 Server酱³ 推送通道的配置项
// 所有字段通过管理页面以 JSON 形式进行设置
type Addition struct {
    // APIURL 为接口完整地址，例如：https://<uid>.push.ft07.com/send/<sendkey>.send
    APIURL string `json:"api_url" required:"true" help:"接口完整地址，例如 https://<uid>.push.ft07.com/send/<sendkey>.send；参考：https://sc3.ft07.com/"`
    // Tags 为可选标签，使用 | 分割，例如：tag1|tag2|tag3
    Tags   string `json:"tags" help:"可选标签，使用 | 分割，例如 tag1|tag2|tag3"`
}

// 注册 Server酱³ 推送通道到工厂
func init() {
    factory.RegisterMessageSender(func() factory.IMessageSender {
        return &ServerChan3Sender{}
    })
}